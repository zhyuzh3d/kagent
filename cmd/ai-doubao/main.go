package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	app "kagent/internal"

	"github.com/gorilla/websocket"
)

func main() {
	configPath := flag.String("config", "config/configx.json", "path to private config json")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18081", "listen addr")
	flag.Parse()

	app.InitLogger(app.LevelDebug)

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	cfg, err := app.LoadModelConfig(configPathResolved, *modelName)
	if err != nil {
		app.Errorf("load config failed: %v", err)
		os.Exit(1)
	}

	version := "unknown"
	if v, err := app.LoadVersionInfo(app.ResolvePathFromRoot(appRoot, "version.json")); err == nil {
		version = v.Backend
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceHealth{
			OK:        true,
			Timestamp: time.Now().UnixMilli(),
			Version:   version,
		})
	})

	mux.HandleFunc("/service/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceInfo{
			ServiceID:   "ai-doubao",
			ServiceName: "Doubao AI Service",
			Version:     version,
			Provider:    "doubao",
			Capabilities: []string{
				"asr.stream",
				"llm.stream",
				"tts.synthesize",
			},
			Transport: "http+ws",
		})
	})

	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceListToolsResponse{
			ServiceID: "ai-doubao",
			Tools: []app.AIServiceToolDescriptor{
				{
					Name:                 "ai.asr.stream",
					Description:          "Stream PCM16 audio and receive ASR partial/final/endpoint events.",
					InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"audio": map[string]any{"type": "string", "description": "binary PCM stream via websocket frames"}}},
					OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
					SideEffect:           "read",
					CapabilitiesRequired: []string{"ai.asr"},
					Idempotency:          "unknown",
					TimeoutMSDefault:     60000,
					Streaming:            "ws_binary",
				},
				{
					Name:                 "ai.llm.stream",
					Description:          "Stream text deltas and final response from LLM.",
					InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}},
					OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
					SideEffect:           "none",
					CapabilitiesRequired: []string{"ai.llm"},
					Idempotency:          "idempotent",
					TimeoutMSDefault:     65000,
					Streaming:            "sse",
				},
				{
					Name:                 "ai.tts.synthesize",
					Description:          "Synthesize text to speech audio bytes.",
					InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
					OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"audio_base64": map[string]any{"type": "string"}, "format": map[string]any{"type": "string"}}},
					SideEffect:           "read",
					CapabilitiesRequired: []string{"ai.tts"},
					Idempotency:          "idempotent",
					TimeoutMSDefault:     35000,
					Streaming:            "none",
				},
			},
		})
	})

	upgrader := websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			host := r.Host
			return strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost")
		},
	}

	mux.HandleFunc("/v1/asr/stream", func(w http.ResponseWriter, r *http.Request) {
		reqID := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Request-ID")), "svc-"+app.NewRequestID())
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		defer conn.Close()
		app.Debugf("[ai-doubao] asr stream open request_id=%s", reqID)

		mt, payload, err := conn.ReadMessage()
		if err != nil || mt != websocket.TextMessage {
			_ = conn.WriteJSON(app.AIServiceASREvent{Type: "error", Error: "missing start request"})
			return
		}
		var startReq app.AIServiceASRStart
		if err := json.Unmarshal(payload, &startReq); err != nil || strings.TrimSpace(startReq.Type) != "start" {
			_ = conn.WriteJSON(app.AIServiceASREvent{Type: "error", Error: "invalid start request"})
			return
		}

		asr := app.NewDoubaoASRClient(cfg.ASR, nil)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		audioCh := make(chan []byte, 64)
		events := make(chan app.ASREvent, 64)
		sendDone := make(chan struct{})

		go func() {
			defer close(sendDone)
			for evt := range events {
				out := app.AIServiceASREvent{
					Type: string(evt.Type),
					Text: evt.Text,
				}
				if err := conn.WriteJSON(out); err != nil {
					cancel()
					return
				}
			}
		}()

		go func() {
			defer close(audioCh)
			for {
				mt, raw, err := conn.ReadMessage()
				if err != nil {
					cancel()
					return
				}
				switch mt {
				case websocket.BinaryMessage:
					if len(raw) == 0 {
						continue
					}
					frame := append([]byte(nil), raw...)
					select {
					case audioCh <- frame:
					case <-ctx.Done():
						return
					}
				case websocket.TextMessage:
					var ctrl app.AIServiceASRControl
					if err := json.Unmarshal(raw, &ctrl); err != nil {
						continue
					}
					if strings.EqualFold(strings.TrimSpace(ctrl.Type), "finish") {
						asr.Finish()
					}
				}
			}
		}()

		runErr := asr.Run(ctx, audioCh, events, startReq.History)
		close(events)
		<-sendDone
		if runErr != nil && ctx.Err() == nil {
			_ = conn.WriteJSON(app.AIServiceASREvent{Type: "error", Error: runErr.Error()})
			app.Warnf("[ai-doubao] asr stream failed request_id=%s turn_id=%d err=%v", firstNonEmpty(startReq.RequestID, reqID), startReq.TurnID, runErr)
			return
		}
		app.Debugf("[ai-doubao] asr stream closed request_id=%s turn_id=%d", firstNonEmpty(startReq.RequestID, reqID), startReq.TurnID)
	})

	mux.HandleFunc("/v1/llm/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req app.AIServiceLLMStreamRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		reqID := firstNonEmpty(req.RequestID, strings.TrimSpace(r.Header.Get("X-Request-ID")), "svc-"+app.NewRequestID())
		app.Debugf("[ai-doubao] llm stream request_id=%s turn_id=%d", reqID, req.TurnID)

		llm := app.NewDoubaoLLMClient(cfg.ActiveChat(), nil)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", http.StatusInternalServerError)
			return
		}

		streamCtx, cancel := context.WithCancel(r.Context())
		defer cancel()

		var writeMu sync.Mutex
		var writeErr error
		pushEvent := func(evt app.AIServiceLLMStreamEvent) {
			writeMu.Lock()
			defer writeMu.Unlock()
			if writeErr != nil {
				return
			}
			line, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
				writeErr = err
				cancel()
				return
			}
			flusher.Flush()
		}

		finalText, err := llm.Stream(streamCtx, req.Input, req.History, func(delta string) {
			pushEvent(app.AIServiceLLMStreamEvent{Type: "delta", Text: delta})
		})
		if writeErr != nil {
			return
		}
		if err != nil {
			pushEvent(app.AIServiceLLMStreamEvent{Type: "error", Error: err.Error()})
			app.Warnf("[ai-doubao] llm stream failed request_id=%s turn_id=%d err=%v", reqID, req.TurnID, err)
			return
		}
		pushEvent(app.AIServiceLLMStreamEvent{Type: "final", Text: finalText})
		app.Debugf("[ai-doubao] llm stream finished request_id=%s turn_id=%d", reqID, req.TurnID)
	})

	mux.HandleFunc("/v1/tts/synthesize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req app.AIServiceTTSSynthesizeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		reqID := firstNonEmpty(req.RequestID, strings.TrimSpace(r.Header.Get("X-Request-ID")), "svc-"+app.NewRequestID())
		app.Debugf("[ai-doubao] tts synth request_id=%s turn_id=%d", reqID, req.TurnID)
		tts := app.NewDoubaoTTSClient(cfg.TTS, nil)
		audio, format, err := tts.Synthesize(r.Context(), req.Text)
		if err != nil {
			http.Error(w, "tts synth failed: "+err.Error(), http.StatusBadRequest)
			app.Warnf("[ai-doubao] tts synth failed request_id=%s turn_id=%d err=%v", reqID, req.TurnID, err)
			return
		}
		writeJSON(w, app.AIServiceTTSSynthesizeResponse{
			AudioBase64: base64.StdEncoding.EncodeToString(audio),
			Format:      format,
		})
		app.Debugf("[ai-doubao] tts synth finished request_id=%s turn_id=%d bytes=%d", reqID, req.TurnID, len(audio))
	})

	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	app.Infof("ai-doubao service listening at http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("service listen failed: %v", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
