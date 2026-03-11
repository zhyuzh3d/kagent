package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionHandleActionResult_PersistAndHistory(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "action_records.jsonl")
	store, err := NewActionRecordStore(storePath)
	if err != nil {
		t.Fatalf("NewActionRecordStore failed: %v", err)
	}
	s := &Session{
		actionRecords: store,
	}
	s.handleActionResult(ControlMessage{
		Type:         "action_result",
		TurnID:       11,
		Reason:       "dispatch",
		Text:         "已把数字改成 11",
		ActionID:     "act-11",
		ActionName:   "surface.call.counter.set_count",
		ActionStatus: "ok",
		ActionArgs:   map[string]any{"count": 11},
		ActionResult: map[string]any{"queued": false},
	})

	raw, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "\"action_name\":\"surface.call.counter.set_count\"") {
		t.Fatalf("record file missing action_name: %s", text)
	}
	history := s.getHistory()
	if len(history) == 0 {
		t.Fatalf("history should contain action observation")
	}
	last := history[len(history)-1]
	if last.Role != "observer" || !strings.Contains(last.Content, "[action_report]") {
		t.Fatalf("unexpected last history: %#v", last)
	}
}
