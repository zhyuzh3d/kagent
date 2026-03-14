package app

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Session struct {
	conn          *websocket.Conn
	cfg           *ModelConfig
	runtimeConfig *RuntimeConfigManager
	sqliteStore   *SQLiteStore
	asr           ASRClient
	llm           LLMClient
	tts           TTSClient
	pipeline      *TurnPipeline
	opsLogger     *OperationLogger

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

	draftMu            sync.Mutex
	assistantDrafts    map[uint64]string
	assistantFinalized map[uint64]struct{}

	interruptMu   sync.Mutex
	turnInterrupt map[uint64]string

	actionMu            sync.Mutex
	userTurnActive      bool
	continuationRunning bool
	continuationSeq     uint64
	pendingFollowups    []ChatMessage
	followupFlushTimer  *time.Timer
	actionRateWindow    []int64
	actionDedup         map[string]int64

	actionRefMu      sync.Mutex
	actionCallRefIDs map[string]string
}

func NewSession(conn *websocket.Conn, cfg *ModelConfig, runtimeConfig *RuntimeConfigManager, sqliteStore *SQLiteStore) *Session {
	activeCfg := cfg.ActiveChat()
	publicCfg := defaultPublicConfig()
	if runtimeConfig != nil {
		publicCfg = runtimeConfig.Snapshot()
	}
	audioQueueSize := publicCfg.Chat.Session.UpstreamAudioQueueSize
	if audioQueueSize <= 0 {
		audioQueueSize = defaultPublicConfig().Chat.Session.UpstreamAudioQueueSize
	}
	controlQueueSize := publicCfg.Chat.Session.ControlQueueSize
	if controlQueueSize <= 0 {
		controlQueueSize = defaultPublicConfig().Chat.Session.ControlQueueSize
	}
	ttsQueueSize := publicCfg.Chat.Session.DownstreamTTSQueueSize
	if ttsQueueSize <= 0 {
		ttsQueueSize = defaultPublicConfig().Chat.Session.DownstreamTTSQueueSize
	}
	s := &Session{
		conn:               conn,
		cfg:                cfg,
		runtimeConfig:      runtimeConfig,
		sqliteStore:        sqliteStore,
		asr:                NewDoubaoASRClient(cfg.ASR, runtimeConfig),
		llm:                NewDoubaoLLMClient(activeCfg, runtimeConfig),
		tts:                NewDoubaoTTSClient(cfg.TTS, runtimeConfig),
		state:              StateIdle,
		audioIn:            make(chan []byte, audioQueueSize),
		control:            make(chan ControlMessage, controlQueueSize),
		ttsQueue:           make(chan TTSChunk, ttsQueueSize),
		asrFinalCh:         make(chan struct{}, 1),
		actionDedup:        map[string]int64{},
		actionCallRefIDs:   map[string]string{},
		assistantDrafts:    map[uint64]string{},
		assistantFinalized: map[uint64]struct{}{},
		turnInterrupt:      map[uint64]string{},
		opsLogger:          NewOperationLogger(sqliteStore.userID),
	}
	s.pipeline = NewTurnPipeline(s.llm, s.tts, runtimeConfig, TurnCallbacks{
		OnStatus: func(turnID uint64, state string, detail string) {
			s.setTurnState(turnID, state, detail)
		},
		OnEvent: func(evt EventMessage) {
			if evt.Type == "llm_delta" {
				s.appendAssistantDraft(evt.TurnID, evt.Text)
			}
			if evt.Type == "llm_final" {
				s.finalizeAssistantMessage(evt.TurnID, evt.Text, CompletionStatusComplete, InterruptNone, 0)
			}
			if err := s.sendEvent(evt); err != nil {
				Errorf("send event failed: %v", err)
			}
		},
		OnChunk: func(chunk TTSChunk) error {
			return s.enqueueTTS(chunk)
		},
	})
	s.bootstrapHistoryFromSQLite()
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
			Debugf("read ws failed: %v", err)
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
				Errorf("send tts chunk failed: %v", err)
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
		s.setUserTurnActive(false)
		s.appendHistoryMessage(ChatMessage{
			TurnID:      ctrl.TurnID,
			Role:        RoleSystem,
			Category:    CategoryPhase,
			MessageType: TypeConvoStart,
			PayloadJSON: `{"reason":"start"}`,
			CreatedAtMS: nowMS(),
		})

		tid := s.adoptTurnID(ctrl.TurnID)
		s.startASRTurn(tid) // Start the initial ASR connection for this session

		s.setState(StateListening, "microphone streaming")
	case "stop":
		s.cancelASR()
		s.appendHistoryMessage(ChatMessage{
			TurnID:      ctrl.TurnID,
			Role:        RoleSystem,
			Category:    CategoryPhase,
			MessageType: TypeConvoStop,
			PayloadJSON: `{"reason":"stop"}`,
			CreatedAtMS: nowMS(),
		})
		s.stopAll()
		s.setUserTurnActive(false)
		s.setState(StateIdle, "stopped")
	case "interrupt":
		tid := s.adoptTurnID(ctrl.TurnID)
		if tid != s.lastInterruptTurnID {
			Infof("[Turn:%d] -> VAD interrupt received (reason=%s)", tid, ctrl.Reason)
			s.lastInterruptTurnID = tid
		}

		s.interruptTurnWithReason(InterruptVAD)
		s.cancelASR()

		// Optional: if client sends start_listen explicitly, we don't need to start here.
		// But for now we start the next ASR turn immediately upon interruption.
		s.startASRTurn(tid)

		s.setState(StateInterrupted, "interrupted")
		s.setState(StateListening, "ready for next utterance")
	case "trigger_llm":
		// The new unified trigger from Client-Driven Architecture.
		tid := s.adoptTurnID(ctrl.TurnID)

		s.asr.Finish()
		select {
		case <-s.asrFinalCh:
			Debugf("[Turn:%d] ASR final received for trigger_llm", tid)
		case <-time.After(s.triggerLLMWaitFinal()):
			Warnf("[Turn:%d] ASR final wait timed out for trigger_llm; falling back", tid)
		}

		// Always consume backend ASR text to prevent stale text leaking to next turn.
		lastSpeech := s.consumeLastASRText()
		// Prefer backend final/partial text if present; fall back to frontend text snapshot.
		text := lastSpeech
		if text == "" {
			text = ctrl.Text
		}

		if text != "" {
			Infof("[Turn:%d] %q -> LLM Triggered", tid, Snippet(text))
		} else {
			Debugf("[Turn:%d] \"\" -> LLM Trigger (skipped, empty text)", tid)
		}

		s.cancelASR()
		s.setUserTurnActive(false)

		if text != "" {
			s.startTurn(text, tid)
			return
		}
		_ = s.sendEvent(EventMessage{Type: "turn_nack", TsMS: nowMS(), TurnID: tid})
		Infof("[Turn:%d] turn_nack sent to frontend, skipped DB persistence", tid)
		s.setState(StateListening, "no speech detected")
		s.tryStartContinuation()
	case "start_listen":
		// Explicit signal from frontend that a new turn is starting voice input
		tid := s.adoptTurnID(ctrl.TurnID)
		Infof("[Turn:%d] -> VAD listening started", tid)
		if shouldInterruptOnStartListen(s.getState()) {
			s.interruptTurnWithReason(InterruptVAD)
		}
		s.setUserTurnActive(true)
		s.cancelASR()
		s.startASRTurn(tid)
		s.setState(StateListening, "listening to user")
	case "utterance_end":
		// Backward compatibility fallback until frontend is fully updated mapping VAD to trigger_llm.
		tid := s.turnID.Load()
		text := s.consumeLastASRText()

		if text != "" {
			Infof("[Turn:%d] %q -> Legacy utterance end", tid, Snippet(text))
		} else {
			Debugf("[Turn:%d] \"\" -> Legacy utterance end (skipped, empty text)", tid)
		}

		s.cancelASR()
		s.setUserTurnActive(false)
		if text != "" {
			s.startTurn(text, tid)
		} else {
			s.tryStartContinuation()
		}
	case "action_result":
		s.handleActionResult(ctrl)
	case "state_change":
		s.handleStateChange(ctrl)
	case "page_close":
		s.appendHistoryMessage(ChatMessage{
			TurnID:      ctrl.TurnID,
			Role:        RoleSystem,
			Category:    CategoryPhase,
			MessageType: TypePageClose,
			PayloadJSON: fmt.Sprintf(`{"reason":%q}`, strings.TrimSpace(ctrl.Reason)),
			CreatedAtMS: nowMS(),
		})
	case "config_change":
		s.handleConfigChange(ctrl)
	case "fetch_history":
		s.handleFetchHistory(ctrl)
	default:
		s.emitError(s.turnID.Load(), "unsupported_control", "unsupported control type: "+typ, true)
	}
}

