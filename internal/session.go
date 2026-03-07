package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	upstreamAudioQueueSize = 64
	downstreamTTSQueueSize = 24
)

type Session struct {
	conn     *websocket.Conn
	cfg      *ModelConfig
	asr      ASRClient
	llm      LLMClient
	tts      TTSClient
	pipeline *TurnPipeline

	stateMu sync.Mutex
	state   string

	writeMu sync.Mutex

	audioIn  chan []byte
	control  chan ControlMessage
	ttsQueue chan TTSChunk

	rootCtx    context.Context
	rootCancel context.CancelFunc

	asrCancelMu sync.Mutex
	asrCancel   context.CancelFunc

	turnMu     sync.Mutex
	turnID     atomic.Uint64
	turnCancel context.CancelFunc

	lastASRTextMu sync.Mutex
	lastASRText   string

	// endpointConsumed prevents ASREventFinal from re-saving text
	// after ASREventEndpoint already consumed it for a turn.
	endpointConsumed bool

	started atomic.Bool

	lastStartedTurnID   uint64
	lastInterruptTurnID uint64

	// Signaled when ASR produces a final transcription for the active turn.
	asrFinalCh chan struct{}

	// Multi-turn conversation history (max 10 rounds = 20 messages)
	historyMu   sync.Mutex
	chatHistory []ChatMessage
}

func NewSession(conn *websocket.Conn, cfg *ModelConfig) *Session {
	activeCfg := cfg.ActiveChat()
	s := &Session{
		conn:     conn,
		cfg:      cfg,
		asr:      NewDoubaoASRClient(cfg.ASR),
		llm:      NewDoubaoLLMClient(activeCfg),
		tts:      NewDoubaoTTSClient(cfg.TTS),
		state:    StateIdle,
		audioIn:  make(chan []byte, upstreamAudioQueueSize),
		control:  make(chan ControlMessage, 32),
		ttsQueue: make(chan TTSChunk, downstreamTTSQueueSize),
		asrFinalCh: make(chan struct{}, 1),
	}
	s.pipeline = NewTurnPipeline(s.llm, s.tts, TurnCallbacks{
		OnStatus: func(state string, detail string) {
			s.setState(state, detail)
		},
		OnEvent: func(evt EventMessage) {
			// Capture assistant final response for multi-turn context
			if evt.Type == "llm_final" && evt.Text != "" {
				s.appendAssistantHistory(evt.Text)
			}
			if err := s.sendEvent(evt); err != nil {
				log.Printf("send event failed: %v", err)
			}
		},
		OnChunk: func(chunk TTSChunk) error {
			return s.enqueueTTS(chunk)
		},
	})
	return s
}

func (s *Session) Run(ctx context.Context) error {
	s.rootCtx, s.rootCancel = context.WithCancel(ctx)
	defer s.cleanup()

	s.setState(StateConnecting, "websocket connected")
	go s.readLoop()
	go s.ttsSenderLoop()

	for {
		select {
		case <-s.rootCtx.Done():
			return nil
		case ctrl, ok := <-s.control:
			if !ok {
				return nil
			}
			s.handleControl(ctrl)
		}
	}
}

func (s *Session) readLoop() {
	defer func() {
		s.rootCancel()
	}()
	for {
		mt, payload, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			log.Printf("read ws failed: %v", err)
			return
		}
		switch mt {
		case websocket.TextMessage:
			var msg ControlMessage
			if err := json.Unmarshal(payload, &msg); err != nil {
				s.emitError(0, "bad_control", "invalid control message", true)
				continue
			}
			if msg.Type == "" {
				continue
			}
			select {
			case s.control <- msg:
			default:
				s.emitError(0, "control_overflow", "control channel is full", true)
			}
		case websocket.BinaryMessage:
			s.pushAudio(payload)
			// NOTE: Backend-side audio energy barge-in has been intentionally removed.
			// The frontend handles barge-in detection with proper echo immunity
			// (sustained RMS blind zone). Backend energy detection was causing
			// self-interrupts because the mic picks up the AI's own playback.
		}
	}
}

func (s *Session) ttsSenderLoop() {
	for {
		select {
		case <-s.rootCtx.Done():
			return
		case chunk := <-s.ttsQueue:
			if err := s.sendTTSChunk(chunk); err != nil {
				log.Printf("send tts chunk failed: %v", err)
				s.emitError(chunk.TurnID, "ws_write_failed", err.Error(), false)
				s.rootCancel()
				return
			}
		}
	}
}

