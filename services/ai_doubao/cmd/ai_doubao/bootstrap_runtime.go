package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/ai_doubao/internal/app"

	"github.com/gorilla/websocket"
)

func runAIDoubao() {
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
	if _, err := hubsvc.LoadProjectConfig(filepath.Join(appRoot, "services", "ai_doubao")); err != nil {
		app.Errorf("load service config failed: %v", err)
		os.Exit(1)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	serviceSecretPath := app.ResolvePathFromRoot(appRoot, "services/ai_doubao/run/.service_secret")
	processStorePath := app.ResolvePathFromRoot(appRoot, "services/ai_doubao/run/.service_pid")
	runtimeManifestPath := app.ResolvePathFromRoot(appRoot, "services/ai_doubao/run/manifest_runtime.json")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}
	if err := hubsvc.CleanupPreviousServiceProcess(processStorePath, "ai_doubao"); err != nil {
		app.Errorf("cleanup previous process failed: %v", err)
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
			AllowedCallerTypes:   []string{"service", "user", "surface"},
			Idempotency:          "idempotent",
			TimeoutMSDefault:     65000,
			Streaming:            "none",
		},
		{
			Name:        "ai.vision.isr",
			Description: "Image structured reasoning: 输入图片、任务说明与期望 schema，返回结构化 JSON。",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"instruction":     map[string]any{"type": "string"},
				"images":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"response_schema": map[string]any{"type": "object"},
				"system_prompt":   map[string]any{"type": "string"},
				"temperature":     map[string]any{"type": "number"},
			}, "required": []string{"instruction", "images"}},
			OutputSchema:         map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}, "json": map[string]any{"type": "object"}, "model": map[string]any{"type": "string"}, "finish_reason": map[string]any{"type": "string"}}},
			SideEffect:           "none",
			CapabilitiesRequired: []string{"ai.vision.isr"},
			AllowedCallerTypes:   []string{"service", "user", "surface"},
			Idempotency:          "idempotent",
			TimeoutMSDefault:     70000,
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
		case "ai.vision.isr":
			result, err := callVisionISR(r.Context(), cfg, req.Args)
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "vision isr failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = result
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
	app.Infof("ai_doubao service listening at http://%s", *addr)
	ln, err := hubsvc.Listen(*addr)
	if err != nil {
		app.Errorf("service listen failed: %v", err)
		os.Exit(1)
	}
	startedAtMS := time.Now().UnixMilli()
	if err := hubsvc.RecordCurrentServiceProcess(processStorePath, "ai_doubao", startedAtMS); err != nil {
		app.Errorf("record current process failed: %v", err)
		os.Exit(1)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(ln)
	}()
	if hubToolCallURL != "" {
		healthy := true
		info := &app.AIServiceInfo{
			ServiceID:    "ai_doubao",
			ServiceName:  "ai_doubao",
			Version:      version,
			Provider:     "ai_doubao",
			Capabilities: []string{"ai.speech.asr", "ai.llm.stream", "ai.llm.generate", "ai.vision.isr", "ai.speech.tts"},
			Transport:    "http+ws",
		}
		manifest := app.BuildAIServiceManifest(info, toolDescriptors)
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
			shutdownNow("register to hub failed")
			return
		}
		if statusCode >= 300 {
			app.Errorf("register ai_doubao to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			shutdownNow("register to hub failed")
			return
		}
		registerResp, err := hubsvc.DecodeSupervisorRegisterResult(rawResp)
		if err != nil {
			app.Errorf("decode register response failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if err := hubsvc.WriteServiceRuntimeManifest(runtimeManifestPath, registerResp); err != nil {
			app.Errorf("write runtime manifest failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register ai_doubao to hub status=%d", statusCode)
		startHubToolHeartbeatGuard(hubToolCallURL, "ai_doubao", instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	if err := <-serveErrCh; err != nil && err != http.ErrServerClosed {
		app.Errorf("service listen failed: %v", err)
		os.Exit(1)
	}
}