func (s *Session) handleFetchHistory(ctrl ControlMessage) {
	if s.sqliteStore == nil {
		Warnf("sqliteStore is nil, ignoring fetch_history")
		return
	}
	limit := ctrl.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	beforeID := ctrl.BeforeID
	if beforeID <= 0 {
		beforeID = ctrl.Cursor
	}

	Debugf("received fetch_history: before_id=%d cursor=%d limit=%d", beforeID, ctrl.Cursor, limit)
	history, hasMore, err := s.sqliteStore.LoadContextBeforeWithMode(beforeID, limit, ctrl.ShowMore)
	if err != nil {
		Errorf("fetch history failed: %v", err)
		return
	}

	Debugf("fetch_history returning %d messages, hasMore=%v", len(history), hasMore)

	evt := EventMessage{
		Type:     "history_sync",
		TsMS:     nowMS(),
		TurnID:   s.turnID.Load(),
		Messages: history,
		HasMore:  hasMore,
	}
	if err := s.sendEvent(evt); err != nil {
		Errorf("send history_sync failed: %v", err)
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

		s.maybeInterruptForRecognizedSpeech(explicitTurnID, evt.Text)

		s.setState(StateRecognizing, "receiving speech")
		s.saveLastASRText(evt.Text)
		_ = s.sendEvent(NewTextEvent("asr_partial", explicitTurnID, evt.Text))

	case ASREventFinal:
		s.maybeInterruptForRecognizedSpeech(explicitTurnID, evt.Text)
		s.setState(StateRecognizing, "speech finalized")
		s.lastASRTextMu.Lock()
		if !s.endpointConsumed {
			s.lastASRText = strings.TrimSpace(evt.Text)
		}
		s.lastASRTextMu.Unlock()

		if text := strings.TrimSpace(evt.Text); text != "" {
			Infof("[Turn:%d] %q -> ASR final", explicitTurnID, Snippet(text))
		}

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
			Debugf("[Turn:%d] %q -> ASR endpoint, await trigger_llm", explicitTurnID, Snippet(text))
		}
	}
}

// interruptTurnLocked performs interruption without needing s.lastASRTextMu since it might be held
func (s *Session) interruptTurnLocked() {
	s.turnMu.Lock()
	if s.turnCancel != nil {
		if s.lastStartedTurnID > 0 {
			s.recordTurnInterrupt(s.lastStartedTurnID, InterruptVAD)
		}
		s.turnCancel()
		s.turnCancel = nil
	}
	s.turnMu.Unlock()
}

func shouldInterruptOnStartListen(state string) bool {
	return state == StateSpeaking
}

func shouldInterruptForRecognizedSpeech(state string, activeGeneratedTurnID uint64, speechTurnID uint64, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if speechTurnID == 0 || activeGeneratedTurnID == 0 || speechTurnID <= activeGeneratedTurnID {
		return false
	}
	return state == StateThinking || state == StateSpeaking
}

