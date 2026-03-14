package app

import (
	"context"
	"testing"
)

func TestShouldInterruptOnStartListen(t *testing.T) {
	if shouldInterruptOnStartListen(StateThinking) {
		t.Fatalf("did not expect start_listen to interrupt while thinking")
	}
	if shouldInterruptOnStartListen(StateSpeaking) {
		t.Fatalf("did not expect start_listen to interrupt while speaking")
	}
	if shouldInterruptOnStartListen(StateListening) {
		t.Fatalf("did not expect start_listen to interrupt while listening")
	}
}

func TestShouldInterruptForRecognizedSpeech(t *testing.T) {
	if !shouldInterruptForRecognizedSpeech(StateThinking, 2, 3, "你好") {
		t.Fatalf("expected confirmed speech to interrupt a thinking turn")
	}
	if !shouldInterruptForRecognizedSpeech(StateSpeaking, 2, 3, "你好") {
		t.Fatalf("expected confirmed speech to interrupt a speaking turn")
	}
	if shouldInterruptForRecognizedSpeech(StateListening, 2, 3, "你好") {
		t.Fatalf("did not expect interruption while already listening")
	}
	if shouldInterruptForRecognizedSpeech(StateThinking, 2, 2, "你好") {
		t.Fatalf("did not expect interruption for the same generated turn")
	}
	if shouldInterruptForRecognizedSpeech(StateThinking, 2, 3, "   ") {
		t.Fatalf("did not expect interruption for empty recognized text")
	}
}

func TestAdoptTurnID(t *testing.T) {
	s := &Session{}
	s.turnID.Store(5)

	if got := s.adoptTurnID(3); got != 5 {
		t.Fatalf("expected current turn 5, got %d", got)
	}
	if got := s.turnID.Load(); got != 5 {
		t.Fatalf("turn id should remain 5, got %d", got)
	}

	if got := s.adoptTurnID(8); got != 8 {
		t.Fatalf("expected adopted turn 8, got %d", got)
	}
	if got := s.turnID.Load(); got != 8 {
		t.Fatalf("turn id should advance to 8, got %d", got)
	}
}

func TestStartTurnRemapsOnTurnIDCollision(t *testing.T) {
	s := &Session{
		rootCtx:            context.Background(),
		ttsQueue:           make(chan TTSChunk, 1),
		actionDedup:        map[string]int64{},
		assistantDrafts:    map[uint64]string{},
		assistantFinalized: map[uint64]struct{}{},
		turnInterrupt:      map[uint64]string{},
	}
	s.pipeline = NewTurnPipeline(&fakeLLM{deltas: []string{"好的。"}}, &fakeTTS{}, nil, TurnCallbacks{
		OnStatus: func(turnID uint64, state string, detail string) {},
		OnEvent:  func(evt EventMessage) {},
		OnChunk:  func(chunk TTSChunk) error { return nil },
	})
	s.turnID.Store(5)
	s.lastStartedTurnID = 5

	s.startTurn("请把数字设置成3215", 5)

	if got := s.turnID.Load(); got != 6 {
		t.Fatalf("expected turn id remapped to 6, got %d", got)
	}
	if s.lastStartedTurnID != 6 {
		t.Fatalf("expected lastStartedTurnID=6, got %d", s.lastStartedTurnID)
	}
	history := s.getHistory()
	if len(history) == 0 {
		t.Fatalf("expected user message appended to history")
	}
	last := history[len(history)-1]
	if last.Role != "user" || last.Content != "请把数字设置成3215" {
		t.Fatalf("unexpected history tail: %#v", last)
	}
}

func TestApplyHistoryWindowKeepsAnchorRangeAndCap(t *testing.T) {
	s := &Session{}
	for i := 0; i < 20; i++ {
		s.chatHistory = append(s.chatHistory, ChatMessage{
			Seq:         int64(i + 1),
			Role:        RoleUser,
			Category:    CategoryChat,
			MessageType: TypeUserMessage,
			Content:     "u",
		})
		s.chatHistory = append(s.chatHistory, ChatMessage{
			Seq:         int64(i + 100),
			Role:        RoleObserver,
			Category:    CategoryAIAction,
			MessageType: TypeActionReport,
			Content:     "r",
		})
	}
	s.historyMu.Lock()
	s.applyHistoryWindowLocked()
	got := append([]ChatMessage(nil), s.chatHistory...)
	s.historyMu.Unlock()

	if len(got) != 40 {
		t.Fatalf("expected 40 messages kept (20 anchors + 20 reports), got %d", len(got))
	}
	if got[0].Seq != 1 || got[len(got)-1].Seq != 119 {
		t.Fatalf("unexpected history window boundaries: first=%#v last=%#v", got[0], got[len(got)-1])
	}
}

func TestFinalizeAssistantMessageInterrupted(t *testing.T) {
	s := &Session{
		assistantDrafts:    map[uint64]string{},
		assistantFinalized: map[uint64]struct{}{},
		turnInterrupt:      map[uint64]string{},
	}
	s.appendAssistantDraft(3, "第一段")
	s.appendAssistantDraft(3, "第二段")
	s.finalizeAssistantMessage(3, "", CompletionStatusInterrupted, InterruptVAD, 0)

	history := s.getHistory()
	if len(history) != 1 {
		t.Fatalf("expected one assistant history message, got %d", len(history))
	}
	msg := history[0]
	if msg.CompletionStatus != CompletionStatusInterrupted || msg.Interrupt != InterruptVAD {
		t.Fatalf("unexpected assistant finalization: %#v", msg)
	}
	if msg.Content != "第一段第二段" {
		t.Fatalf("unexpected assistant content: %#v", msg)
	}
}
