package transport

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCallTCP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/tool/exec" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	client := NewClient(true)
	resp, err := client.Call(
		context.Background(),
		Endpoint{Transport: "tcp", TCPURL: server.URL},
		http.MethodPost,
		"/service/tool/exec",
		http.Header{"Content-Type": []string{"application/json"}},
		[]byte(`{}`),
		3*time.Second,
	)
	if err != nil {
		t.Fatalf("call tcp failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}
}

func TestCallUDS(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "svc.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix failed: %v", err)
	}
	defer listener.Close()
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/service/tool/exec" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}),
	}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()
	defer func() {
		_ = server.Close()
		<-done
		_ = os.Remove(socketPath)
	}()

	client := NewClient(false)
	resp, err := client.Call(
		context.Background(),
		Endpoint{Transport: "uds", UDSPath: socketPath},
		http.MethodPost,
		"/service/tool/exec",
		http.Header{"Content-Type": []string{"application/json"}},
		[]byte(`{}`),
		3*time.Second,
	)
	if err != nil {
		t.Fatalf("call uds failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}
}