func (s *Session) handleControl(ctrl ControlMessage) {
	typ := strings.ToLower(strings.TrimSpace(ctrl.Type))
	switch typ {
	case "start":
		if s.started.Load() {
			s.setState(StateListening, "already started")
			return
		}
		s.started.Store(true)

		tid := s.turnID.Load()
		if ctrl.TurnID > 0 {
			s.turnID.Store(ctrl.TurnID)
			tid = ctrl.TurnID
		}
		s.startASRTurn(tid) // Start the initial ASR connection for this session

		s.setState(StateListening, "microphone streaming")
	case "stop":
		s.cancelASR()
		s.stopAll()
		s.setState(StateIdle, "stopped")
	case "interrupt":
		tid := s.turnID.Load()
		if ctrl.TurnID > 0 {
			s.turnID.Store(ctrl.TurnID)
			tid = ctrl.TurnID
		}
		if tid != s.lastInterruptTurnID {
			log.Printf("[session] -> interrupt from client: reason=%s turnID=%d", ctrl.Reason, tid)
			s.lastInterruptTurnID = tid
		}

		s.interruptTurn()
		s.cancelASR()

		// Optional: if client sends start_listen explicitly, we don't need to start here.
		// But for now we start the next ASR turn immediately upon interruption.
		s.startASRTurn(tid)

		s.setState(StateInterrupted, "interrupted")
		s.setState(StateListening, "ready for next utterance")
	case "trigger_llm":
		// The new unified trigger from Client-Driven Architecture.
		tid := s.turnID.Load()
		if ctrl.TurnID > 0 {
			s.turnID.Store(ctrl.TurnID)
			tid = ctrl.TurnID
		}

		s.asr.Finish()
		select {
		case <-s.asrFinalCh:
			log.Printf("[session] ASR final received for trigger_llm turnID=%d", tid)
		case <-time.After(320 * time.Millisecond):
			log.Printf("[session] ASR final wait timed out for trigger_llm turnID=%d; falling back", tid)
		}

		// Always consume backend ASR text to prevent stale text leaking to next turn.
		lastSpeech := s.consumeLastASRText()
		// Prefer backend final/partial text if present; fall back to frontend text snapshot.
		text := lastSpeech
		if text == "" {
			text = ctrl.Text
		}

		log.Printf("[session] -> trigger_llm: text=%q turnID=%d", text, tid)
		s.cancelASR()

		if text != "" {
			s.startTurn(text, tid)
			return
		}
		_ = s.sendEvent(EventMessage{Type: "turn_nack", TsMS: nowMS(), TurnID: tid})
		s.setState(StateListening, "no speech detected")
	case "start_listen":
		// Explicit signal from frontend that a new turn is starting voice input
		tid := s.turnID.Load()
		if ctrl.TurnID > 0 {
			s.turnID.Store(ctrl.TurnID)
			tid = ctrl.TurnID
		}
		log.Printf("[session] -> start_listen from client: turnID=%d", tid)
		s.interruptTurn()
		s.cancelASR()
		s.startASRTurn(tid)
		s.setState(StateListening, "listening to user")
	case "utterance_end":
		// Backward compatibility fallback until frontend is fully updated mapping VAD to trigger_llm.
		tid := s.turnID.Load()
		text := s.consumeLastASRText()
		log.Printf("[session] (legacy) utterance_end triggering startTurn text=%q turnID=%d", text, tid)
		s.cancelASR()
		if text != "" {
			s.startTurn(text, tid)
		}
	default:
		s.emitError(s.turnID.Load(), "unsupported_control", "unsupported control type: "+typ, true)
	}
}

