package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ActionRecord struct {
	RecordID     string         `json:"record_id"`
	TurnID       uint64         `json:"turn_id"`
	Category     string         `json:"category"`
	ActionName   string         `json:"action_name"`
	Status       string         `json:"status"`
	Content      string         `json:"content,omitempty"`
	Args         map[string]any `json:"args,omitempty"`
	Result       map[string]any `json:"result,omitempty"`
	Detail       string         `json:"detail,omitempty"`
	CreatedAtMS  int64          `json:"created_at_ms"`
	CreatedAtISO string         `json:"created_at_iso"`
}

type ActionRecordStore struct {
	path string
	mu   sync.Mutex
}

func NewActionRecordStore(path string) (*ActionRecordStore, error) {
	if path == "" {
		return nil, fmt.Errorf("action record path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create action record dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open action record file: %w", err)
	}
	_ = f.Close()
	return &ActionRecordStore{path: path}, nil
}

func (s *ActionRecordStore) Add(record ActionRecord) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if record.RecordID == "" {
		record.RecordID = fmt.Sprintf("ar-%d", time.Now().UnixNano())
	}
	if record.CreatedAtMS <= 0 {
		record.CreatedAtMS = time.Now().UnixMilli()
	}
	if record.CreatedAtISO == "" {
		record.CreatedAtISO = time.UnixMilli(record.CreatedAtMS).Format(time.RFC3339)
	}

	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal action record: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open action record file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write action record file: %w", err)
	}
	return nil
}