func (s *Session) maybeInterruptForRecognizedSpeech(turnID uint64, text string) {
	activeState := s.getState()
	s.turnMu.Lock()
	activeGeneratedTurnID := s.lastStartedTurnID
	hasActiveTurn := s.turnCancel != nil
	s.turnMu.Unlock()

	if !hasActiveTurn || !shouldInterruptForRecognizedSpeech(activeState, activeGeneratedTurnID, turnID, text) {
		return
	}
	Infof("[Turn:%d] %q -> Interrupt active response after confirmed user speech", turnID, Snippet(text))
	s.interruptTurnWithReason(InterruptVAD)
}

func (s *Session) adoptTurnID(proposed uint64) uint64 {
	current := s.turnID.Load()
	if proposed > current {
		s.turnID.Store(proposed)
		return proposed
	}
	return current
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
		Debugf("[Turn:%d] Connecting dedicated ASR WebSocket", turnID)

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
			Errorf("[Turn:%d] asr run error: %v", turnID, err)
			s.emitError(turnID, "asr_failed", err.Error(), true)
		}
		Debugf("[Turn:%d] Dedicated ASR connection closed", turnID)
	}()
}

func (s *Session) startTurn(text string, targetTurnID uint64) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}
	s.clearPendingFollowupsForUserTurn()

	s.turnMu.Lock()
	if s.lastStartedTurnID == targetTurnID {
		remapped := s.turnID.Add(1)
		Warnf("[Turn:%d] startTurn turn_id collision detected; remap to turn=%d", targetTurnID, remapped)
		targetTurnID = remapped
	}
	s.lastStartedTurnID = targetTurnID
	s.turnMu.Unlock()

	s.interruptTurnWithReason(InterruptOther)
	// Removed s.turnID.Add(1) - TurnID is now incremented upon receiving the first ASREventPartial for a new utterance
	ctx, cancel := context.WithCancel(s.rootCtx)
	s.turnMu.Lock()
	s.turnCancel = cancel
	s.turnMu.Unlock()
	history := s.getHistory()

	s.appendHistoryMessage(ChatMessage{
		TurnID:      targetTurnID,
		Role:        RoleUser,
		Category:    CategoryChat,
		MessageType: TypeUserMessage,
		Content:     clean,
		PayloadJSON: fmt.Sprintf(`{"text":%q,"origin":"user_turn"}`, clean),
		CreatedAtMS: nowMS(),
	})

	s.setState(StateThinking, "ai is thinking")
	go func(turnID uint64, input string, hist []ChatMessage) {
		err := s.pipeline.RunTurn(ctx, turnID, input, hist)
		if errors.Is(err, context.Canceled) {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusInterrupted, s.consumeTurnInterrupt(turnID), 0)
			return
		}
		if err != nil {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusError, InterruptOther, 0)
			Errorf("[Turn:%d] turn failed: %v", turnID, err)
			s.emitError(turnID, "turn_failed", err.Error(), true)
			s.setTurnState(turnID, StateError, "turn failed")
			return
		}
		if ctx.Err() == nil {
			s.setTurnState(turnID, StateListening, "ready for next utterance")
			s.tryStartContinuation()
		}
	}(targetTurnID, clean, history)
}

func (s *Session) interruptTurnWithReason(reason string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		if s.lastStartedTurnID > 0 {
			s.recordTurnInterrupt(s.lastStartedTurnID, reason)
		}
		s.turnCancel()
		s.turnCancel = nil
	}
	s.flushTTSQueue()
}

func (s *Session) stopAll() {
	s.interruptTurnWithReason(InterruptManual)
	s.started.Store(false)
	s.actionMu.Lock()
	s.userTurnActive = false
	s.continuationRunning = false
	s.pendingFollowups = nil
	if s.followupFlushTimer != nil {
		s.followupFlushTimer.Stop()
		s.followupFlushTimer = nil
	}
	s.actionMu.Unlock()
	s.actionRefMu.Lock()
	s.actionCallRefIDs = map[string]string{}
	s.actionRefMu.Unlock()
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
	if s.opsLogger != nil {
		_ = s.opsLogger.Close()
	}
}

func (s *Session) setState(state string, detail string) {
	s.setTurnState(s.turnID.Load(), state, detail)
}

