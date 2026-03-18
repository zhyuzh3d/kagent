package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	app "kagent/services/ai-doubao/internal/app"

	"github.com/gorilla/websocket"
)

func main() {
	configPath := flag.String("config", "config/configx.json", "path to private config json")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18081", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "AI-DOUBAO")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	serviceSecretPath := app.ResolvePathFromRoot(appRoot, "run/.service_secret")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}

	cfg, err := app.LoadModelConfig(configPathResolved, *modelName)
	if err != nil {
		app.Errorf("load config failed: %v", err)
		os.Exit(1)
	}

	version := "1.0.0"
	if strings.TrimSpace(serviceBootstrap.ServiceID) != "ai-doubao" {
		app.Errorf("bootstrap service_id mismatch: expect=ai-doubao got=%s", strings.TrimSpace(serviceBootstrap.ServiceID))
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(serviceBootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	instance := strings.TrimSpace(serviceBootstrap.InstanceID)
	if instance == "" {
		instance = strings.TrimSpace(*instanceID)
	}
	if instance == "" {
		instance = "ai-doubao-" + app.NewRequestID()
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
			WSPath:               "/service/tool/ws",
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
			Streaming:            "ws",
			WSPath:               "/service/tool/ws",
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

	if registerURL != "" {
		healthy := true
		info := &app.AIServiceInfo{
			ServiceID:    "ai-doubao",
			ServiceName:  "Doubao AI Service",
			Version:      version,
			Provider:     "doubao",
			Capabilities: []string{"ai.speech.asr", "ai.llm.stream", "ai.speech.tts"},
			Transport:    "http+ws",
		}
		manifest := app.BuildAIServiceManifest(info, toolDescriptors, true)
		registerPayload := app.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: app.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools:   toSupervisorToolsFromDescriptors(version, toolDescriptors),
			Healthy: &healthy,
		}
		raw, _ := json.Marshal(registerPayload)
		req, _ := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		hubsvc.ApplyServiceAuthHeaders(req.Header, serviceBootstrap)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			app.Errorf("register ai-doubao to hub failed: %v", err)
			os.Exit(1)
		}
		rawResp, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register ai-doubao to hub status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register ai-doubao to hub status=%d", resp.StatusCode)
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

	upgrader := websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			host := r.Host
			return strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost")
		},
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
			ServiceID:    "ai-doubao",
			ServiceName:  "Doubao AI Service",
			Version:      version,
			Provider:     "doubao",
			Capabilities: []string{"ai.speech.asr", "ai.llm.stream", "ai.speech.tts"},
			Transport:    "http+ws",
		})
	})
	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceListToolsResponse{
			ServiceID: "ai-doubao",
			Tools:     toolDescriptors,
		})
	})
	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeToolResponse(w, http.StatusMethodNotAllowed, app.CallResponse{
				Ok: false,
				Error: &app.ToolError{
					Code:    app.ErrorCodeBadRequest,
					Message: "method not allowed",
				},
			})
			return
		}
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, "ai-doubao", instance, serviceBootstrap.H2SToken); err != nil {
			writeToolResponse(w, http.StatusForbidden, app.CallResponse{
				Ok: false,
				Error: &app.ToolError{
					Code:    app.ErrorCodeForbidden,
					Message: "invalid hub auth",
				},
			})
			return
		}
		var req app.CallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeToolResponse(w, http.StatusBadRequest, app.CallResponse{
				Ok: false,
				Error: &app.ToolError{
					Code:    app.ErrorCodeBadRequest,
					Message: "invalid request body",
				},
			})
			return
		}
		req, err := app.NormalizeCallRequest(req)
		if err != nil {
			writeToolResponse(w, http.StatusBadRequest, app.CallResponse{
				Ok: false,
				Error: &app.ToolError{
					Code:    app.ErrorCodeBadRequest,
					Message: err.Error(),
				},
			})
			return
		}
		hubOnly := isHubOnlyContext(req.Context)
		if !hubOnly {
			delete(req.Args, "healthz")
		}
		startedAt := time.Now()
		resp := app.CallResponse{
			Ok: false,
			Meta: app.Meta{
				RequestID:  strings.TrimSpace(req.Context.RequestID),
				TraceID:    strings.TrimSpace(req.Context.TraceID),
				ServiceID:  "ai-doubao",
				InstanceID: strings.TrimSpace(instance),
			},
		}
		if hubOnly && healthzRequested(req.Args) {
			resp.Ok = true
			resp.Result = map[string]any{
				"service_id": "ai-doubao",
				"hub_only":   true,
				"healthz":    true,
			}
			writeToolResponse(w, http.StatusOK, resp)
			return
		}
		switch req.ToolID {
		case "ai.speech.tts":
			audio, format, err := synthesizeTTS(r.Context(), cfg, asString(req.Args["text"]))
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "tts synth failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"audio_base64": base64.StdEncoding.EncodeToString(audio),
				"format":       format,
			}
		default:
			resp.Error = &app.ToolError{
				Code:    app.ErrorCodeToolNotFound,
				Message: "tool not found",
			}
		}
		resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
		statusCode := http.StatusOK
		if !resp.Ok && resp.Error != nil {
			statusCode = app.HTTPStatusFromCode(resp.Error.Code)
		}
		writeToolResponse(w, statusCode, resp)
	})
	mux.HandleFunc("/service/tool/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, "ai-doubao", instance, serviceBootstrap.H2SToken); err != nil {
			http.Error(w, "invalid hub auth", http.StatusForbidden)
			return
		}
		switch strings.TrimSpace(r.URL.Query().Get("tool_id")) {
		case "ai.speech.asr":
			handleASRWS(w, r, cfg, upgrader)
		case "ai.llm.stream":
			handleLLMWS(w, r, cfg, upgrader)
		default:
			http.Error(w, "tool not found", http.StatusNotFound)
		}
	})

	mux.HandleFunc("/v1/asr/stream", func(w http.ResponseWriter, r *http.Request) {
		handleASRWS(w, r, cfg, upgrader)
	})
	mux.HandleFunc("/v1/llm/ws", func(w http.ResponseWriter, r *http.Request) {
		handleLLMWS(w, r, cfg, upgrader)
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
		audio, format, err := synthesizeTTS(r.Context(), cfg, req.Text)
		if err != nil {
			http.Error(w, "tts synth failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, app.AIServiceTTSSynthesizeResponse{
			AudioBase64: base64.StdEncoding.EncodeToString(audio),
			Format:      format,
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

		writeJSON(w, map[string]any{"ok": true, "message": "shutting down"})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		go func() {
			time.Sleep(20 * time.Millisecond)
			shutdownNow("admin shutdown requested")
		}()
	})

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if hbURL := buildHubHeartbeatURL(registerURL); hbURL != "" {
		startHubHeartbeatGuard(hbURL, "ai-doubao", instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	app.Infof("ai-doubao service listening at http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("service listen failed: %v", err)
		os.Exit(1)
	}
}

func handleASRWS(w http.ResponseWriter, r *http.Request, cfg *app.ModelConfig, upgrader websocket.Upgrader) {
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
}

func handleLLMWS(w http.ResponseWriter, r *http.Request, cfg *app.ModelConfig, upgrader websocket.Upgrader) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "upgrade failed", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	_, payload, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var req app.AIServiceLLMStreamRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		_ = conn.WriteJSON(app.AIServiceLLMStreamEvent{Type: "error", Error: "invalid request"})
		return
	}
	reqID := firstNonEmpty(req.RequestID, "svc-"+app.NewRequestID())
	app.Debugf("[ai-doubao] llm ws request_id=%s turn_id=%d", reqID, req.TurnID)

	llm := app.NewDoubaoLLMClient(cfg.ActiveChat(), nil)
	var writeMu sync.Mutex
	push := func(evt app.AIServiceLLMStreamEvent) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(evt)
	}

	finalText, err := llm.Stream(r.Context(), req.Input, req.History, func(delta string) {
		if strings.TrimSpace(delta) == "" {
			return
		}
		_ = push(app.AIServiceLLMStreamEvent{Type: "delta", Text: delta})
	})
	if err != nil {
		_ = push(app.AIServiceLLMStreamEvent{Type: "error", Error: err.Error()})
		app.Warnf("[ai-doubao] llm ws failed request_id=%s turn_id=%d err=%v", reqID, req.TurnID, err)
		return
	}
	_ = push(app.AIServiceLLMStreamEvent{Type: "final", Text: finalText})
	app.Debugf("[ai-doubao] llm ws finished request_id=%s turn_id=%d", reqID, req.TurnID)
}

