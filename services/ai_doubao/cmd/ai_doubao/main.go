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

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/ai_doubao/internal/app"

	"github.com/gorilla/websocket"
)

func main() {
	configPath := flag.String("config", "services/ai_doubao/config/configx.json", "path to private config json")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18081", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "AI_DOUBAO")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	serviceSecretPath := app.ResolvePathFromRoot(appRoot, "services/ai_doubao/run/.service_secret")
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
	if strings.TrimSpace(serviceBootstrap.ServiceID) != "ai_doubao" {
		app.Errorf("bootstrap service_id mismatch: expect=ai_doubao got=%s", strings.TrimSpace(serviceBootstrap.ServiceID))
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
		instance = "ai_doubao-" + app.NewRequestID()
	}

	toolDescriptors := []app.AIServiceToolDescriptor{
		{
			Name:                 "ai.speech.asr",
			Description:          "Stream PCM16 audio and receive ASR partial/final/endpoint events.",
			InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"audio": map[string]any{"type": "string", "description": "binary PCM stream via websocket frames"}}},
			OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
			SideEffect:           "read",
			CapabilitiesRequired: []string{"ai.speech.asr"},
			AllowedCallerTypes:   []string{"service"},
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
			AllowedCallerTypes:   []string{"service"},
			Idempotency:          "idempotent",
			TimeoutMSDefault:     65000,
			Streaming:            "ws",
			WSPath:               "/service/tool/ws",
		},
		{
			Name:                 "ai.llm.generate",
			Description:          "Generate one full text output from LLM using optional custom system prompt.",
			InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}, "system_prompt": map[string]any{"type": "string"}}},
			OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			SideEffect:           "none",
			CapabilitiesRequired: []string{"ai.llm"},
			AllowedCallerTypes:   []string{"service", "user"},
			Idempotency:          "idempotent",
			TimeoutMSDefault:     65000,
			Streaming:            "none",
		},
		{
			Name:                 "ai.speech.tts",
			Description:          "Synthesize text to speech audio bytes.",
			InputSchema:          map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"audio_base64": map[string]any{"type": "string"}, "format": map[string]any{"type": "string"}}},
			SideEffect:           "read",
			CapabilitiesRequired: []string{"ai.speech.tts"},
			AllowedCallerTypes:   []string{"service"},
			Idempotency:          "idempotent",
			TimeoutMSDefault:     35000,
			Streaming:            "none",
		},
		{
			Name:               "service.lifecycle.shutdown",
			Description:        "Gracefully shutdown the service process.",
			AllowedCallerTypes: []string{"service"},
			InputSchema:        map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string"}}},
			SideEffect:         "none",
			Streaming:          "none",
		},
		{
			Name:               "service.lifecycle.health",
			Description:        "Query internal health and version state.",
			AllowedCallerTypes: []string{"service"},
			InputSchema:        map[string]any{"type": "object"},
			OutputSchema:       map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "version": map[string]any{"type": "string"}}},
			SideEffect:         "none",
			Streaming:          "none",
		},
		{
			Name:               "service.lifecycle.state.get",
			Description:        "Return lifecycle status snapshot for Hub governance.",
			AllowedCallerTypes: []string{"service"},
			InputSchema:        map[string]any{"type": "object"},
			OutputSchema:       map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "string"}, "healthy": map[string]any{"type": "boolean"}}},
			SideEffect:         "none",
			Streaming:          "none",
		},
	}

	hubToolCallURL := buildHubToolCallURL(registerURL)
	if hubToolCallURL != "" {
		healthy := true
		info := &app.AIServiceInfo{
			ServiceID:    "ai_doubao",
			ServiceName:  "ai_doubao",
			Version:      version,
			Provider:     "ai_doubao",
			Capabilities: []string{"ai.speech.asr", "ai.llm.stream", "ai.llm.generate", "ai.speech.tts"},
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

		callReq := toolproto.CallRequest{
			ToolID: "hub.governance.service.register",
			Args:   structToMap(registerPayload),
			Context: &toolproto.Context{
				RequestID: "reg-" + app.NewRequestID(),
				TraceID:   "tr-" + app.NewRequestID(),
				Caller: toolproto.Caller{
					Type:      "service",
					ServiceID: strings.TrimSpace(manifest.ServiceID),
				},
			},
		}
		rawResp, statusCode, err := postHubToolCall(hubToolCallURL, serviceBootstrap, callReq)
		if err != nil {
			app.Errorf("register ai_doubao to hub failed: %v", err)
			os.Exit(1)
		}
		if statusCode >= 300 {
			app.Errorf("register ai_doubao to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register ai_doubao to hub status=%d", statusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("ai_doubao shutdown: %s", strings.TrimSpace(reason))
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

	// Remove legacy endpoints
	// healthz, service/info, service/tools are now redundant tools
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
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, "ai_doubao", instance, serviceBootstrap.H2SToken); err != nil {
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
				ServiceID:  "ai_doubao",
				InstanceID: strings.TrimSpace(instance),
			},
		}
		if hubOnly && healthzRequested(req.Args) {
			resp.Ok = true
			resp.Result = map[string]any{
				"service_id": "ai_doubao",
				"hub_only":   true,
				"healthz":    true,
			}
			writeToolResponse(w, http.StatusOK, resp)
			return
		}
		switch req.ToolID {
		case "service.lifecycle.health", "ai_doubao.system.health":
			resp.Ok = true
			resp.Result = map[string]any{
				"service_id":   "ai_doubao",
				"instance_id":  strings.TrimSpace(instance),
				"endpoint":     "http://" + strings.TrimSpace(*addr),
				"pid":          os.Getpid(),
				"healthy":      true,
				"status":       "ready",
				"version":      version,
				"timestamp_ms": time.Now().UnixMilli(),
			}
		case "service.lifecycle.state.get":
			resp.Ok = true
			resp.Result = map[string]any{
				"service_id":   "ai_doubao",
				"instance_id":  strings.TrimSpace(instance),
				"endpoint":     "http://" + strings.TrimSpace(*addr),
				"pid":          os.Getpid(),
				"healthy":      true,
				"status":       "ready",
				"version":      version,
				"timestamp_ms": time.Now().UnixMilli(),
			}
		case "service.lifecycle.shutdown", "ai_doubao.system.shutdown":
			reason := asString(req.Args["reason"])
			if reason == "" {
				reason = "shutdown requested via tool"
			}
			resp.Ok = true
			resp.Result = map[string]any{"status": "shutting_down"}
			writeToolResponse(w, http.StatusOK, resp)
			go func() {
				time.Sleep(100 * time.Millisecond)
				shutdownNow(reason)
			}()
			return
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
		case "ai.llm.generate":
			llm := app.NewDoubaoLLMClient(cfg.ActiveChat(), nil)
			finalText, err := llm.StreamWithSystem(r.Context(), asString(req.Args["system_prompt"]), asString(req.Args["input"]), nil, nil)
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "llm generate failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"text": finalText,
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
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, "ai_doubao", instance, serviceBootstrap.H2SToken); err != nil {
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
	// admin/shutdown is now handled via service.lifecycle.shutdown tool

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if hubToolCallURL != "" {
		startHubToolHeartbeatGuard(hubToolCallURL, "ai_doubao", instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	app.Infof("ai_doubao service listening at http://%s", *addr)
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
	app.Debugf("[ai_doubao] asr stream open request_id=%s", reqID)

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
		app.Warnf("[ai_doubao] asr stream failed request_id=%s turn_id=%d err=%v", firstNonEmpty(startReq.RequestID, reqID), startReq.TurnID, runErr)
		return
	}
	app.Debugf("[ai_doubao] asr stream closed request_id=%s turn_id=%d", firstNonEmpty(startReq.RequestID, reqID), startReq.TurnID)
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
	app.Debugf("[ai_doubao] llm ws request_id=%s turn_id=%d", reqID, req.TurnID)

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
		app.Warnf("[ai_doubao] llm ws failed request_id=%s turn_id=%d err=%v", reqID, req.TurnID, err)
		return
	}
	_ = push(app.AIServiceLLMStreamEvent{Type: "final", Text: finalText})
	app.Debugf("[ai_doubao] llm ws finished request_id=%s turn_id=%d", reqID, req.TurnID)
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
		protocol := "http"
		if isStreaming {
			protocol = "ws"
		}
		tools = append(tools, app.ServiceTool{
			ToolID:               toolID,
			Description:          strings.TrimSpace(descriptor.Description),
			Protocol:             protocol,
			Version:              strings.TrimSpace(version),
			Streaming:            isStreaming,
			StreamingMode:        streaming,
			WSPath:               strings.TrimSpace(descriptor.WSPath),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			TimeoutMSDefault:     descriptor.TimeoutMSDefault,
			InputSchema:          descriptor.InputSchema,
			OutputSchema:         descriptor.OutputSchema,
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
			AllowedCallerTypes:   append([]string(nil), descriptor.AllowedCallerTypes...),
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

func buildHubToolCallURL(registerURL string) string {
	return hubsvc.BuildHubToolCallURL(registerURL)
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(&http.Client{Timeout: 3 * time.Second}, hubToolCallURL, serviceAuth, req)
}

func startHubToolHeartbeatGuard(hubToolCallURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  strings.TrimSpace(serviceID),
					"instance_id": strings.TrimSpace(instanceID),
					"status":      "ready",
					"healthy":     true,
					"pid":         pid,
					"endpoint":    strings.TrimSpace(endpoint),
				},
				Context: &toolproto.Context{
					RequestID: "hb-" + app.NewRequestID(),
					TraceID:   "tr-" + app.NewRequestID(),
					Caller: toolproto.Caller{
						Type:      "service",
						ServiceID: strings.TrimSpace(serviceID),
					},
				},
			}
			rawResp, statusCode, err := postHubToolCall(hubToolCallURL, serviceAuth, callReq)
			if err != nil {
				return err
			}
			if statusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			}
			var resp toolproto.CallResponse
			if err := json.Unmarshal(rawResp, &resp); err != nil {
				return fmt.Errorf("decode heartbeat response: %w", err)
			}
			if !resp.Ok {
				if resp.Error != nil && strings.TrimSpace(resp.Error.Message) != "" {
					return fmt.Errorf("heartbeat rejected: %s", strings.TrimSpace(resp.Error.Message))
				}
				return fmt.Errorf("heartbeat rejected")
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

func structToMap(v any) map[string]any {
	raw, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
