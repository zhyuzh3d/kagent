package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/chat_server/internal/app"

	"github.com/gorilla/websocket"
)

type forwardedClaims struct {
	UserID string
}

func main() {
	configPath := flag.String("config", "services/chat_server/config/configx.json", "path to private config json")
	publicConfigPath := flag.String("public-config", "services/chat_server/config/config.json", "path to public config json")
	projectID := flag.String("project-id", "project-default", "runtime project id")
	threadID := flag.String("thread-id", "chat-default", "runtime thread id")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18082", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "CHAT_SERVER")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	if _, err := hubsvc.LoadProjectConfig(filepath.Join(appRoot, "services", "chat_server")); err != nil {
		app.Errorf("load service config failed: %v", err)
		os.Exit(1)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	publicConfigPathResolved := app.ResolvePathFromRoot(appRoot, *publicConfigPath)
	serviceSecretPath := app.ResolvePathFromRoot(appRoot, "services/chat_server/run/.service_secret")
	processStorePath := app.ResolvePathFromRoot(appRoot, "services/chat_server/run/.service_pid")
	runtimeManifestPath := app.ResolvePathFromRoot(appRoot, "services/chat_server/run/manifest_runtime.json")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}
	if err := hubsvc.CleanupPreviousServiceProcess(processStorePath, "chat_server"); err != nil {
		app.Errorf("cleanup previous process failed: %v", err)
		os.Exit(1)
	}

	cfg, err := app.LoadModelConfig(configPathResolved, *modelName)
	if err != nil {
		app.Errorf("load config failed: %v", err)
		os.Exit(1)
	}
	runtimeCfg, err := app.NewRuntimeConfigManager(publicConfigPathResolved)
	if err != nil {
		app.Errorf("load runtime config failed: %v", err)
		os.Exit(1)
	}
	hubBaseURL := strings.TrimSpace(cfg.EffectiveAIService().BaseURL)
	if hubBaseURL == "" {
		app.Errorf("chat_server requires ai_service.baseUrl as hub url")
		os.Exit(1)
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	manifest := app.ChatServerServiceManifest()
	if strings.TrimSpace(serviceBootstrap.ServiceID) != strings.TrimSpace(manifest.ServiceID) {
		app.Errorf("bootstrap service_id mismatch: expect=%s got=%s", strings.TrimSpace(manifest.ServiceID), strings.TrimSpace(serviceBootstrap.ServiceID))
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
		instance = "chat_server-" + app.NewRequestID()
	}

	hubToolCallURL := buildHubToolCallURL(registerURL)
	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("chat_server shutdown: %s", strings.TrimSpace(reason))
			appCancel()
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

	openToolStore := func(ctx context.Context, uid string, pid string, tid string) (app.ChatStore, error) {
		client := app.NewHubToolClient(hubBaseURL, serviceBootstrap, 70*time.Second)
		return app.NewHubDatabaseStoreWithOptions(ctx, client, uid, pid, tid, app.HubDatabaseStoreOptions{EnsureDefaults: false})
	}
	openSessionStore := func(ctx context.Context, uid string, pid string, tid string) (app.ChatStore, error) {
		client := app.NewHubToolClient(hubBaseURL, serviceBootstrap, 70*time.Second)
		return app.NewHubDatabaseStoreWithOptions(ctx, client, uid, firstNonEmpty(pid, *projectID), firstNonEmpty(tid, *threadID), app.HubDatabaseStoreOptions{EnsureDefaults: true})
	}

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
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, manifest.ServiceID, instance, serviceBootstrap.H2SToken); err != nil {
			writeToolResponse(w, http.StatusForbidden, app.CallResponse{
				Ok: false,
				Error: &app.ToolError{
					Code:    app.ErrorCodeForbidden,
					Message: "invalid hub auth",
				},
				Meta: app.Meta{
					RequestID: strings.TrimSpace(req.Context.RequestID),
					TraceID:   strings.TrimSpace(req.Context.TraceID),
				},
			})
			return
		}
		meta := app.Meta{
			RequestID:  strings.TrimSpace(req.Context.RequestID),
			TraceID:    strings.TrimSpace(req.Context.TraceID),
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
		}
		caller := hubsvc.MergeCaller((*toolproto.CallRequest)(&req), r.Header)
		hubsvc.MergeOriginCaller((*toolproto.CallRequest)(&req), r.Header)
		reqCtx := hubsvc.ContextWithDelegation(r.Context(), req.Context.OriginCaller, req.Context.OriginToken)
		effectiveCaller := caller
		if strings.TrimSpace(req.Context.OriginCaller.UserID) != "" {
			effectiveCaller.UserID = strings.TrimSpace(req.Context.OriginCaller.UserID)
		}
		if req.ToolID == "service.lifecycle.health" {
			writeToolResponse(w, http.StatusOK, app.CallResponse{
				Ok: true,
				Result: map[string]any{
					"service_id":   strings.TrimSpace(manifest.ServiceID),
					"instance_id":  strings.TrimSpace(instance),
					"endpoint":     "http://" + strings.TrimSpace(*addr),
					"pid":          os.Getpid(),
					"version":      strings.TrimSpace(manifest.Version),
					"healthy":      true,
					"status":       "ready",
					"timestamp_ms": time.Now().UnixMilli(),
				},
				Meta: meta,
			})
			return
		}
		if req.ToolID == "service.lifecycle.state.get" {
			writeToolResponse(w, http.StatusOK, app.CallResponse{
				Ok: true,
				Result: map[string]any{
					"service_id":   strings.TrimSpace(manifest.ServiceID),
					"instance_id":  strings.TrimSpace(instance),
					"endpoint":     "http://" + strings.TrimSpace(*addr),
					"pid":          os.Getpid(),
					"version":      strings.TrimSpace(manifest.Version),
					"healthy":      true,
					"status":       "ready",
					"timestamp_ms": time.Now().UnixMilli(),
					"tools":        []string{"app.chat.project_list", "app.chat.project_create", "app.chat.project_update", "app.chat.project_delete", "app.chat.thread_list", "app.chat.thread_create", "app.chat.thread_update", "app.chat.thread_delete", "app.chat.config.get", "app.chat.config.update", "app.chat.stream"},
				},
				Meta: meta,
			})
			return
		}
		if req.ToolID == "service.lifecycle.shutdown" {
			resp := app.CallResponse{
				Ok:     true,
				Result: map[string]any{"status": "shutting_down"},
				Meta:   meta,
			}
			writeToolResponse(w, http.StatusOK, resp)
			go func() {
				time.Sleep(100 * time.Millisecond)
				shutdownNow("shutdown requested via tool")
			}()
			return
		}
		if strings.TrimSpace(effectiveCaller.UserID) == "" {
			writeToolResponse(w, http.StatusUnauthorized, app.CallResponse{
				Ok: false,
				Error: &app.ToolError{
					Code:    app.ErrorCodeUnauthorized,
					Message: "missing caller user",
				},
				Meta: app.Meta{
					RequestID: strings.TrimSpace(req.Context.RequestID),
					TraceID:   strings.TrimSpace(req.Context.TraceID),
				},
			})
			return
		}
		startedAt := time.Now()
		resp := app.CallResponse{
			Ok:     false,
			Result: nil,
			Error:  nil,
			Meta:   meta,
		}
		store, err := openToolStore(reqCtx, effectiveCaller.UserID, asString(req.Args["project_id"]), asString(req.Args["thread_id"]))
		if err != nil {
			resp.Error = &app.ToolError{
				Code:    app.ErrorCodeInternalError,
				Message: "open store failed: " + err.Error(),
			}
			resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
			writeToolResponse(w, app.HTTPStatusFromCode(resp.Error.Code), resp)
			return
		}
		defer store.Close()

		switch req.ToolID {
		case "app.chat.config.get":
			resp.Ok = true
			resp.Result = runtimeCfg.EffectiveMap()
		case "app.chat.config.update":
			nextCfg, ok := asConfigMap(req.Args["config"])
			if !ok {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "config object is required"}
				break
			}
			effective, err := runtimeCfg.UpdateEffectiveMap(nextCfg)
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeInternalError, Message: "config update failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = effective
		case "app.chat.project_list":
			list, err := store.ListProjectsForUser(effectiveCaller.UserID)
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "project list failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"items": list, "count": len(list)}
		case "app.chat.project_create":
			projectIDNew, err := store.CreateProject(effectiveCaller.UserID, asString(req.Args["title"]))
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "project create failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"project_id": projectIDNew}
		case "app.chat.project_update":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "project_id is required"}
				break
			}
			if err := store.UpdateProject(projectIDReq, asString(req.Args["title"]), asInt(req.Args["order_index"], 0)); err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "project update failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		case "app.chat.project_delete":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "project_id is required"}
				break
			}
			if err := store.DeleteProject(projectIDReq); err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "project delete failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		case "app.chat.thread_list":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "project_id is required"}
				break
			}
			list, err := store.ListThreadsForProject(effectiveCaller.UserID, projectIDReq)
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "thread list failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"items": list, "count": len(list)}
		case "app.chat.thread_create":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "project_id is required"}
				break
			}
			threadIDNew, err := store.CreateThread(effectiveCaller.UserID, projectIDReq, asString(req.Args["title"]))
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "thread create failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"thread_id": threadIDNew}
		case "app.chat.thread_update":
			threadIDReq := asString(req.Args["thread_id"])
			if threadIDReq == "" {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "thread_id is required"}
				break
			}
			if err := store.UpdateThread(threadIDReq, asString(req.Args["title"]), asInt(req.Args["order_index"], 0), asString(req.Args["project_id"])); err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "thread update failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		case "app.chat.thread_delete":
			threadIDReq := asString(req.Args["thread_id"])
			if threadIDReq == "" {
				resp.Error = &app.ToolError{Code: app.ErrorCodeBadRequest, Message: "thread_id is required"}
				break
			}
			if err := store.DeleteThread(threadIDReq); err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "thread delete failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
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

	upgrader := websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			host := r.Host
			return strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost")
		},
	}
	openSessionWS := func(w http.ResponseWriter, r *http.Request, claims forwardedClaims) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			app.Errorf("chat_server ws upgrade failed: %v", err)
			return
		}
		q := r.URL.Query()
		pID := q.Get("project_id")
		if pID == "" {
			pID = *projectID
		}
		tID := q.Get("thread_id")
		if tID == "" {
			tID = *threadID
		}
		// The HTTP request context is canceled soon after websocket upgrade.
		// Session-scoped storage must inherit delegation, but outlive the upgrade request.
		wsCtx := hubsvc.ContextWithDelegation(appCtx, hubsvc.OriginCallerFromHeaders(r.Header), hubsvc.OriginCallerTokenFromHeaders(r.Header))
		effectiveUserID := strings.TrimSpace(claims.UserID)
		if originUserID := strings.TrimSpace(hubsvc.OriginCallerFromHeaders(r.Header).UserID); originUserID != "" {
			effectiveUserID = originUserID
		}
		store, err := openSessionStore(wsCtx, effectiveUserID, pID, tID)
		if err != nil {
			app.Errorf("chat_server ws user store failed: %v", err)
			_ = conn.Close()
			return
		}
		providerFactory := app.NewHubProviderFactory(hubBaseURL, serviceBootstrap)
		s := app.NewSession(conn, cfg, runtimeCfg, store, providerFactory)
		go func() {
			ctx, cancel := context.WithCancel(appCtx)
			defer cancel()
			defer store.Close()
			if err := s.Run(ctx); err != nil {
				app.Errorf("chat_server session ended with error: %v", err)
			}
		}()
	}
	mux.HandleFunc("/service/tool/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, manifest.ServiceID, instance, serviceBootstrap.H2SToken); err != nil {
			http.Error(w, "invalid hub auth", http.StatusForbidden)
			return
		}
		toolID := strings.TrimSpace(r.URL.Query().Get("tool_id"))
		if toolID != "" && toolID != "app.chat.stream" {
			http.Error(w, "tool not found", http.StatusNotFound)
			return
		}
		userID := strings.TrimSpace(r.Header.Get("X-Caller-User-Id"))
		if userID == "" {
			userID = strings.TrimSpace(r.Header.Get(hubsvc.HeaderOriginCallerUserID))
		}
		if userID == "" {
			userID = strings.TrimSpace(r.Header.Get("X-User-ID"))
		}
		if userID == "" {
			http.Error(w, "missing caller user", http.StatusUnauthorized)
			return
		}
		claims := forwardedClaims{UserID: userID}
		openSessionWS(w, r, claims)
	})

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	app.Infof("chat_server listening at http://%s", *addr)
	ln, err := hubsvc.Listen(*addr)
	if err != nil {
		app.Errorf("chat_server listen failed: %v", err)
		os.Exit(1)
	}
	startedAtMS := time.Now().UnixMilli()
	if err := hubsvc.RecordCurrentServiceProcess(processStorePath, manifest.ServiceID, startedAtMS); err != nil {
		app.Errorf("record current process failed: %v", err)
		os.Exit(1)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(ln)
	}()
	if hubToolCallURL != "" {
		healthy := true
		registerPayload := app.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: app.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools:   toSupervisorTools(manifest),
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
			app.Errorf("register chat_server to hub failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if statusCode >= 300 {
			app.Errorf("register chat_server to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
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
		app.Infof("register chat_server to hub status=%d", statusCode)
		startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	if err := <-serveErrCh; err != nil && err != http.ErrServerClosed {
		app.Errorf("chat_server failed: %v", err)
		os.Exit(1)
	}
}

func toSupervisorTools(manifest app.ServiceManifest) []app.ServiceTool {
	tools := make([]app.ServiceTool, 0, len(manifest.Provides))
	for _, descriptor := range manifest.Provides {
		toolID := strings.TrimSpace(descriptor.ToolID)
		if toolID == "" {
			continue
		}
		tools = append(tools, app.ServiceTool{
			ToolID:      toolID,
			Description: strings.TrimSpace(descriptor.Description),
			Protocol: firstNonEmpty(strings.TrimSpace(descriptor.Protocol), func() string {
				if descriptor.Streaming {
					return "ws"
				}
				return "http"
			}()),
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            descriptor.Streaming,
			StreamingMode:        strings.TrimSpace(descriptor.StreamingMode),
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

func structToMap(v any) map[string]any {
	raw, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func firstNonEmpty(items ...string) string {
	for _, it := range items {
		clean := strings.TrimSpace(it)
		if clean != "" {
			return clean
		}
	}
	return ""
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

func asConfigMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return nil, false
	}
	return m, true
}

func asInt(v any, defaultValue int) int {
	switch tv := v.(type) {
	case int:
		return tv
	case int32:
		return int(tv)
	case int64:
		return int(tv)
	case float32:
		return int(tv)
	case float64:
		return int(tv)
	case json.Number:
		i, err := tv.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		var parsed int
		_, err := fmt.Sscan(strings.TrimSpace(tv), &parsed)
		if err == nil {
			return parsed
		}
	}
	return defaultValue
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
