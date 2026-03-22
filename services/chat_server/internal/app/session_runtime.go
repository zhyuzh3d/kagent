package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"kagent/pkg/hubsvc"

	"github.com/gorilla/websocket"
)

type Session struct {
	conn          *websocket.Conn
	cfg           *ModelConfig
	runtimeConfig *RuntimeConfigManager
	chatStore     ChatStore
	asr           ASRClient
	llm           LLMClient
	tts           TTSClient
	pipeline      *TurnPipeline

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

	lastASRTextMu    sync.Mutex
	committedTurnID  uint64
	committedASRText string
	lastASRText      string

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

	actionMu               sync.Mutex
	userTurnActive         bool
	continuationRunning    bool
	continuationSeq        uint64
	pendingFollowups       []ChatMessage
	followupReplyRequested bool
	followupFlushTimer     *time.Timer
	actionRateWindow       []int64
	actionDedup            map[string]int64

	actionRefMu      sync.Mutex
	actionCallRefIDs map[string]string
}

func NewSession(conn *websocket.Conn, cfg *ModelConfig, runtimeConfig *RuntimeConfigManager, chatStore ChatStore, providerFactory ProviderFactory) *Session {
	publicCfg := defaultPublicConfig()
	if runtimeConfig != nil {
		publicCfg = runtimeConfig.Snapshot()
	}
	if providerFactory == nil {
		hubBaseURL := ""
		if cfg != nil {
			hubBaseURL = cfg.EffectiveAIService().BaseURL
		}
		providerFactory = NewHubProviderFactory(hubBaseURL, hubsvc.BootstrapSecret{})
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
		chatStore:          chatStore,
		asr:                providerFactory.NewASRClient(cfg, runtimeConfig),
		llm:                providerFactory.NewLLMClient(cfg, runtimeConfig),
		tts:                providerFactory.NewTTSClient(cfg, runtimeConfig),
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
	s.bootstrapHistoryFromStore()
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

func (s *Session) cleanup() {
	s.stopAll()
	if s.rootCancel != nil {
		s.rootCancel()
	}
	_ = s.conn.Close()
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
