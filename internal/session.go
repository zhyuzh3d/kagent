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
	asrOut   chan ASREvent
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

	lastTurnMu   sync.Mutex
	lastTurnText string
	lastTurnAt   time.Time

	lastInterruptTurnID uint64
}

func NewSession(conn *websocket.Conn, cfg *ModelConfig) *Session {
	s := &Session{
		conn:     conn,
		cfg:      cfg,
		asr:      NewDoubaoASRClient(cfg.ASR),
		llm:      NewDoubaoLLMClient(cfg.Chat),
		tts:      NewDoubaoTTSClient(cfg.TTS),
		state:    StateIdle,
		audioIn:  make(chan []byte, upstreamAudioQueueSize),
		asrOut:   make(chan ASREvent, 64),
		control:  make(chan ControlMessage, 32),
		ttsQueue: make(chan TTSChunk, downstreamTTSQueueSize),
	}
	s.pipeline = NewTurnPipeline(s.llm, s.tts, TurnCallbacks{
		OnStatus: func(state string, detail string) {
			s.setState(state, detail)
		},
		OnEvent: func(evt EventMessage) {
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
		case evt := <-s.asrOut:
			s.handleASREvent(evt)
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
		s.startASRWorker()
		s.setState(StateListening, "microphone streaming")
	case "stop":
		s.stopAll()
		s.setState(StateIdle, "stopped")
	case "interrupt":
		tid := s.turnID.Load()
		if tid != s.lastInterruptTurnID {
			log.Printf("[session] interrupt received reason=%s turnID=%d", ctrl.Reason, tid)
			s.lastInterruptTurnID = tid
		}
		s.interruptTurn()
		s.setState(StateInterrupted, "interrupted")
		s.setState(StateListening, "ready for next utterance")
	case "utterance_end":
		text := s.consumeLastASRText()
		if text == "" {
			_ = s.sendEvent(NewTextEvent("turn_nack", s.turnID.Load(), ""))
			return
		}
		s.startTurn(text)
	default:
		s.emitError(0, "unsupported_control", "unsupported control type: "+typ, true)
	}
}

func (s *Session) handleASREvent(evt ASREvent) {
	curTurn := s.turnID.Load()
	switch evt.Type {
	case ASREventPartial:
		s.setState(StateRecognizing, "receiving speech")
		s.lastASRTextMu.Lock()
		s.endpointConsumed = false // New speech is arriving, reset the flag
		s.lastASRTextMu.Unlock()
		s.saveLastASRText(evt.Text)
		_ = s.sendEvent(NewTextEvent("asr_partial", curTurn, evt.Text))
	case ASREventFinal:
		s.setState(StateRecognizing, "speech finalized")
		// Only save if endpoint hasn't already consumed and started a turn.
		// This prevents the race: Endpoint→startTurn, then Final re-saves text,
		// then utterance_end triggers a duplicate startTurn.
		s.lastASRTextMu.Lock()
		if !s.endpointConsumed {
			s.lastASRText = strings.TrimSpace(evt.Text)
		}
		s.lastASRTextMu.Unlock()
		_ = s.sendEvent(NewTextEvent("asr_final", curTurn, evt.Text))
	case ASREventEndpoint:
		s.lastASRTextMu.Lock()
		text := strings.TrimSpace(s.lastASRText)
		s.lastASRText = ""
		s.endpointConsumed = true // Mark: don't let ASREventFinal re-save
		s.lastASRTextMu.Unlock()
		if text != "" {
			log.Printf("[session] ASREventEndpoint startTurn text=%q", text)
			s.startTurn(text)
		}
	}
}

func (s *Session) startASRWorker() {
	asrCtx, cancel := context.WithCancel(s.rootCtx)
	s.asrCancelMu.Lock()
	s.asrCancel = cancel
	s.asrCancelMu.Unlock()

	go func() {
		backoff := time.Second
		for {
			err := s.asr.Run(asrCtx, s.audioIn, s.asrOut)
			if err == nil || errors.Is(err, context.Canceled) || asrCtx.Err() != nil {
				return
			}
			s.emitError(s.turnID.Load(), "asr_failed", err.Error(), true)
			s.setState(StateError, "asr failed")
			s.setState(StateListening, fmt.Sprintf("asr reconnecting in %s", backoff))
			select {
			case <-time.After(backoff):
			case <-asrCtx.Done():
				return
			}
			if backoff < 8*time.Second {
				backoff *= 2
			}
		}
	}()
}

func (s *Session) startTurn(text string) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}
	if s.isDuplicateTurn(clean) {
		return
	}
	s.interruptTurn()
	id := s.turnID.Add(1)
	ctx, cancel := context.WithCancel(s.rootCtx)
	s.turnMu.Lock()
	s.turnCancel = cancel
	s.turnMu.Unlock()

	s.setState(StateThinking, "ai is thinking")
	go func(turnID uint64, input string) {
		err := s.pipeline.RunTurn(ctx, turnID, input)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.emitError(turnID, "turn_failed", err.Error(), true)
			s.setState(StateError, "turn failed")
		}
		if ctx.Err() == nil {
			s.setState(StateListening, "ready for next utterance")
		}
	}(id, clean)
}

func (s *Session) isDuplicateTurn(text string) bool {
	now := time.Now()
	s.lastTurnMu.Lock()
	defer s.lastTurnMu.Unlock()
	if s.lastTurnText == text && now.Sub(s.lastTurnAt) < 3000*time.Millisecond {
		return true
	}
	s.lastTurnText = text
	s.lastTurnAt = now
	return false
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
