package app

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStoreBasicFlow(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "chat.db"), "default", "chat-default")
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	msgID, err := store.AppendMessage(ChatMessage{Role: "user", Content: "你好", CreatedAtMS: 1710000001000}, 1, "user", "chat", "visible", map[string]any{"origin": "test"})
	if err != nil {
		t.Fatalf("AppendMessage failed: %v", err)
	}
	if msgID == "" {
		t.Fatalf("message id should not be empty")
	}

	err = store.AppendActionCall(ActionCall{
		ActionID:    "act-1",
		ActionName:  "surface.call.counter.set_count",
		SurfaceID:   "counter",
		TurnID:      1,
		Followup:    "report",
		Args:        map[string]any{"count": 9},
		RequestedAt: 1710000002000,
	}, "ok", "", "")
	if err != nil {
		t.Fatalf("AppendActionCall failed: %v", err)
	}

	err = store.AppendActionReport(ActionReport{
		ReportID:      "rep-1",
		ActionID:      "act-1",
		ActionName:    "surface.call.counter.set_count",
		SurfaceID:     "counter",
		TurnID:        1,
		Followup:      "report",
		Status:        "ok",
		ResultSummary: `{"queued":false}`,
		EffectSummary: `{"count":9}`,
		BusinessState: map[string]any{"count": 9},
		CreatedAtMS:   1710000003000,
	}, "msg-1")
	if err != nil {
		t.Fatalf("AppendActionReport failed: %v", err)
	}

	err = store.UpsertSurfaceState(SurfaceState{
		SurfaceID:     "counter",
		StateVersion:  3,
		BusinessState: map[string]any{"count": 9},
		VisibleText:   "9",
		Status:        "ready",
		UpdatedAtMS:   1710000004000,
	})
	if err != nil {
		t.Fatalf("UpsertSurfaceState failed: %v", err)
	}
	state, ok, err := store.LoadSurfaceState("counter")
	if err != nil {
		t.Fatalf("LoadSurfaceState failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected cached state")
	}
	if state.SurfaceID != "counter" || state.StateVersion != 3 {
		t.Fatalf("unexpected state: %#v", state)
	}

	history, err := store.LoadRecentContext(10)
	if err != nil {
		t.Fatalf("LoadRecentContext failed: %v", err)
	}
	if len(history) == 0 {
		t.Fatalf("expected non-empty history")
	}
	last := history[len(history)-1]
	if last.Role != "user" || last.Content != "你好" {
		t.Fatalf("unexpected history tail: %#v", last)
	}
}
