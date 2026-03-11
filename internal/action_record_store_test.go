package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActionRecordStoreAdd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.jsonl")
	store, err := NewActionRecordStore(path)
	if err != nil {
		t.Fatalf("NewActionRecordStore failed: %v", err)
	}
	err = store.Add(ActionRecord{
		TurnID:     8,
		Category:   "dispatch",
		ActionName: "surface.call.counter.set_count",
		Status:     "ok",
		Content:    "已把数字改成 8",
		Args:       map[string]any{"count": 8},
		Result:     map[string]any{"queued": false},
	})
	if err != nil {
		t.Fatalf("store.Add failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "\"action_name\":\"surface.call.counter.set_count\"") {
		t.Fatalf("unexpected record content: %s", text)
	}
	if !strings.Contains(text, "\"status\":\"ok\"") {
		t.Fatalf("unexpected record status: %s", text)
	}
}