func synthesizeTTS(ctx context.Context, cfg *app.ModelConfig, text string) ([]byte, string, error) {
	tts := app.NewDoubaoTTSClient(cfg.TTS, nil)
	return tts.Synthesize(ctx, strings.TrimSpace(text))
}

func toSupervisorToolsFromDescriptors(version string, descriptors []app.AIServiceToolDescriptor) []app.ServiceTool {
	tools := make([]app.ServiceTool, 0, len(descriptors))
	for _, descriptor := range descriptors {
		toolID := strings.TrimSpace(descriptor.Name)
		if toolID == "" {
			continue
		}
		streaming := strings.TrimSpace(descriptor.Streaming)
		isStreaming := streaming != "" && !strings.EqualFold(streaming, "none")
		tools = append(tools, app.ServiceTool{
			ToolID:    toolID,
			Version:   strings.TrimSpace(version),
			Streaming: isStreaming,
			WSPath:    strings.TrimSpace(descriptor.WSPath),
		})
	}
	return tools
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeToolResponse(w http.ResponseWriter, statusCode int, resp app.CallResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}

func asString(v any) string {
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv)
	default:
		return ""
	}
}

func healthzRequested(args map[string]any) bool {
	if args == nil {
		return false
	}
	value, ok := args["healthz"]
	if !ok {
		return false
	}
	switch tv := value.(type) {
	case bool:
		return tv
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "1", "true", "yes", "on":
			return true
		}
	case int:
		return tv != 0
	case int64:
		return tv != 0
	case float64:
		return tv != 0
	}
	return false
}

func isHubOnlyContext(ctx *app.CallContext) bool {
	if ctx == nil || ctx.Meta == nil {
		return false
	}
	value, ok := ctx.Meta["hub_only"]
	if !ok {
		return false
	}
	switch tv := value.(type) {
	case bool:
		return tv
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "1", "true", "yes", "on":
			return true
		}
	case float64:
		return tv != 0
	case int:
		return tv != 0
	case int64:
		return tv != 0
	}
	return false
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

func startHubHeartbeatGuard(heartbeatURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(heartbeatURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			body := map[string]any{
				"service_id":  strings.TrimSpace(serviceID),
				"instance_id": strings.TrimSpace(instanceID),
				"status":      "ready",
				"healthy":     true,
				"pid":         pid,
				"endpoint":    strings.TrimSpace(endpoint),
			}
			raw, _ := json.Marshal(body)
			req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(heartbeatURL), bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			hubsvc.ApplyServiceAuthHeaders(req.Header, serviceAuth)
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
