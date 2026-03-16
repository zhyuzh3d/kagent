package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/chat-server/internal/app"

	"github.com/gorilla/websocket"
)

type forwardedClaims struct {
	UserID   string
	Username string
}

func main() {
	configPath := flag.String("config", "services/chat-server/config/configx.json", "path to private config json")
	publicConfigPath := flag.String("public-config", "config/config.json", "path to public config json")
	userConfigPath := flag.String("user-config", "data/users/default/user_custom_config.json", "path to user custom config json")
	sqlitePath := flag.String("sqlite-path", "data/kagent.db", "path to sqlite message store")
	userID := flag.String("user-id", "default", "runtime user id")
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
	sqlitePathResolved := app.ResolvePathFromRoot(appRoot, *sqlitePath)
	dataRoot := filepath.Join(appRoot, "data")
	serviceSecret, err := hubsvc.LoadServiceSecret(filepath.Join(dataRoot, ".service_secret"))
	if err != nil {
		app.Errorf("load service secret failed: %v", err)
		os.Exit(1)
	}

	cfg, err := app.LoadModelConfig(configPathResolved, *modelName)
	if err != nil {
		app.Errorf("load config failed: %v", err)
		os.Exit(1)
	}
	aiServiceCfg := cfg.EffectiveAIService()
	if !app.IsServiceMode(cfg) {
		app.Errorf("chat-server requires ai_service.mode=service")
		os.Exit(1)
	}
	runtimeCfg, err := app.NewRuntimeConfigManager(publicConfigPathResolved, userConfigPathResolved)
	if err != nil {
		app.Errorf("load runtime config failed: %v", err)
		os.Exit(1)
	}
	baseStore, err := app.NewSQLiteStore(sqlitePathResolved, *userID, *projectID, *threadID)
	if err != nil {
		app.Errorf("init sqlite store failed: %v", err)
		os.Exit(1)
	}
	defer baseStore.Close()

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	aiServiceManager := app.NewAIServiceManager(aiServiceCfg)
	if err := aiServiceManager.Start(appCtx); err != nil {
		app.Errorf("ai service manager start failed: %v", err)
		os.Exit(1)
	}
	healthy := aiServiceManager.WaitForHealthy(appCtx, time.Duration(aiServiceCfg.StartupGracePeriodMS)*time.Millisecond)
	if !healthy {
		app.Errorf("ai service startup health check timeout: %s", aiServiceCfg.BaseURL)
		os.Exit(1)
	}
	defer aiServiceManager.Stop()
	providerFactory := app.NewServiceProviderFactory(aiServiceCfg, aiServiceManager)
	manifest := app.ChatServerServiceManifest()
	instance := strings.TrimSpace(*instanceID)
	if instance == "" {
		instance = "chat-server-" + app.NewRequestID()
	}

	if strings.TrimSpace(*hubRegisterURL) != "" {
		registerPayload := toolproto.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: toolproto.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools: toSupervisorTools(manifest),
		}
		raw, _ := json.Marshal(registerPayload)
		req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(*hubRegisterURL), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			app.Errorf("register chat-server to hub failed: %v", err)
			os.Exit(1)
		}
		rawResp, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register chat-server to hub status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		app.Infof("register chat-server to hub status=%d", resp.StatusCode)
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"ok":           true,
			"timestamp_ms": time.Now().UnixMilli(),
		})
	})
	mux.HandleFunc("/service/info", func(w http.ResponseWriter, _ *http.Request) {
		caps := make([]string, 0, len(manifest.Provides))
		for _, p := range manifest.Provides {
			if strings.TrimSpace(p.ToolID) != "" {
				caps = append(caps, p.ToolID)
			}
		}
		writeJSON(w, app.AIServiceInfo{
			ServiceID:    manifest.ServiceID,
			ServiceName:  manifest.ServiceName,
			Version:      manifest.Version,
			Provider:     "chat-app",
			Capabilities: caps,
			Transport:    "http+ws",
		})
	})
	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		tools := make([]app.AIServiceToolDescriptor, 0, len(manifest.Provides))
		for _, p := range manifest.Provides {
			tools = append(tools, app.AIServiceToolDescriptor{
				Name:             p.ToolID,
				Description:      p.Description,
				InputSchema:      p.InputSchema,
				OutputSchema:     p.OutputSchema,
				SideEffect:       p.SideEffect,
				TimeoutMSDefault: p.TimeoutMSDefault,
				Streaming:        p.Streaming,
			})
		}
		writeJSON(w, app.AIServiceListToolsResponse{
			ServiceID: manifest.ServiceID,
			Tools:     tools,
		})
	})

	openStore := func(uid string, pid string, tid string) (*app.SQLiteStore, error) {
		return app.NewSQLiteStore(sqlitePathResolved, uid, firstNonEmpty(pid, *projectID), firstNonEmpty(tid, *threadID))
	}

	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeToolResponse(w, http.StatusMethodNotAllowed, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "method not allowed",
				},
			})
			return
		}
		var req toolproto.CallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "invalid request body",
				},
			})
			return
		}
		req, err := toolproto.NormalizeRequest(req)
		if err != nil {
			writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: err.Error(),
				},
			})
			return
		}
		tokenClaims, err := hubsvc.VerifyServiceSessionTokenLoose(strings.TrimSpace(r.Header.Get("X-Hub-Service-Token")), serviceSecret)
		if err != nil || tokenClaims.ServiceID != strings.TrimSpace(manifest.ServiceID) {
			writeToolResponse(w, http.StatusUnauthorized, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeUnauthorized,
					Message: "invalid hub service token",
				},
				Meta: toolproto.Meta{
					RequestID: strings.TrimSpace(req.Context.RequestID),
					TraceID:   strings.TrimSpace(req.Context.TraceID),
				},
			})
			return
		}
		caller := toolproto.Caller{
			Type:      strings.ToLower(strings.TrimSpace(r.Header.Get("X-Caller-Type"))),
			UserID:    strings.TrimSpace(r.Header.Get("X-Caller-User-Id")),
			ServiceID: strings.TrimSpace(r.Header.Get("X-Caller-Service-Id")),
			SurfaceID: strings.TrimSpace(r.Header.Get("X-Caller-Surface-Id")),
		}
		if caller.Type == "" {
			caller = req.Context.Caller
		}
		if strings.TrimSpace(caller.UserID) == "" {
			writeToolResponse(w, http.StatusUnauthorized, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeUnauthorized,
					Message: "missing caller user",
				},
				Meta: toolproto.Meta{
					RequestID: strings.TrimSpace(req.Context.RequestID),
					TraceID:   strings.TrimSpace(req.Context.TraceID),
				},
			})
			return
		}
		startedAt := time.Now()
		meta := toolproto.Meta{
			RequestID:  strings.TrimSpace(req.Context.RequestID),
			TraceID:    strings.TrimSpace(req.Context.TraceID),
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
		}
		resp := toolproto.CallResponse{
			Ok:     false,
			Result: nil,
			Error:  nil,
			Meta:   meta,
		}
		store, err := openStore(caller.UserID, asString(req.Args["project_id"]), asString(req.Args["thread_id"]))
		if err != nil {
			resp.Error = &toolproto.Error{
				Code:    toolproto.ErrorCodeInternalError,
				Message: "open store failed: " + err.Error(),
			}
			resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
			writeToolResponse(w, toolproto.HTTPStatusFromCode(resp.Error.Code), resp)
			return
		}
		defer store.Close()

		switch req.ToolID {
		case "app.chat.project_list":
			list, err := store.ListProjectsForUser(caller.UserID)
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "project list failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"items": list, "count": len(list)}
		case "app.chat.project_create":
			projectIDNew, err := store.CreateProject(caller.UserID, asString(req.Args["title"]))
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "project create failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"project_id": projectIDNew}
		case "app.chat.project_update":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "project_id is required",
				}
				break
			}
			err := store.UpdateProject(projectIDReq, asString(req.Args["title"]), asInt(req.Args["order_index"], 0))
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "project update failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		case "app.chat.project_delete":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "project_id is required",
				}
				break
			}
			err := store.DeleteProject(projectIDReq)
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "project delete failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		case "app.chat.thread_list":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "project_id is required",
				}
				break
			}
			list, err := store.ListThreadsForProject(caller.UserID, projectIDReq)
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "thread list failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"items": list, "count": len(list)}
		case "app.chat.thread_create":
			projectIDReq := asString(req.Args["project_id"])
			if projectIDReq == "" {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "project_id is required",
				}
				break
			}
			threadIDNew, err := store.CreateThread(caller.UserID, projectIDReq, asString(req.Args["title"]))
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "thread create failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"thread_id": threadIDNew}
		case "app.chat.thread_update":
			threadIDReq := asString(req.Args["thread_id"])
			if threadIDReq == "" {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "thread_id is required",
				}
				break
			}
			err := store.UpdateThread(threadIDReq, asString(req.Args["title"]), asInt(req.Args["order_index"], 0), asString(req.Args["project_id"]))
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "thread update failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		case "app.chat.thread_delete":
			threadIDReq := asString(req.Args["thread_id"])
			if threadIDReq == "" {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "thread_id is required",
				}
				break
			}
			err := store.DeleteThread(threadIDReq)
			if err != nil {
				resp.Error = &toolproto.Error{
					Code:    toolproto.ErrorCodeToolExecError,
					Message: "thread delete failed: " + err.Error(),
				}
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true}
		default:
			resp.Error = &toolproto.Error{
				Code:    toolproto.ErrorCodeToolNotFound,
				Message: "tool not found",
			}
		}

		resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
		statusCode := http.StatusOK
		if !resp.Ok && resp.Error != nil {
			statusCode = toolproto.HTTPStatusFromCode(resp.Error.Code)
		}
		writeToolResponse(w, statusCode, resp)
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

		app.Warnf("chat-server shutdown requested from %s", r.RemoteAddr)
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
		userStore, err := openStore(claims.UserID, pID, tID)
		if err != nil {
			app.Errorf("chat-server ws user store failed: %v", err)
			conn.Close()
			return
		}
		s := app.NewSession(conn, cfg, runtimeCfg, userStore, providerFactory)
		go func() {
			ctx, cancel := context.WithCancel(appCtx)
			defer cancel()
			defer userStore.Close()
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
		tokenClaims, err := hubsvc.VerifyServiceSessionTokenLoose(strings.TrimSpace(r.Header.Get("X-Hub-Service-Token")), serviceSecret)
		if err != nil || tokenClaims.ServiceID != strings.TrimSpace(manifest.ServiceID) {
			http.Error(w, "invalid hub service token", http.StatusUnauthorized)
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
		claims := forwardedClaims{
			UserID:   userID,
			Username: strings.TrimSpace(r.Header.Get("X-Username")),
		}
		openSessionWS(w, r, claims)
	})

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if hbURL := buildHubHeartbeatURL(strings.TrimSpace(*hubRegisterURL)); hbURL != "" {
		startHubHeartbeatGuard(hbURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), shutdownNow)
	}
	app.Infof("chat-server listening at http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("chat-server failed: %v", err)
		os.Exit(1)
	}
}

func toSupervisorTools(manifest app.ServiceManifest) []toolproto.ServiceTool {
	tools := make([]toolproto.ServiceTool, 0, len(manifest.Provides))
	for _, descriptor := range manifest.Provides {
		toolID := strings.TrimSpace(descriptor.ToolID)
		if toolID == "" {
			continue
		}
		tools = append(tools, toolproto.ServiceTool{
			ToolID:               toolID,
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
		})
	}
	return tools
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeToolResponse(w http.ResponseWriter, statusCode int, resp toolproto.CallResponse) {
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
		parsed, err := strconv.Atoi(strings.TrimSpace(tv))
		if err == nil {
			return parsed
		}
	}
	return defaultValue
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
