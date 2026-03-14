package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildServiceWSURL(t *testing.T) {
	wsURL, err := buildServiceWSURL("http://127.0.0.1:18081", "/v1/asr/stream")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wsURL != "ws://127.0.0.1:18081/v1/asr/stream" {
		t.Fatalf("unexpected ws url: %s", wsURL)
	}
}

func TestAIServiceManagerHealthProbe(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONForTest(w, AIServiceHealth{OK: true, Timestamp: nowMS(), Version: "vtest"})
	})
	mux.HandleFunc("/service/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONForTest(w, AIServiceInfo{
			ServiceID:    "ai-doubao",
			ServiceName:  "Doubao AI Service",
			Version:      "vtest",
			Provider:     "doubao",
			Capabilities: []string{"asr.stream", "llm.stream", "tts.synthesize"},
			Transport:    "http+ws",
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cfg := AIServiceConfig{
		Mode:             "service",
		BaseURL:          ts.URL,
		HealthIntervalMS: 30,
		HealthTimeoutMS:  500,
	}
	manager := NewAIServiceManager(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("manager start failed: %v", err)
	}
	ok := manager.WaitForHealthy(ctx, 2*time.Second)
	if !ok {
		t.Fatalf("expected manager to become healthy, status=%+v", manager.Snapshot())
	}
	status := manager.Snapshot()
	if !status.Healthy {
		t.Fatalf("expected healthy status")
	}
	if status.Info == nil || status.Info.ServiceID != "ai-doubao" {
		t.Fatalf("unexpected service info: %+v", status.Info)
	}
}

func writeJSONForTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
