package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"kagent/pkg/hubsvc"
)

type recordedToolCall struct {
	ToolID string
	Query  string
}

func TestNewHubDatabaseStoreWithOptions_NoDefaultWritesForReadOnlyStore(t *testing.T) {
	t.Parallel()

	calls, serverURL := startHubStoreTestServer(t)
	client := NewHubToolClient(serverURL, hubTestBootstrapSecret(), 5*time.Second)

	store, err := NewHubDatabaseStoreWithOptions(context.Background(), client, "user-1", "", "", HubDatabaseStoreOptions{EnsureDefaults: false})
	if err != nil {
		t.Fatalf("NewHubDatabaseStoreWithOptions() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, call := range snapshotRecordedToolCalls(calls) {
		query := strings.ToUpper(strings.TrimSpace(call.Query))
		if strings.Contains(query, "INSERT OR IGNORE INTO USERS") || strings.Contains(query, "INSERT INTO PROJECTS") || strings.Contains(query, "INSERT INTO THREADS") {
			t.Fatalf("unexpected default write during read-only store init: tool=%s query=%q", call.ToolID, call.Query)
		}
	}
}

func TestNewHubDatabaseStoreWithOptions_SeedsDefaultsForSessionStore(t *testing.T) {
	t.Parallel()

	calls, serverURL := startHubStoreTestServer(t)
	client := NewHubToolClient(serverURL, hubTestBootstrapSecret(), 5*time.Second)

	store, err := NewHubDatabaseStoreWithOptions(context.Background(), client, "user-1", "", "", HubDatabaseStoreOptions{EnsureDefaults: true})
	if err != nil {
		t.Fatalf("NewHubDatabaseStoreWithOptions() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var sawUserInsert bool
	var sawProjectInsert bool
	var sawThreadInsert bool
	for _, call := range snapshotRecordedToolCalls(calls) {
		query := strings.ToUpper(strings.TrimSpace(call.Query))
		if strings.Contains(query, "INSERT OR IGNORE INTO USERS") {
			sawUserInsert = true
		}
		if strings.Contains(query, "INSERT INTO PROJECTS") {
			sawProjectInsert = true
		}
		if strings.Contains(query, "INSERT INTO THREADS") {
			sawThreadInsert = true
		}
	}
	if !sawUserInsert || !sawProjectInsert || !sawThreadInsert {
		t.Fatalf("expected session store init to seed defaults, got user=%v project=%v thread=%v", sawUserInsert, sawProjectInsert, sawThreadInsert)
	}
}

type recordedToolCalls struct {
	mu    sync.Mutex
	items []recordedToolCall
}

func startHubStoreTestServer(t *testing.T) (*recordedToolCalls, string) {
	t.Helper()

	calls := &recordedToolCalls{items: make([]recordedToolCall, 0, 16)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var req struct {
			ToolID string         `json:"tool_id"`
			Args   map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		query := ""
		if req.Args != nil {
			if raw, ok := req.Args["query"].(string); ok {
				query = raw
			}
		}

		calls.mu.Lock()
		calls.items = append(calls.items, recordedToolCall{ToolID: req.ToolID, Query: query})
		calls.mu.Unlock()

		resp := map[string]any{"ok": true, "result": map[string]any{}}
		if req.ToolID == "storage.database.query" {
			resp["result"] = map[string]any{"rows": []map[string]any{}, "count": 0}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	return calls, server.URL
}

func snapshotRecordedToolCalls(calls *recordedToolCalls) []recordedToolCall {
	if calls == nil {
		return nil
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	out := make([]recordedToolCall, len(calls.items))
	copy(out, calls.items)
	return out
}

func hubTestBootstrapSecret() hubsvc.BootstrapSecret {
	return hubsvc.BootstrapSecret{
		ServiceID:  "chat_server",
		InstanceID: "chat_server-test",
		S2HToken:   "test-s2h",
		H2SToken:   "test-h2s",
	}
}
