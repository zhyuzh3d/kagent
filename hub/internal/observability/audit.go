package observability

import (
	"sync"
	"time"
)

type Event struct {
	Module    string         `json:"module"`
	Action    string         `json:"action"`
	Status    string         `json:"status"`
	Detail    map[string]any `json:"detail,omitempty"`
	Timestamp int64          `json:"timestamp_ms"`
}

type Store struct {
	mu      sync.RWMutex
	maxSize int
	events  []Event
}

func NewStore(maxSize int) *Store {
	if maxSize <= 0 {
		maxSize = 2000
	}
	return &Store{
		maxSize: maxSize,
		events:  make([]Event, 0, maxSize),
	}
}

func (s *Store) Add(module string, action string, status string, detail map[string]any) {
	if s == nil {
		return
	}
	event := Event{
		Module:    module,
		Action:    action,
		Status:    status,
		Detail:    detail,
		Timestamp: time.Now().UnixMilli(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	if len(s.events) > s.maxSize {
		s.events = s.events[len(s.events)-s.maxSize:]
	}
}

func (s *Store) List(limit int) []Event {
	if s == nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return nil
	}
	if limit > len(s.events) {
		limit = len(s.events)
	}
	out := make([]Event, 0, limit)
	start := len(s.events) - limit
	for i := len(s.events) - 1; i >= start; i-- {
		out = append(out, s.events[i])
	}
	return out
}