func (s *Session) handleASREvent(evt ASREvent, explicitTurnID uint64) {
	switch evt.Type {
	case ASREventPartial:
		s.lastASRTextMu.Lock()
		if s.endpointConsumed {
			// A new utterance has started after the previous one was consumed!
			// Interrupt any ongoing AI generation for the previous turn.
			s.interruptTurnLocked()
			// Increment turn ID so this new utterance gets a fresh turn.
			s.turnID.Add(1)
			s.endpointConsumed = false
		}
		s.lastASRTextMu.Unlock()

		s.setState(StateRecognizing, "receiving speech")
		s.saveLastASRText(evt.Text)
		_ = s.sendEvent(NewTextEvent("asr_partial", explicitTurnID, evt.Text))

	case ASREventFinal:
		s.setState(StateRecognizing, "speech finalized")
		s.lastASRTextMu.Lock()
		if !s.endpointConsumed {
			s.lastASRText = strings.TrimSpace(evt.Text)
		}
		s.lastASRTextMu.Unlock()
		// Always send asr_final with the explicitly bound turn ID!
		_ = s.sendEvent(NewTextEvent("asr_final", explicitTurnID, evt.Text))
		select {
		case s.asrFinalCh <- struct{}{}:
		default:
		}

	case ASREventEndpoint:
		s.lastASRTextMu.Lock()
		text := strings.TrimSpace(s.lastASRText)
		s.lastASRText = ""
		s.endpointConsumed = true
		s.lastASRTextMu.Unlock()

		// In the pure Client-Driven architecture, ASR Endpoint MUST NOT securely trigger the LLM.
		// The LLM trigger authority belongs entirely to the Frontend via `trigger_llm`.
		// But during transition, we only log it.
		if text != "" {
			log.Printf("[session] ASR returned EndPoint text=%q for turnID=%d. Awaiting frontend trigger_llm.", text, explicitTurnID)
		}
	}
}

// interruptTurnLocked performs interruption without needing s.lastASRTextMu since it might be held
func (s *Session) interruptTurnLocked() {
	s.turnMu.Lock()
	if s.turnCancel != nil {
		s.turnCancel()
		s.turnCancel = nil
	}
	s.turnMu.Unlock()
}

func (s *Session) cancelASR() {
	s.asrCancelMu.Lock()
	defer s.asrCancelMu.Unlock()
	if s.asrCancel != nil {
		s.asrCancel()
		s.asrCancel = nil
	}
}

// startASRTurn creates a new physically isolated ASR WebSocket connection exactly tied to one turn.
func (s *Session) startASRTurn(turnID uint64) {
	s.cancelASR() // Drop old connection if it exists

	s.lastASRTextMu.Lock()
	s.lastASRText = ""
	s.endpointConsumed = false
	s.lastASRTextMu.Unlock()
	s.flushAudioQueue()
	select {
	case <-s.asrFinalCh:
	default:
	}

	ctx, cancel := context.WithCancel(s.rootCtx)
	s.asrCancelMu.Lock()
	s.asrCancel = cancel
	s.asrCancelMu.Unlock()

	history := s.getHistory() // snapshot current history for this specific turn

	go func() {
		defer cancel() // auto clean if finished normally
		log.Printf("[asr] Connecting dedicated ASR WebSocket for turnID=%d", turnID)

		events := make(chan ASREvent, 64)
		stopCh := make(chan struct{})

		go func() {
			for {
				select {
				case <-ctx.Done():
					close(stopCh)
					return
				case evt, ok := <-events:
					if !ok {
						close(stopCh)
						return
					}
					s.handleASREvent(evt, turnID) // PERFECT TAGGING!
				}
			}
		}()

		err := s.asr.Run(ctx, s.audioIn, events, history)
		close(events)
		<-stopCh // wait for events to flush

		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil && s.rootCtx.Err() == nil {
			log.Printf("[asr] turnID=%d run error: %v", turnID, err)
			s.emitError(turnID, "asr_failed", err.Error(), true)
		}
		log.Printf("[asr] Dedicated connection for turnID=%d closed", turnID)
	}()
}

func (s *Session) startTurn(text string, targetTurnID uint64) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}

	s.turnMu.Lock()
	if s.lastStartedTurnID == targetTurnID {
		s.turnMu.Unlock()
		log.Printf("[session] startTurn ignored: turnID %d already launched", targetTurnID)
		return
	}
	s.lastStartedTurnID = targetTurnID
	s.turnMu.Unlock()

	s.interruptTurn()
	// Removed s.turnID.Add(1) - TurnID is now incremented upon receiving the first ASREventPartial for a new utterance
	ctx, cancel := context.WithCancel(s.rootCtx)
	s.turnMu.Lock()
	s.turnCancel = cancel
	s.turnMu.Unlock()

	// Capture current history snapshot and append user message
	s.historyMu.Lock()
	history := make([]ChatMessage, len(s.chatHistory))
	copy(history, s.chatHistory)
	s.chatHistory = append(s.chatHistory, ChatMessage{Role: "user", Content: clean})
	s.trimHistory()
	s.historyMu.Unlock()

	s.setState(StateThinking, "ai is thinking")
	go func(turnID uint64, input string, hist []ChatMessage) {
		err := s.pipeline.RunTurn(ctx, turnID, input, hist)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.emitError(turnID, "turn_failed", err.Error(), true)
			s.setState(StateError, "turn failed")
		}
		if ctx.Err() == nil {
			s.setState(StateListening, "ready for next utterance")
		}
	}(targetTurnID, clean, history)
}