func (s *Session) setTurnState(turnID uint64, state string, detail string) {
	s.stateMu.Lock()
	s.state = state
	s.stateMu.Unlock()
	if err := s.sendEvent(NewStatusEvent(turnID, state, detail)); err != nil {
		Errorf("send status failed: %v", err)
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
	if s.conn == nil {
		return nil
	}
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
	if s.conn == nil {
		return nil
	}
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
		return fmt.Errorf("tts queue full (%d)", cap(s.ttsQueue))
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

func (s *Session) sessionAnchorLimit() int {
	limit := s.publicConfig().Chat.Session.MaxHistoryMessages
	if limit <= 0 {
		limit = defaultPublicConfig().Chat.Session.MaxHistoryMessages
	}
	if limit <= 0 {
		limit = 20
	}
	return limit
}

func (s *Session) sessionMessageCap() int {
	return maxInt(s.sessionAnchorLimit()*4, 64)
}

// applyHistoryWindowLocked keeps the in-memory history aligned with the sliding anchor window.
// Must be called with historyMu held.
func (s *Session) applyHistoryWindowLocked() {
	anchorLimit := s.sessionAnchorLimit()
	if anchorLimit > 0 {
		anchorSeen := 0
		anchorIdx := -1
		for i := len(s.chatHistory) - 1; i >= 0; i-- {
			if !isAnchorMessage(s.chatHistory[i]) {
				continue
			}
			anchorSeen++
			if anchorSeen == anchorLimit {
				anchorIdx = i
				break
			}
		}
		if anchorIdx > 0 {
			s.chatHistory = append([]ChatMessage(nil), s.chatHistory[anchorIdx:]...)
		}
	}
	if capLimit := s.sessionMessageCap(); len(s.chatHistory) > capLimit {
		s.chatHistory = append([]ChatMessage(nil), s.chatHistory[len(s.chatHistory)-capLimit:]...)
	}
}

func (s *Session) appendAssistantDraft(turnID uint64, delta string) {
	if turnID == 0 || strings.TrimSpace(delta) == "" {
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	if s.assistantDrafts == nil {
		s.assistantDrafts = map[uint64]string{}
	}
	if s.assistantFinalized == nil {
		s.assistantFinalized = map[uint64]struct{}{}
	}
	if _, finalized := s.assistantFinalized[turnID]; finalized {
		return
	}
	s.assistantDrafts[turnID] += delta
}

func (s *Session) finalizeAssistantMessage(turnID uint64, finalText string, status string, interrupt string, interruptAtMS int64) {
	if turnID == 0 {
		return
	}
	s.draftMu.Lock()
	if s.assistantDrafts == nil {
		s.assistantDrafts = map[uint64]string{}
	}
	if s.assistantFinalized == nil {
		s.assistantFinalized = map[uint64]struct{}{}
	}
	if _, finalized := s.assistantFinalized[turnID]; finalized {
		s.draftMu.Unlock()
		return
	}
	draft := strings.TrimSpace(s.assistantDrafts[turnID])
	delete(s.assistantDrafts, turnID)
	s.assistantFinalized[turnID] = struct{}{}
	s.draftMu.Unlock()

	rawText := firstNonEmpty(draft, strings.TrimSpace(finalText))
	if rawText == "" {
		return
	}
	env := parseAssistantEnvelope(rawText)
	say := firstNonEmpty(strings.TrimSpace(env.Say), strings.TrimSpace(finalText))
	aside := strings.TrimSpace(env.Aside)
	content := strings.TrimSpace(composeMessageContent(say, aside))
	if content == "" {
		content = strings.TrimSpace(finalText)
	}
	if content == "" {
		content = strings.TrimSpace(env.Say)
	}
	if content == "" {
		content = formatMalformedPreview(rawText)
	}
	normalizedStatus := normalizeCompletionStatus(status)
	if normalizedStatus == "" {
		normalizedStatus = CompletionStatusComplete
	}
	normalizedInterrupt := normalizeInterrupt(interrupt)
	if normalizedInterrupt == "" {
		normalizedInterrupt = InterruptNone
	}
	payload := map[string]any{
		"say":               say,
		"aside":             aside,
		"action":            jsonOrEmptyMap(env.ActionJSON),
		"raw_data":          jsonOrEmptyMap(env.RawData),
		"parse_error":       env.ParseError,
		"completion_status": normalizedStatus,
		"interrupt":         normalizedInterrupt,
	}
	if content != "" {
		payload["text"] = content
	}
	if draft != "" && draft != rawText {
		payload["partial_text"] = draft
	}
	entry := ChatMessage{
		TurnID:               turnID,
		Role:                 RoleAssistant,
		Category:             CategoryChat,
		MessageType:          TypeAssistantMessage,
		Say:                  say,
		Aside:                aside,
		ActionJSON:           env.ActionJSON,
		RawData:              env.RawData,
		ParseError:           env.ParseError,
		Content:              content,
		CreatedAtMS:          nowMS(),
		CompletionStatus:     normalizedStatus,
		Interrupt:            normalizedInterrupt,
		InterruptAtMS:        interruptAtMS,
		PartialText:          draft,
		PayloadSchemaVersion: PayloadSchemaVersion1,
	}
	if raw, err := json.Marshal(payload); err == nil {
		entry.PayloadJSON = string(raw)
	}
	msgID := s.appendHistoryMessage(entry)
	if msgID != "" {
		if actionID := actionIDFromJSON(env.ActionJSON); actionID != "" {
			s.bindActionCallRef(actionID, msgID)
		}
	}
	s.clearTurnInterrupt(turnID)
}

func (s *Session) appendHistoryMessage(msg ChatMessage) string {
	payload := map[string]any{}
	if strings.TrimSpace(msg.PayloadJSON) != "" {
		_ = json.Unmarshal([]byte(msg.PayloadJSON), &payload)
	}
	entry, err := BuildMessage(MessageWrite{
		MessageID:            msg.MessageID,
		TurnID:               msg.TurnID,
		Role:                 msg.Role,
		Say:                  msg.Say,
		Aside:                msg.Aside,
		ActionJSON:           msg.ActionJSON,
		RefMessageID:         msg.RefMessageID,
		RefActionSlot:        msg.RefActionSlot,
		RawData:              msg.RawData,
		ParseError:           msg.ParseError,
		Category:             msg.Category,
		MessageType:          msg.MessageType,
		Content:              msg.Content,
		PayloadSchemaVersion: msg.PayloadSchemaVersion,
		Payload:              payload,
		PayloadJSON:          msg.PayloadJSON,
		CreatedAtMS:          msg.CreatedAtMS,
		CompletionStatus:     msg.CompletionStatus,
		Interrupt:            msg.Interrupt,
		InterruptAtMS:        msg.InterruptAtMS,
		PartialText:          msg.PartialText,
	})
	if err != nil {
		Errorf("[Turn:%d] build message failed: %v", msg.TurnID, err)
		return ""
	}
	if s.sqliteStore != nil {
		persisted, err := s.sqliteStore.AppendMessage(entry)
		if err != nil {
			Errorf("[Turn:%d] sqlite append message failed: %v", msg.TurnID, err)
		} else {
			entry = persisted
		}
	}
	s.historyMu.Lock()
	s.chatHistory = append(s.chatHistory, entry)
	s.applyHistoryWindowLocked()
	s.historyMu.Unlock()
	return entry.MessageID
}

func (s *Session) bootstrapHistoryFromSQLite() {
	if s.sqliteStore == nil {
		return
	}
	history, err := s.sqliteStore.LoadSessionWindow(s.sessionAnchorLimit(), s.sessionMessageCap())
	if err != nil {
		Warnf("load sqlite history failed: %v", err)
		return
	}
	if len(history) == 0 {
		return
	}
	s.historyMu.Lock()
	s.chatHistory = append([]ChatMessage(nil), history...)
	s.applyHistoryWindowLocked()
	s.historyMu.Unlock()
}

func (s *Session) handleStateChange(ctrl ControlMessage) {
	surfaceID := strings.TrimSpace(ctrl.SurfaceID)
	if surfaceID == "" {
		return
	}
	state := SurfaceState{
		SurfaceID:      surfaceID,
		SurfaceType:    firstNonEmpty(strings.TrimSpace(ctrl.SurfaceType), "app"),
		SurfaceVersion: firstNonEmpty(strings.TrimSpace(ctrl.SurfaceVersion), "1"),
		EventType:      strings.TrimSpace(ctrl.EventType),
		BusinessState:  cloneAnyMap(ctrl.BusinessState),
		VisibleText:    strings.TrimSpace(ctrl.VisibleText),
		Status:         strings.TrimSpace(ctrl.Status),
		StateVersion:   ctrl.StateVersion,
		UpdatedAtMS:    ctrl.UpdatedAtMS,
	}
	if state.UpdatedAtMS <= 0 {
		state.UpdatedAtMS = nowMS()
	}
	statePayload := map[string]any{
		"surface_id":      state.SurfaceID,
		"surface_type":    state.SurfaceType,
		"surface_version": state.SurfaceVersion,
		"state":           cloneAnyMap(state.BusinessState),
		"business_state":  cloneAnyMap(state.BusinessState),
		"visible_text":    state.VisibleText,
		"status":          state.Status,
		"state_version":   state.StateVersion,
		"updated_at_ms":   state.UpdatedAtMS,
		"event_type":      firstNonEmpty(state.EventType, "state_change"),
	}
	eventType := firstNonEmpty(state.EventType, "state_change")
	actionPayload := map[string]any{
		"type":                  TypeActionState,
		"surface_id":            state.SurfaceID,
		"surface_type":          state.SurfaceType,
		"surface_version":       state.SurfaceVersion,
		"surface_instance_name": firstNonEmpty(state.SurfaceID, state.VisibleText),
		"event_type":            eventType,
		"delta_or_state":        cloneAnyMap(state.BusinessState),
		"state_version":         state.StateVersion,
		"status":                state.Status,
		"visible_text":          state.VisibleText,
		"updated_at_ms":         state.UpdatedAtMS,
	}
	entry := ChatMessage{
		TurnID:      ctrl.TurnID,
		Role:        RoleObserver,
		Category:    CategorySurface,
		MessageType: TypeActionState,
		Say:         "",
		ActionJSON:  mustJSON(actionPayload),
		PayloadJSON: mustJSON(statePayload),
		CreatedAtMS: state.UpdatedAtMS,
	}
	if eventType == "surface_open" {
		entry.MessageType = TypeSurfaceOpen
	}
	s.appendHistoryMessage(entry)
	s.enqueueFollowupMessage(ChatMessage{Role: RoleObserver, ActionJSON: entry.ActionJSON}, true)
	_ = s.sendEvent(EventMessage{
		Type:           "state_change",
		TsMS:           nowMS(),
		TurnID:         ctrl.TurnID,
		SurfaceID:      state.SurfaceID,
		SurfaceType:    state.SurfaceType,
		SurfaceVersion: state.SurfaceVersion,
		StateVersion:   state.StateVersion,
		BusinessState:  cloneAnyMap(state.BusinessState),
		Detail:         firstNonEmpty(state.EventType, "state_change"),
	})
	if s.opsLogger != nil {
		if err := s.opsLogger.Append(
			s.sqliteStore.projectID,
			s.sqliteStore.threadID,
			state.SurfaceID,
			"surface.state_change",
			map[string]any{
				"event_type":      firstNonEmpty(state.EventType, "state_change"),
				"surface_type":    state.SurfaceType,
				"surface_version": state.SurfaceVersion,
				"state_version":   state.StateVersion,
				"status":          state.Status,
				"visible_text":    state.VisibleText,
				"business_state":  cloneAnyMap(state.BusinessState),
			},
		); err != nil {
			Warnf("append operation log failed: %v", err)
		}
	}
}

func (s *Session) handleActionResult(ctrl ControlMessage) {
	turnID := s.turnID.Load()
	if ctrl.TurnID > 0 {
		turnID = ctrl.TurnID
	}
	actionName := strings.TrimSpace(ctrl.ActionName)
	if actionName == "" {
		s.emitError(turnID, "bad_action_result", "missing action_name in action_result", true)
		return
	}
	followup := normalizeFollowup(ctrl.ActionFollowup)
	actionID := firstNonEmpty(strings.TrimSpace(ctrl.ActionID), "act-"+newRequestID())
	status := firstNonEmpty(strings.TrimSpace(ctrl.ActionStatus), "unknown")
	manualConfirm := normalizeManualConfirm(ctrl.ActionManualConfirm)
	blockReason := normalizeBlockReason(ctrl.ActionBlockReason)
	if blockReason == "" {
		blockReason = s.evaluateActionGuard(actionName, ctrl.ActionArgs)
	}
	if blockReason != "" && manualConfirm == "" {
		manualConfirm = "waiting"
	}
	if manualConfirm == "cancel" {
		status = "cancelled"
	} else if manualConfirm == "waiting" && status == "ok" {
		status = "blocked"
	}

	surfaceID := firstNonEmpty(strings.TrimSpace(ctrl.ActionSurfaceID), inferSurfaceIDFromAction(actionName))
	resultSummary := summarizeActionResultForReport(strings.TrimSpace(ctrl.Text), status, ctrl.ActionResult)
	effectSummary := summarizeAnyMap(ctrl.ActionEffect)
	businessState := cloneAnyMap(ctrl.ActionState)
	if len(businessState) == 0 {
		businessState = extractBusinessState(ctrl.ActionEffect, ctrl.ActionResult)
	}
	now := nowMS()
	storeUserID := "default"
	storeProjectID := "project-default"
	storeThreadID := "chat-default"
	if s.sqliteStore != nil {
		storeUserID = s.sqliteStore.userID
		storeProjectID = s.sqliteStore.projectID
		storeThreadID = s.sqliteStore.threadID
	}
	report := ActionReport{
		ReportID:       "rep-" + newRequestID(),
		Origin:         "action_callback",
		UserID:         storeUserID,
		ProjectID:      storeProjectID,
		ThreadID:       storeThreadID,
		TurnID:         turnID,
		SurfaceID:      surfaceID,
		SurfaceType:    firstNonEmpty(strings.TrimSpace(ctrl.SurfaceType), "app"),
		SurfaceVersion: firstNonEmpty(strings.TrimSpace(ctrl.SurfaceVersion), "1"),
		ActionID:       actionID,
		ActionName:     actionName,
		Followup:       followup,
		Status:         status,
		ResultSummary:  resultSummary,
		EffectSummary:  effectSummary,
		BusinessState:  businessState,
		ManualConfirm:  manualConfirm,
		BlockReason:    blockReason,
		CreatedAtMS:    now,
		CreatedAtISO:   time.UnixMilli(now).Format(time.RFC3339),
		MessageType:    "action_report",
		Visibility:     "hidden",
	}
	callMessageID := s.resolveActionCallRef(actionID, actionName)
	if callMessageID == "" {
		callPayload := map[string]any{
			"type":            TypeActionCall,
			"id":              actionID,
			"path":            actionName,
			"name":            actionName,
			"surface_id":      surfaceID,
			"surface_type":    report.SurfaceType,
			"surface_version": report.SurfaceVersion,
			"followup":        followup,
			"args":            cloneAnyMap(ctrl.ActionArgs),
			"status":          status,
			"manual_confirm":  manualConfirm,
			"block_reason":    blockReason,
			"trigger_reason":  firstNonEmpty(strings.TrimSpace(ctrl.Reason), "dispatch"),
		}
		callMessageID = s.appendHistoryMessage(ChatMessage{
			TurnID:      turnID,
			Role:        RoleObserver,
			Category:    CategoryAIAction,
			MessageType: TypeActionCall,
			Say:         "",
			ActionJSON:  mustJSON(callPayload),
			PayloadJSON: mustJSON(callPayload),
			CreatedAtMS: now,
		})
		if callMessageID != "" {
			s.bindActionCallRef(actionID, callMessageID)
		}
	}
	executePayload := map[string]any{
		"type":            TypeActionExecute,
		"ref_message_id":  callMessageID,
		"ref_action_slot": 0,
		"action_id":       actionID,
		"path":            actionName,
		"name":            actionName,
		"status":          "running",
		"dispatch_info": map[string]any{
			"surface_id":      surfaceID,
			"surface_type":    report.SurfaceType,
			"surface_version": report.SurfaceVersion,
			"trigger_reason":  firstNonEmpty(strings.TrimSpace(ctrl.Reason), "dispatch"),
		},
	}
	s.appendHistoryMessage(ChatMessage{
		TurnID:        turnID,
		Role:          RoleObserver,
		Category:      CategoryAIAction,
		MessageType:   TypeActionExecute,
		Say:           "",
		ActionJSON:    mustJSON(executePayload),
		RefMessageID:  callMessageID,
		RefActionSlot: 0,
		PayloadJSON:   mustJSON(executePayload),
		CreatedAtMS:   now,
	})

	reportText := formatActionReportText(report)
	reportPayload := map[string]any{
		"type":            TypeActionReport,
		"ref_message_id":  callMessageID,
		"ref_action_slot": 0,
		"report_id":       report.ReportID,
		"origin":          report.Origin,
		"action_id":       actionID,
		"path":            actionName,
		"name":            actionName,
		"surface_id":      surfaceID,
		"surface_type":    report.SurfaceType,
		"surface_version": report.SurfaceVersion,
		"followup":        followup,
		"state":           reportStateFromStatus(status),
		"status":          status,
		"desc":            resultSummary,
		"result_summary":  resultSummary,
		"effect_summary":  effectSummary,
		"result":          cloneAnyMap(ctrl.ActionResult),
		"effect":          cloneAnyMap(ctrl.ActionEffect),
		"business_state":  cloneAnyMap(businessState),
		"manual_confirm":  manualConfirm,
		"block_reason":    blockReason,
	}
	reportMsg := ChatMessage{
		TurnID:        turnID,
		Role:          RoleObserver,
		Category:      CategoryAIAction,
		MessageType:   TypeActionReport,
		Say:           "",
		ActionJSON:    mustJSON(reportPayload),
		RefMessageID:  callMessageID,
		RefActionSlot: 0,
		Content:       reportText,
		PayloadJSON:   mustJSON(reportPayload),
		CreatedAtMS:   now,
	}
	s.appendHistoryMessage(reportMsg)
	if callMessageID != "" {
		s.bindActionCallRef(actionID, callMessageID)
	}

	_ = s.sendEvent(EventMessage{
		Type:           "action_report",
		TsMS:           now,
		TurnID:         turnID,
		Origin:         "action_callback",
		MessageType:    "action_report",
		Text:           reportText,
		ActionID:       actionID,
		ActionName:     actionName,
		ActionStatus:   status,
		Followup:       followup,
		ManualConfirm:  manualConfirm,
		BlockReason:    blockReason,
		SurfaceID:      surfaceID,
		SurfaceType:    report.SurfaceType,
		SurfaceVersion: report.SurfaceVersion,
		BusinessState:  cloneAnyMap(businessState),
		Payload:        reportPayload,
	})
	if s.opsLogger != nil {
		if err := s.opsLogger.Append(
			s.sqliteStore.projectID,
			s.sqliteStore.threadID,
			surfaceID,
			"action.report",
			map[string]any{
				"action_id":       actionID,
				"action_name":     actionName,
				"followup":        followup,
				"status":          status,
				"result_summary":  resultSummary,
				"effect_summary":  effectSummary,
				"surface_id":      surfaceID,
				"surface_type":    report.SurfaceType,
				"surface_version": report.SurfaceVersion,
				"manual_confirm":  manualConfirm,
				"block_reason":    blockReason,
				"result":          cloneAnyMap(ctrl.ActionResult),
				"effect":          cloneAnyMap(ctrl.ActionEffect),
				"state":           cloneAnyMap(businessState),
			},
		); err != nil {
			Warnf("append operation log failed: %v", err)
		}
	}

	Infof("[Turn:%d] action report generated: %s status=%s followup=%s", turnID, actionName, status, followup)
	if followup == "report" && manualConfirm != "waiting" && manualConfirm != "cancel" {
		s.enqueueFollowupMessage(reportMsg, concurrentActionHint(ctrl.ActionResult) > 1)
	}
}

func formatActionReportText(report ActionReport) string {
	result := strings.TrimSpace(report.ResultSummary)
	if result == "" {
		result = "{}"
	}
	effect := strings.TrimSpace(report.EffectSummary)
	if effect == "" {
		effect = "{}"
	}
	tail := ""
	if report.ManualConfirm != "" {
		tail += " manual_confirm=" + report.ManualConfirm
	}
	if report.BlockReason != "" {
		tail += " block_reason=" + report.BlockReason
	}
	return fmt.Sprintf("[action_report] name=%s status=%s followup=%s result=%s effect=%s%s",
		report.ActionName, report.Status, normalizeFollowup(report.Followup), result, effect, tail)
}

func summarizeActionResultForReport(contentText string, status string, result map[string]any) string {
	cleanText := strings.TrimSpace(contentText)
	if strings.EqualFold(strings.TrimSpace(status), "ok") {
		return firstNonEmpty(cleanText, summarizeAnyMap(result))
	}
	reason := firstNonEmpty(
		asTrimmedString(result["reason"]),
		asTrimmedString(result["error"]),
		asTrimmedString(result["message"]),
	)
	if reason != "" {
		return "失败原因：" + reason
	}
	return firstNonEmpty(cleanText, summarizeAnyMap(result))
}

func inferSurfaceIDFromAction(actionName string) string {
	name := strings.TrimSpace(actionName)
	lower := strings.ToLower(name)
	if lower == "get_surfaces" {
		return "surface_registry"
	}
	if strings.HasPrefix(lower, "surface.call.") {
		parts := strings.Split(name, ".")
		if len(parts) >= 4 {
			surfaceID := strings.TrimSpace(parts[2])
			if surfaceID != "" {
				return surfaceID
			}
		}
	}
	return ""
}

func summarizeAnyMap(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func extractBusinessState(candidates ...map[string]any) map[string]any {
	for _, c := range candidates {
		if len(c) == 0 {
			continue
		}
		if raw, ok := c["business_state"]; ok {
			if m, ok := raw.(map[string]any); ok && len(m) > 0 {
				return cloneAnyMap(m)
			}
		}
		if raw, ok := c["state"]; ok {
			if m, ok := raw.(map[string]any); ok && len(m) > 0 {
				return cloneAnyMap(m)
			}
		}
	}
	return nil
}

func normalizeManualConfirm(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "confirm":
		return "confirm"
	case "cancel":
		return "cancel"
	case "waiting":
		return "waiting"
	default:
		return ""
	}
}

func normalizeBlockReason(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "rate_limit":
		return "rate_limit"
	case "quota_limit":
		return "quota_limit"
	default:
		return ""
	}
}

func (s *Session) evaluateActionGuard(actionName string, args map[string]any) string {
	now := time.Now().UnixMilli()
	const (
		windowMS = int64(60_000)
		rateMax  = 10
		dedupeMS = int64(3_000)
	)
	keyHash := sha1.Sum([]byte(actionName + "|" + summarizeAnyMap(args)))
	key := fmt.Sprintf("%x", keyHash[:])

	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	if s.actionDedup == nil {
		s.actionDedup = map[string]int64{}
	}

	filtered := make([]int64, 0, len(s.actionRateWindow)+1)
	for _, ts := range s.actionRateWindow {
		if now-ts <= windowMS {
			filtered = append(filtered, ts)
		}
	}
	if len(filtered) >= rateMax {
		s.actionRateWindow = filtered
		return "rate_limit"
	}
	if last, ok := s.actionDedup[key]; ok && now-last <= dedupeMS {
		s.actionRateWindow = append(filtered, now)
		s.actionDedup[key] = now
		return "quota_limit"
	}
	s.actionRateWindow = append(filtered, now)
	s.actionDedup[key] = now
	return ""
}

func (s *Session) setUserTurnActive(active bool) {
	s.actionMu.Lock()
	s.userTurnActive = active
	s.actionMu.Unlock()
}

func (s *Session) clearPendingFollowupsForUserTurn() {
	s.actionMu.Lock()
	s.pendingFollowups = nil
	if s.followupFlushTimer != nil {
		s.followupFlushTimer.Stop()
		s.followupFlushTimer = nil
	}
	s.actionMu.Unlock()
}

func (s *Session) enqueueFollowupMessage(message ChatMessage, aggregate bool) {
	s.actionMu.Lock()
	s.pendingFollowups = append(s.pendingFollowups, message)
	if aggregate {
		if s.followupFlushTimer != nil {
			s.followupFlushTimer.Stop()
		}
		s.followupFlushTimer = time.AfterFunc(1*time.Second, func() {
			s.actionMu.Lock()
			s.followupFlushTimer = nil
			s.actionMu.Unlock()
			s.tryStartContinuation()
		})
		s.actionMu.Unlock()
		return
	}
	hasTimer := s.followupFlushTimer != nil
	s.actionMu.Unlock()
	if !hasTimer {
		s.tryStartContinuation()
	}
}

func (s *Session) tryStartContinuation() {
	if s.rootCtx == nil || s.rootCtx.Err() != nil {
		return
	}
	s.actionMu.Lock()
	if s.userTurnActive || s.continuationRunning || len(s.pendingFollowups) == 0 || !s.started.Load() || s.followupFlushTimer != nil {
		s.actionMu.Unlock()
		return
	}
	s.pendingFollowups = nil
	s.continuationRunning = true
	s.continuationSeq++
	continuationSeq := s.continuationSeq
	s.actionMu.Unlock()

	turnID := s.turnID.Add(1)
	s.startContinuationTurn(turnID, continuationSeq)
}

func concurrentActionHint(result map[string]any) int {
	if len(result) == 0 {
		return 0
	}
	raw, ok := result["concurrent_actions"]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		if v < 0 {
			return 0
		}
		return int(v)
	default:
		return 0
	}
}

func (s *Session) startContinuationTurn(turnID uint64, continuationSeq uint64) {
	history := s.getHistory()
	ctx, cancel := context.WithCancel(s.rootCtx)

	s.turnMu.Lock()
	s.turnCancel = cancel
	s.lastStartedTurnID = turnID
	s.turnMu.Unlock()
	s.setTurnState(turnID, StateThinking, fmt.Sprintf("continuation #%d", continuationSeq))

	go func() {
		defer func() {
			s.turnMu.Lock()
			if s.lastStartedTurnID == turnID {
				s.turnCancel = nil
			}
			s.turnMu.Unlock()
			s.actionMu.Lock()
			s.continuationRunning = false
			s.actionMu.Unlock()
			s.tryStartContinuation()
		}()
		err := s.pipeline.RunTurn(ctx, turnID, "", history)
		if errors.Is(err, context.Canceled) {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusInterrupted, s.consumeTurnInterrupt(turnID), 0)
			return
		}
		if err != nil {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusError, InterruptOther, 0)
			Errorf("[Turn:%d] continuation failed: %v", turnID, err)
			s.emitError(turnID, "continuation_failed", err.Error(), true)
			s.setTurnState(turnID, StateError, "continuation failed")
			return
		}
		if ctx.Err() == nil {
			s.setTurnState(turnID, StateListening, "continuation finished")
		}
	}()
}

