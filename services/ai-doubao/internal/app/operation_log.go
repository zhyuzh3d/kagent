package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type OperationLogger struct {
	mu     sync.Mutex
	userID string
	date   string
	f      *os.File
}

func NewOperationLogger(userID string) *OperationLogger {
	return &OperationLogger{
		userID: firstNonEmpty(strings.TrimSpace(userID), "default"),
	}
}

func (l *OperationLogger) Append(projectID string, threadID string, surfaceID string, kind string, payload map[string]any) error {
	cleanKind := firstNonEmpty(strings.TrimSpace(kind), "operation")
	now := time.Now()
	tsMS := now.UnixMilli()
	date := now.Format("20060102")

	record := map[string]any{
		"ts_ms":      tsMS,
		"user_id":    l.userID,
		"project_id": strings.TrimSpace(projectID),
		"thread_id":  strings.TrimSpace(threadID),
		"surface_id": strings.TrimSpace(surfaceID),
		"kind":       cleanKind,
		"payload":    nonNilMap(payload),
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal operation record: %w", err)
	}
	raw = append(raw, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f == nil || l.date != date {
		if l.f != nil {
			_ = l.f.Close()
			l.f = nil
		}
		dir := filepath.Join("data", "users", l.userID, "ops")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir ops dir: %w", err)
		}
		path := filepath.Join(dir, date+".jsonl")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open operation log: %w", err)
		}
		l.f = f
		l.date = date
	}
	if _, err := l.f.Write(raw); err != nil {
		return fmt.Errorf("append operation log: %w", err)
	}
	return nil
}

func (l *OperationLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		err := l.f.Close()
		l.f = nil
		return err
	}
	return nil
}
