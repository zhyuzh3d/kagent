package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONForTest(w, AIServiceListToolsResponse{
			ServiceID: "ai-doubao",
			Tools: []AIServiceToolDescriptor{
				{
					Name:         "ai.llm.stream",
					Description:  "test",
					InputSchema:  map[string]any{"type": "object"},
					OutputSchema: map[string]any{"type": "object"},
				},
			},
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

func TestServiceLLMClientFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/llm/stream", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := &ServiceLLMClient{
		baseURL:        ts.URL,
		requestTimeout: 2 * time.Second,
		fallback: &fakeLLMClient{
			final: "fallback-answer",
		},
	}

	got, err := c.Stream(context.Background(), "hello", nil, nil)
	if err != nil {
		t.Fatalf("expected fallback success, got err=%v", err)
	}
	if got != "fallback-answer" {
		t.Fatalf("unexpected fallback output: %s", got)
	}
}

func TestServiceLLMClientSSESuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/llm/stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"delta\",\"text\":\"你\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"delta\",\"text\":\"好\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"final\",\"text\":\"你好\"}\n\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var deltas []string
	c := &ServiceLLMClient{
		baseURL:        ts.URL,
		requestTimeout: 2 * time.Second,
	}
	got, err := c.Stream(context.Background(), "hello", nil, func(delta string) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if got != "你好" {
		t.Fatalf("unexpected final text: %s", got)
	}
	if len(deltas) != 2 || deltas[0] != "你" || deltas[1] != "好" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
}

func TestServiceTTSClientFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/tts/synthesize", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := &ServiceTTSClient{
		baseURL:        ts.URL,
		requestTimeout: 2 * time.Second,
		fallback: &fakeTTSClient{
			audio:  []byte{1, 2, 3},
			format: "audio/mpeg",
		},
	}
	audio, format, err := c.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("expected fallback success, got err=%v", err)
	}
	if format != "audio/mpeg" || len(audio) != 3 {
		t.Fatalf("unexpected fallback tts result: format=%s len=%d", format, len(audio))
	}
}

func TestServiceASRClientFallbackOnDialFailure(t *testing.T) {
	fb := &fakeASRClient{}
	c := &ServiceASRClient{
		baseURL:  "http://127.0.0.1:1",
		dialer:   &websocket.Dialer{HandshakeTimeout: 100 * time.Millisecond},
		fallback: fb,
	}
	audio := make(chan []byte)
	events := make(chan ASREvent, 4)
	close(audio)
	err := c.Run(context.Background(), audio, events, nil)
	if err != nil {
		t.Fatalf("expected fallback success, got err=%v", err)
	}
	if !fb.runCalled {
		t.Fatalf("expected fallback asr run to be called")
	}
	if len(fb.historyArg) != 0 {
		t.Fatalf("expected history passthrough")
	}
}

func writeJSONForTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

type fakeLLMClient struct {
	final string
}

func (f *fakeLLMClient) Stream(_ context.Context, _ string, _ []ChatMessage, _ func(string)) (string, error) {
	return f.final, nil
}

type fakeTTSClient struct {
	audio  []byte
	format string
}

func (f *fakeTTSClient) Synthesize(_ context.Context, _ string) ([]byte, string, error) {
	return f.audio, f.format, nil
}

type fakeASRClient struct {
	runCalled  bool
	historyArg []ChatMessage
}

func (f *fakeASRClient) Run(_ context.Context, _ <-chan []byte, events chan<- ASREvent, history []ChatMessage) error {
	f.runCalled = true
	f.historyArg = append([]ChatMessage(nil), history...)
	events <- ASREvent{Type: ASREventFinal, Text: "fb"}
	return nil
}

func (f *fakeASRClient) Finish() {}