func (s *Session) handleConfigChange(ctrl ControlMessage) {
	payload := map[string]any{
		"source":        firstNonEmpty(strings.TrimSpace(ctrl.ConfigSource), "config_drawer"),
		"changed_paths": append([]string(nil), ctrl.ConfigChangedPaths...),
		"config":        cloneAnyMap(ctrl.ConfigSnapshot),
	}
	s.appendHistoryMessage(ChatMessage{
		TurnID:      ctrl.TurnID,
		Role:        RoleSystem,
		Category:    CategoryConfig,
		MessageType: TypeConfigChange,
		PayloadJSON: mustJSON(payload),
		CreatedAtMS: nowMS(),
	})
}

func (s *Session) recordTurnInterrupt(turnID uint64, reason string) {
	if turnID == 0 {
		return
	}
	reason = firstNonEmpty(normalizeInterrupt(reason), InterruptOther)
	s.interruptMu.Lock()
	if s.turnInterrupt == nil {
		s.turnInterrupt = map[uint64]string{}
	}
	s.turnInterrupt[turnID] = reason
	s.interruptMu.Unlock()
}

func (s *Session) consumeTurnInterrupt(turnID uint64) string {
	if turnID == 0 {
		return InterruptOther
	}
	s.interruptMu.Lock()
	defer s.interruptMu.Unlock()
	reason := firstNonEmpty(s.turnInterrupt[turnID], InterruptOther)
	delete(s.turnInterrupt, turnID)
	return reason
}

