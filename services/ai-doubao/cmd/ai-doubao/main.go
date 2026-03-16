package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"kagent/pkg/toolproto"
	app "kagent/services/ai-doubao/internal/app"

	"github.com/gorilla/websocket"
)

func main() {
	configPath := flag.String("config", "services/ai-doubao/config/configx.json", "path to private config json")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18081", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint, e.g. http://127.0.0.1:18080/api/service/register")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "AI-DOUBAO")

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
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("ai-doubao shutdown: %s", strings.TrimSpace(reason))
			if server != nil {
				_ = server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				_ = server.Shutdown(ctx)
				cancel()
			}
			time.Sleep(80 * time.Millisecond)
			os.Exit(0)
		})
	}

	toolDescriptors := []app.AIServiceToolDescriptor{
		{
			Name:                 "ai.speech.asr",
			Description:          "Stream PCM16 audio and receive ASR partial/final/endpoint events.",
			InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"audio": map[string]any{"type": "string", "description": "binary PCM stream via websocket frames"}}},
			OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
			SideEffect:           "read",
			CapabilitiesRequired: []string{"ai.speech.asr"},
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
			Name:                 "ai.speech.tts",
			Description:          "Synthesize text to speech audio bytes.",
			InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"audio_base64": map[string]any{"type": "string"}, "format": map[string]any{"type": "string"}}},
			SideEffect:           "read",
			CapabilitiesRequired: []string{"ai.speech.tts"},
			Idempotency:          "idempotent",
			TimeoutMSDefault:     35000,
			Streaming:            "none",
		},
	}
	instance := strings.TrimSpace(*instanceID)
	if instance == "" {
		instance = "ai-doubao-" + app.NewRequestID()
	}

	if strings.TrimSpace(*hubRegisterURL) != "" {
		info := &app.AIServiceInfo{
			ServiceID:   "ai-doubao",
			ServiceName: "Doubao AI Service",
			Version:     version,
			Provider:    "doubao",
			Capabilities: []string{
				"ai.speech.asr",
				"ai.llm.stream",
				"ai.speech.tts",
			},
			Transport: "http+ws",
		}
		manifest := app.BuildAIServiceManifest(info, toolDescriptors, true)
		registerPayload := toolproto.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: toolproto.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools: toSupervisorToolsFromDescriptors(version, toolDescriptors),
		}
		raw, _ := json.Marshal(registerPayload)
		req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(*hubRegisterURL), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			app.Errorf("register ai-doubao to hub failed: %v", err)
			os.Exit(1)
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register ai-doubao to hub status=%d", resp.StatusCode)
			os.Exit(1)
		}
		app.Infof("register ai-doubao to hub status=%d", resp.StatusCode)
	}

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
				"ai.speech.asr",
				"ai.llm.stream",
				"ai.speech.tts",
			},
			Transport: "http+ws",
		})
	})

	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceListToolsResponse{
			ServiceID: "ai-doubao",
			Tools:     toolDescriptors,
		})
	})

	mux.HandleFunc("/admin/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "bad remote addr", http.StatusBadRequest)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		app.Warnf("ai-doubao shutdown requested from %s", r.RemoteAddr)
		writeJSON(w, map[string]any{
			"ok":      true,
			"message": "shutting down",
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		go func() {
			time.Sleep(20 * time.Millisecond)
			shutdownNow("admin shutdown requested")
		}()
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

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if hbURL := buildHubHeartbeatURL(strings.TrimSpace(*hubRegisterURL)); hbURL != "" {
		startHubHeartbeatGuard(hbURL, "ai-doubao", instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), shutdownNow)
	}
	app.Infof("ai-doubao service listening at http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("service listen failed: %v", err)
		os.Exit(1)
	}
}

func toSupervisorToolsFromDescriptors(version string, descriptors []app.AIServiceToolDescriptor) []toolproto.ServiceTool {
	tools := make([]toolproto.ServiceTool, 0, len(descriptors))
	for _, descriptor := range descriptors {
		toolID := strings.TrimSpace(descriptor.Name)
		if toolID == "" {
			continue
		}
		tools = append(tools, toolproto.ServiceTool{
			ToolID:    toolID,
			Version:   strings.TrimSpace(version),
			Streaming: strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
		})
	}
	return tools
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

func buildHubHeartbeatURL(registerURL string) string {
	raw := strings.TrimSpace(registerURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/service/heartbeat"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func startHubHeartbeatGuard(heartbeatURL string, serviceID string, instanceID string, pid int, endpoint string, onFailure func(reason string)) {
	if strings.TrimSpace(heartbeatURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			body := map[string]any{
				"service_id":  strings.TrimSpace(serviceID),
				"instance_id": strings.TrimSpace(instanceID),
				"pid":         pid,
				"endpoint":    strings.TrimSpace(endpoint),
			}
			raw, _ := json.Marshal(body)
			req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(heartbeatURL), bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 2200 * time.Millisecond}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d", resp.StatusCode)
			}
			return nil
		}

		if err := send(); err != nil {
			onFailure("hub heartbeat failed: " + err.Error())
			return
		}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := send(); err != nil {
				onFailure("hub heartbeat failed: " + err.Error())
				return
			}
		}
	}()
}
