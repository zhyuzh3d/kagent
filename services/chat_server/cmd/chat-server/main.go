package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	configPath := flag.String("config", "config/configx.json", "path to private config json")
	publicConfigPath := flag.String("public-config", "config/config.json", "path to public config json")
	userConfigPath := flag.String("user-config", "run/user_custom_config.json", "path to user custom config json")
	projectID := flag.String("project-id", "project-default", "runtime project id")
	threadID := flag.String("thread-id", "chat-default", "runtime thread id")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18082", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "CHAT-SERVER")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	publicConfigPathResolved := app.ResolvePathFromRoot(appRoot, *publicConfigPath)
	userConfigPathResolved := app.ResolvePathFromRoot(appRoot, *userConfigPath)
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
	runtimeCfg, err := app.NewRuntimeConfigManager(publicConfigPathResolved, userConfigPathResolved)
	if err != nil {
		app.Errorf("load runtime config failed: %v", err)
		os.Exit(1)
	}
	hubBaseURL := strings.TrimSpace(cfg.EffectiveAIService().BaseURL)
	if hubBaseURL == "" {
		app.Errorf("chat-server requires ai_service.baseUrl as hub url")
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
		instance = "chat-server-" + app.NewRequestID()
	}

	hubToolCallURL := buildHubToolCallURL(registerURL)
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
			app.Errorf("register chat-server to hub failed: %v", err)
			os.Exit(1)
		}
		if statusCode >= 300 {
			app.Errorf("register chat-server to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register chat-server to hub status=%d", statusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("chat-server shutdown: %s", strings.TrimSpace(reason))
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

	openStore := func(uid string, pid string, tid string) (app.ChatStore, error) {
		client := app.NewHubToolClient(hubBaseURL, serviceBootstrap, 70*time.Second)
		return app.NewHubDatabaseStore(client, uid, firstNonEmpty(pid, *projectID), firstNonEmpty(tid, *threadID))
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
		caller := app.Caller{
			Type:      strings.ToLower(strings.TrimSpace(r.Header.Get("X-Caller-Type"))),
			UserID:    strings.TrimSpace(r.Header.Get("X-Caller-User-Id")),
			ServiceID: strings.TrimSpace(r.Header.Get("X-Caller-Service-Id")),
			SurfaceID: strings.TrimSpace(r.Header.Get("X-Caller-Surface-Id")),
		}
		if caller.Type == "" {
			caller = req.Context.Caller
		}
		if req.ToolID == "service.lifecycle.health" {
			writeToolResponse(w, http.StatusOK, app.CallResponse{
				Ok: true,
				Result: map[string]any{
					"service_id": strings.TrimSpace(manifest.ServiceID),
					"version":    strings.TrimSpace(manifest.Version),
					"ok":         true,
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
		if strings.TrimSpace(caller.UserID) == "" {
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
		store, err := openStore(caller.UserID, asString(req.Args["project_id"]), asString(req.Args["thread_id"]))
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
		case "app.chat.project_list":
			list, err := store.ListProjectsForUser(caller.UserID)
			if err != nil {
				resp.Error = &app.ToolError{Code: app.ErrorCodeToolExecError, Message: "project list failed: " + err.Error()}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"items": list, "count": len(list)}
		case "app.chat.project_create":
			projectIDNew, err := store.CreateProject(caller.UserID, asString(req.Args["title"]))
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
			list, err := store.ListThreadsForProject(caller.UserID, projectIDReq)
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
			threadIDNew, err := store.CreateThread(caller.UserID, projectIDReq, asString(req.Args["title"]))
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
			app.Errorf("chat-server ws upgrade failed: %v", err)
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
		store, err := openStore(claims.UserID, pID, tID)
		if err != nil {
			app.Errorf("chat-server ws user store failed: %v", err)
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
				app.Errorf("chat-server session ended with error: %v", err)
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
	if hubToolCallURL != "" {
		startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	app.Infof("chat-server listening at http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("chat-server failed: %v", err)
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
		streaming := strings.TrimSpace(descriptor.Streaming)
		isStreaming := streaming != "" && !strings.EqualFold(streaming, "none")
		tools = append(tools, app.ServiceTool{
			ToolID:               toolID,
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            isStreaming,
			WSPath:               strings.TrimSpace(descriptor.WSPath),
			TimeoutMS:            descriptor.TimeoutMSDefault,
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
	raw := strings.TrimSpace(registerURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/tool/call"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, strings.TrimSpace(hubToolCallURL), bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(httpReq.Header, serviceAuth)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return rawResp, resp.StatusCode, nil
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