func (s *Session) clearTurnInterrupt(turnID uint64) {
	if turnID == 0 {
		return
	}
	s.interruptMu.Lock()
	delete(s.turnInterrupt, turnID)
	s.interruptMu.Unlock()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(b)
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func jsonOrEmptyMap(raw string) map[string]any {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(clean), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func actionIDFromJSON(actionJSON string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(actionJSON)), &payload); err != nil {
		return ""
	}
	return firstNonEmpty(asTrimmedString(payload["id"]), asTrimmedString(payload["action_id"]))
}

func reportStateFromStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "complete", "completed":
		return "success"
	case "pending", "blocked":
		return "pending"
	default:
		return "fail"
	}
}

func (s *Session) bindActionCallRef(actionID string, messageID string) {
	cleanActionID := strings.TrimSpace(actionID)
	cleanMessageID := strings.TrimSpace(messageID)
	if cleanActionID == "" || cleanMessageID == "" {
		return
	}
	s.actionRefMu.Lock()
	if s.actionCallRefIDs == nil {
		s.actionCallRefIDs = map[string]string{}
	}
	s.actionCallRefIDs[cleanActionID] = cleanMessageID
	s.actionRefMu.Unlock()
}

func (s *Session) resolveActionCallRef(actionID string, actionName string) string {
	cleanActionID := strings.TrimSpace(actionID)
	if cleanActionID != "" {
		s.actionRefMu.Lock()
		if messageID := strings.TrimSpace(s.actionCallRefIDs[cleanActionID]); messageID != "" {
			s.actionRefMu.Unlock()
			return messageID
		}
		s.actionRefMu.Unlock()
	}
	targetName := strings.TrimSpace(actionName)
	if targetName == "" {
		return ""
	}
	history := s.getHistory()
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != RoleAssistant {
			continue
		}
		action := jsonOrEmptyMap(msg.ActionJSON)
		if strings.ToLower(asTrimmedString(action["type"])) != TypeActionCall {
			continue
		}
		path := firstNonEmpty(asTrimmedString(action["path"]), asTrimmedString(action["name"]))
		if path != targetName {
			continue
		}
		if msg.MessageID != "" {
			return msg.MessageID
		}
	}
	return ""
}

// getHistory returns a snapshot of the current conversation history.
func (s *Session) getHistory() []ChatMessage {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	out := make([]ChatMessage, len(s.chatHistory))
	copy(out, s.chatHistory)
	return out
}

func (s *Session) publicConfig() PublicConfig {
	if s.runtimeConfig != nil {
		return s.runtimeConfig.Snapshot()
	}
	return defaultPublicConfig()
}

func (s *Session) triggerLLMWaitFinal() time.Duration {
	ms := s.publicConfig().Chat.Session.TriggerLLMWaitFinalMs
	if ms <= 0 {
		ms = defaultPublicConfig().Chat.Session.TriggerLLMWaitFinalMs
	}
	return time.Duration(ms) * time.Millisecond
}