func (s *Session) interruptTurn() {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		s.turnCancel()
		s.turnCancel = nil
	}
	s.flushTTSQueue()
}

func (s *Session) stopAll() {
	s.interruptTurn()
	s.started.Store(false)
	s.asrCancelMu.Lock()
	if s.asrCancel != nil {
		s.asrCancel()
		s.asrCancel = nil
	}
	s.asrCancelMu.Unlock()
	s.flushAudioQueue()
}

func (s *Session) cleanup() {
	s.stopAll()
	if s.rootCancel != nil {
		s.rootCancel()
	}
	_ = s.conn.Close()
}

func (s *Session) setState(state string, detail string) {
	s.stateMu.Lock()
	s.state = state
	s.stateMu.Unlock()
	if err := s.sendEvent(NewStatusEvent(s.turnID.Load(), state, detail)); err != nil {
		log.Printf("send status failed: %v", err)
	}
}

func (s *Session) getState() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state
}

func (s *Session) emitError(turnID uint64, code string, message string, recoverable bool) {
	_ = s.sendEvent(NewErrorEvent(turnID, code, message, recoverable))
}

func (s *Session) sendEvent(evt EventMessage) error {
	b, err := encodeEvent(evt)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, b)
}

func (s *Session) sendTTSChunk(chunk TTSChunk) error {
	evt := NewTTSChunkEvent(chunk.TurnID, chunk.Seq, chunk.Format)
	evtPayload, err := encodeEvent(evt)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, evtPayload); err != nil {
		return err
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, chunk.Data)
}

func (s *Session) enqueueTTS(chunk TTSChunk) error {
	select {
	case s.ttsQueue <- chunk:
		return nil
	default:
		return fmt.Errorf("tts queue full (%d)", downstreamTTSQueueSize)
	}
}

func (s *Session) flushTTSQueue() {
	for {
		select {
		case <-s.ttsQueue:
		default:
			return
		}
	}
}

func (s *Session) pushAudio(frame []byte) {
	cp := append([]byte(nil), frame...)
	select {
	case s.audioIn <- cp:
	default:
		select {
		case <-s.audioIn:
		default:
		}
		select {
		case s.audioIn <- cp:
		default:
		}
	}
}

func (s *Session) flushAudioQueue() {
	for {
		select {
		case <-s.audioIn:
		default:
			return
		}
	}
}

func significantEnergy(frame []byte) bool {
	if len(frame) < 4 {
		return false
	}
	var sum int64
	count := 0
	for i := 0; i+1 < len(frame); i += 2 {
		v := int16(frame[i]) | int16(frame[i+1])<<8
		if v < 0 {
			v = -v
		}
		sum += int64(v)
		count++
	}
	if count == 0 {
		return false
	}
	avg := sum / int64(count)
	return avg > 420
}

func (s *Session) saveLastASRText(text string) {
	s.lastASRTextMu.Lock()
	s.lastASRText = strings.TrimSpace(text)
	s.lastASRTextMu.Unlock()
}

func (s *Session) consumeLastASRText() string {
	s.lastASRTextMu.Lock()
	defer s.lastASRTextMu.Unlock()
	out := strings.TrimSpace(s.lastASRText)
	s.lastASRText = ""
	return out
}

const maxHistoryMessages = 20 // 10 rounds of user+assistant

// trimHistory keeps only the last maxHistoryMessages messages.
// Must be called with historyMu held.
func (s *Session) trimHistory() {
	if len(s.chatHistory) > maxHistoryMessages {
		s.chatHistory = s.chatHistory[len(s.chatHistory)-maxHistoryMessages:]
	}
}

// appendAssistantHistory appends an assistant reply to the conversation history.
func (s *Session) appendAssistantHistory(text string) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}
	s.historyMu.Lock()
	s.chatHistory = append(s.chatHistory, ChatMessage{Role: "assistant", Content: clean})
	s.trimHistory()
	s.historyMu.Unlock()
}

// getHistory returns a snapshot of the current conversation history.
func (s *Session) getHistory() []ChatMessage {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	out := make([]ChatMessage, len(s.chatHistory))
	copy(out, s.chatHistory)
	return out
}
