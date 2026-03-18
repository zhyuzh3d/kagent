package main

import (
	"bufio"
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
	"syscall"
	"time"

	app "kagent/hub/internal/app"
	hubgateway "kagent/hub/internal/gateway"
	"kagent/hub/internal/observability"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/supervisor"
	"kagent/hub/internal/transport"
	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

type serviceBindRequest struct {
	ToolID    string `json:"tool_id"`
	ServiceID string `json:"service_id"`
}

type responseObserver struct {
	http.ResponseWriter
	status int
}

func (o *responseObserver) WriteHeader(code int) {
	o.status = code
	o.ResponseWriter.WriteHeader(code)
}

func (o *responseObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := o.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijack not supported by underlying ResponseWriter")
	}
	// WebSocket upgrade paths typically hijack the connection and may bypass WriteHeader.
	// Preserve accurate access-log status for successful upgrades.
	if o.status == http.StatusOK {
		o.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}

func (o *responseObserver) Flush() {
	if f, ok := o.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func main() {
	publicConfigPath := flag.String("public-config", "config/config.json", "path to public config json")
	userConfigPath := flag.String("user-config", "data/users/default/user_custom_config.json", "path to user custom config json")
	sqlitePath := flag.String("sqlite-path", "data/hub/users.db", "path to sqlite auth user store")
	servicesConfigPath := flag.String("services-config", "config/services.json", "path to hub managed services lifecycle config")
	addr := flag.String("addr", "127.0.0.1:18080", "listen addr")
	chatServiceURL := flag.String("chat-service-url", "http://127.0.0.1:18082", "chat service base url")
	fileServiceURL := flag.String("file-service-url", "http://127.0.0.1:18084", "file service base url")
	databaseServiceURL := flag.String("database-service-url", "http://127.0.0.1:18085", "database service base url")
	surfaceManagerURL := flag.String("surface-manager-url", "http://127.0.0.1:18086", "surface-manager service base url")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "HUB")

	// Ensure port is available before starting (Self-Cleaning Startup)
	if err := app.EnsurePortReady(*addr); err != nil {
		app.Warnf("System:Internal:Startup:PortPreemptError: %v", err)
	}

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	publicConfigPathResolved := app.ResolvePathFromRoot(appRoot, *publicConfigPath)
	userConfigPathResolved := app.ResolvePathFromRoot(appRoot, *userConfigPath)
	sqlitePathResolved := app.ResolvePathFromRoot(appRoot, *sqlitePath)
	servicesConfigPathResolved := app.ResolvePathFromRoot(appRoot, *servicesConfigPath)
	dataRoot := filepath.Join(appRoot, "data")
	webuiRoot := filepath.Join(appRoot, "webui")
	versionPath := filepath.Join(appRoot, "version.json")

	runtimeCfg, err := app.NewRuntimeConfigManager(publicConfigPathResolved, userConfigPathResolved)
	if err != nil {
		app.Errorf("RuntimeConfig-Init-Error:%v", err)
		os.Exit(1)
	}
	userStore, err := app.NewUserStore(sqlitePathResolved)
	if err != nil {
		app.Errorf("UserStore-Init-Error:%v", err)
		os.Exit(1)
	}
	defer userStore.Close()
	startupSnapshotStore, snapshotErr := app.NewStartupSnapshotStore(sqlitePathResolved)
	if snapshotErr != nil {
		app.Warnf("StartupSnapshotStore-Init-Error:%v", snapshotErr)
	}
	if startupSnapshotStore != nil {
		defer startupSnapshotStore.Close()
	}

	authService, err := app.NewAuthService(dataRoot)
	if err != nil {
		app.Errorf("AuthService-Init-Error:%v", err)
		os.Exit(1)
	}
	hubPlatform, err := app.NewHubPlatform(dataRoot)
	if err != nil {
		app.Errorf("HubPlatform-Init-Error:%v", err)
		os.Exit(1)
	}
	servicesRoot := filepath.Join(appRoot, "services")
	for _, sid := range []string{"chat-server", "ai-doubao", "file", "database", "surface-manager"} {
		if err := app.EnsureServiceConfigFiles(filepath.Join(servicesRoot, sid)); err != nil {
			app.Warnf("ServiceConfigFile-Ensure-Error:%s-%v", sid, err)
		}
	}
	_, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	ver, verr := app.LoadVersionInfo(versionPath)
	if verr != nil {
		app.Warnf("VersionFile-Read-Error:%v", verr)
		ver = &app.VersionInfo{Format: "calver-yymmddnn", Backend: "unknown", WebUI: "unknown"}
	}
	// Category 1: System:Internal:
	app.Infof("System:Internal:Version:backend=%s,webui=%s", ver.Backend, ver.WebUI)

	mux := http.NewServeMux()
	var server *http.Server
	var lifecycleManager *supervisor.LifecycleManager
	supervisorRegistry := supervisor.NewRegistry()
	routingEngine := routing.NewEngine()
	auditStore := observability.NewStore(3000)
	transportClient := transport.NewClient(true)
	toolHandler := hubgateway.NewToolHandler(
		authService,
		hubPlatform,
		routingEngine,
		supervisorRegistry,
		transportClient,
		auditStore,
		map[string]transport.Endpoint{
			"chat-server": {
				Transport: "tcp",
				TCPURL:    strings.TrimSpace(*chatServiceURL),
			},
			"file": {
				Transport: "tcp",
				TCPURL:    strings.TrimSpace(*fileServiceURL),
			},
			"database": {
				Transport: "tcp",
				TCPURL:    strings.TrimSpace(*databaseServiceURL),
			},
			"surface-manager": {
				Transport: "tcp",
				TCPURL:    strings.TrimSpace(*surfaceManagerURL),
			},
		},
	)
	if cfg, cfgErr := supervisor.LoadLifecycleConfig(servicesConfigPathResolved); cfgErr != nil {
		app.Warnf("ServiceLifecycle-Config-Load-Error:%v", cfgErr)
	} else {
		registerURL := "http://" + strings.TrimSpace(*addr) + "/api/service/register"
		manager, managerErr := supervisor.NewLifecycleManager(appRoot, registerURL, cfg, hubPlatform, supervisorRegistry)
		if managerErr != nil {
			app.Warnf("ServiceLifecycle-Init-Error:%v", managerErr)
		} else {
			lifecycleManager = manager
		}
	}

	// Request logging middleware
	loggingMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// Selective silence for high-frequency or internal setup noise
		if strings.HasPrefix(path, "/api/service/") || strings.HasSuffix(path, "/debug/log") {
			mux.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		observer := &responseObserver{ResponseWriter: w, status: http.StatusOK}

		extra := ""
		if path == "/api/tool/call" && r.Method == http.MethodPost {
			// Peek at body to extract tool_id
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			var payload struct {
				ToolID string `json:"tool_id"`
			}
			if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.ToolID != "" {
				extra = fmt.Sprintf(" [%s]", payload.ToolID)
			}
		}

		mux.ServeHTTP(observer, r)

		// Audit logs for all incoming requests - dynamically tagged based on identity
		identity := app.IdentityFromContext(r.Context())
		tag := "HUB"
		switch identity.Type {
		case app.IdentityService:
			tag = strings.ToUpper(identity.Name)
		case app.IdentityUser:
			tag = "PAGE"
		case app.IdentitySurface:
			tag = "SURF"
		}

		target := path
		if extra != "" {
			target = strings.Trim(extra, " []")
		} else {
			target = fmt.Sprintf("%s %s", r.Method, path)
		}

		// Category 2: System:HTTP:
		// Format: System:HTTP:<Method> <Path> [<Status>] (<Duration>)
		msg := fmt.Sprintf("System:HTTP:%s [%d] (%v)", target, observer.status, time.Since(start))
		app.InfofCtxTag(r.Context(), tag, "%s", msg)
	})

	mux.HandleFunc("/api/debug/log", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Level   string `json:"level"`
			Module  string `json:"module"`
			Content string `json:"content"`
			Source  string `json:"source"`
			Page    string `json:"page"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		identity := app.IdentityFromContext(r.Context())
		tag := "PAGE"
		switch identity.Type {
		case app.IdentityService:
			tag = strings.ToUpper(identity.Name)
		case app.IdentitySurface:
			tag = "SURF"
		case app.IdentityUser:
			tag = "PAGE"
		default:
			tag = "HUB"
		}

		page := strings.TrimSpace(body.Page)
		if identity.Type == app.IdentitySurface {
			// For surface logs, ensure the page name is the surface name for clarity
			page = identity.Name
		}
		if page == "" {
			page = strings.ToLower(tag)
		}

		// Category 3: Service:Report:
		app.InfofCtxTag(r.Context(), tag, "Service:Report:%s:%s:%s", page, body.Module, strings.ReplaceAll(body.Content, ": ", ":"))
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, ver)
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(runtimeCfg.EffectiveMap())
		case http.MethodPut:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid config payload", http.StatusBadRequest)
				return
			}
			effective, err := runtimeCfg.UpdateEffectiveMap(body)
			if err != nil {
				app.Errorf("update runtime config failed: %v", err)
				http.Error(w, "update runtime config failed", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(effective)
		default:
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/tool/call", toolHandler.HandleCall)
	mux.HandleFunc("/api/tool/ws", toolHandler.HandleWS)

	mux.HandleFunc("/api/internal/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{
			"ok":           true,
			"timestamp_ms": time.Now().UnixMilli(),
		})
	})

	mux.HandleFunc("/api/service/prepare-start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req toolproto.SupervisorPrepareStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		sid := strings.TrimSpace(req.ServiceID)
		if sid == "" {
			http.Error(w, "service_id is required", http.StatusBadRequest)
			return
		}
		prev, had, err := ensureServiceStoppedForRegister(hubPlatform, sid, strings.TrimSpace(req.InstanceID), 7*time.Second)
		if err != nil {
			writeJSON(w, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeConflict,
					Message: "prepare-start failed: " + err.Error(),
				},
			})
			return
		}
		if had && strings.TrimSpace(prev.InstanceID) != "" {
			supervisorRegistry.Unregister(sid, prev.InstanceID)
			routingEngine.SyncServices(hubPlatform.ListRegisteredServices())
		}
		writeJSON(w, toolproto.CallResponse{
			Ok: true,
			Result: toolproto.SupervisorPrepareStartResult{
				Prepared: true,
				Endpoint: toolproto.Endpoint{
					TCPURL: strings.TrimSpace(prev.Endpoint),
				},
			},
			Meta: toolproto.Meta{
				ServiceID:  sid,
				InstanceID: prev.InstanceID,
				DurationMS: 0,
			},
		})
		if had && strings.TrimSpace(prev.InstanceID) != "" {
			app.Infof("internal prepare-start cleaned previous service=%s instance=%s pid=%d", sid, prev.InstanceID, prev.PID)
		}
	})

	mux.HandleFunc("/api/service/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req toolproto.SupervisorRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		regReq, transportName, parseErr := decodeInternalRegister(req)
		if parseErr != nil {
			http.Error(w, "invalid register payload: "+parseErr.Error(), http.StatusBadRequest)
			return
		}
		if _, _, authErr := verifyServiceInternalAuth(hubPlatform, r, regReq.Manifest.ServiceID, regReq.InstanceID); authErr != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if sid := strings.TrimSpace(regReq.Manifest.ServiceID); sid != "" {
			prev, had, err := ensureServiceStoppedForRegister(hubPlatform, sid, regReq.InstanceID, 7*time.Second)
			if err != nil {
				writeJSON(w, toolproto.CallResponse{
					Ok: false,
					Error: &toolproto.Error{
						Code:    toolproto.ErrorCodeConflict,
						Message: "prepare-start failed: " + err.Error(),
					},
					Meta: toolproto.Meta{
						ServiceID:  sid,
						InstanceID: prev.InstanceID,
					},
				})
				return
			}
			if had && strings.TrimSpace(prev.InstanceID) != "" && strings.TrimSpace(prev.InstanceID) != strings.TrimSpace(regReq.InstanceID) {
				app.Warnf("internal register replaced previous service=%s instance=%s pid=%d endpoint=%s", sid, prev.InstanceID, prev.PID, prev.Endpoint)
			}
		}
		res, err := hubPlatform.RegisterService(regReq)
		if err != nil {
			auditStore.Add("supervisor", "register", "error", map[string]any{
				"service_id":  strings.TrimSpace(regReq.Manifest.ServiceID),
				"instance_id": strings.TrimSpace(regReq.InstanceID),
				"error":       err.Error(),
			})
			writeJSON(w, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeConflict,
					Message: err.Error(),
				},
				Meta: toolproto.Meta{
					ServiceID:  res.Service.ServiceID,
					InstanceID: res.Service.InstanceID,
				},
			})
			return
		}
		supervisorRegistry.UpsertFromServiceRegistration(res.Service, transportName, instanceStatusFromHealth(res.Service.Healthy))
		routingEngine.SyncServices(hubPlatform.ListRegisteredServices())
		app.Infof("System:Internal:RegistrationSuccess:service=%s,endpoint=%s", res.Service.ServiceID, res.Service.Endpoint)
		auditStore.Add("supervisor", "register", "ok", map[string]any{
			"service_id":  res.Service.ServiceID,
			"instance_id": res.Service.InstanceID,
			"endpoint":    res.Service.Endpoint,
		})
		writeJSON(w, toolproto.CallResponse{
			Ok: true,
			Result: toolproto.SupervisorRegisterResult{
				HeartbeatIntervalSec:           3,
				InverseHeartbeatIntervalSec:    3,
				InverseHeartbeatFailuresToExit: 2,
				DrainGracePeriodSec:            30,
			},
			Meta: toolproto.Meta{
				ServiceID:  res.Service.ServiceID,
				InstanceID: res.Service.InstanceID,
			},
		})
	})

	mux.HandleFunc("/api/service/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req toolproto.SupervisorHeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if _, _, authErr := verifyServiceInternalAuth(hubPlatform, r, req.ServiceID, req.InstanceID); authErr != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		reg, err := hubPlatform.AcceptServiceHeartbeat(app.HubServiceHeartbeatRequest{
			ServiceID:  strings.TrimSpace(req.ServiceID),
			InstanceID: strings.TrimSpace(req.InstanceID),
			PID:        req.PID,
			Endpoint:   strings.TrimSpace(req.Endpoint),
			Healthy:    req.Healthy,
		})
		if err != nil {
			auditStore.Add("supervisor", "heartbeat", "error", map[string]any{
				"service_id":  strings.TrimSpace(req.ServiceID),
				"instance_id": strings.TrimSpace(req.InstanceID),
				"error":       err.Error(),
			})
			writeJSON(w, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeConflict,
					Message: "heartbeat rejected: " + err.Error(),
				},
			})
			return
		}
		supervisorRegistry.Heartbeat(req.ServiceID, req.InstanceID, req.Status, req.Healthy)
		auditStore.Add("supervisor", "heartbeat", "ok", map[string]any{
			"service_id":  reg.ServiceID,
			"instance_id": reg.InstanceID,
			"status":      reg.Status,
		})
		writeJSON(w, toolproto.CallResponse{
			Ok: true,
			Result: map[string]any{
				"status":       reg.Status,
				"last_seen_ms": reg.LastSeenAtMS,
			},
			Meta: toolproto.Meta{
				ServiceID:  reg.ServiceID,
				InstanceID: reg.InstanceID,
			},
		})
	})

	mux.HandleFunc("/api/service/drain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req toolproto.SupervisorDrainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		sid := strings.TrimSpace(req.ServiceID)
		if sid == "" {
			http.Error(w, "service_id is required", http.StatusBadRequest)
			return
		}
		if _, _, authErr := verifyServiceInternalAuth(hubPlatform, r, sid, strings.TrimSpace(req.InstanceID)); authErr != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			reason = "drain requested"
		}
		hubPlatform.MarkServiceDown(sid, reason)
		supervisorRegistry.MarkDraining(sid, strings.TrimSpace(req.InstanceID))
		routingEngine.SyncServices(hubPlatform.ListRegisteredServices())
		auditStore.Add("supervisor", "drain", "ok", map[string]any{
			"service_id":  sid,
			"instance_id": strings.TrimSpace(req.InstanceID),
			"reason":      reason,
		})
		writeJSON(w, toolproto.CallResponse{
			Ok: true,
			Result: map[string]any{
				"draining": true,
			},
			Meta: toolproto.Meta{
				ServiceID: sid,
			},
		})
	})

	mux.HandleFunc("/api/service/unregister", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req toolproto.SupervisorUnregisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		sid := strings.TrimSpace(req.ServiceID)
		iid := strings.TrimSpace(req.InstanceID)
		if _, _, authErr := verifyServiceInternalAuth(hubPlatform, r, sid, iid); authErr != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		hubPlatform.UnregisterService(sid, iid)
		supervisorRegistry.Unregister(sid, iid)
		routingEngine.SyncServices(hubPlatform.ListRegisteredServices())
		auditStore.Add("supervisor", "unregister", "ok", map[string]any{
			"service_id":  sid,
			"instance_id": iid,
		})
		writeJSON(w, toolproto.CallResponse{
			Ok: true,
			Result: map[string]any{
				"unregistered": true,
			},
			Meta: toolproto.Meta{
				ServiceID:  sid,
				InstanceID: iid,
			},
		})
	})

	// ── Auth API ─────────────────────────────────────────────────
	mux.HandleFunc("/api/auth/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		username := strings.TrimSpace(body.Username)
		if username == "" {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名不能为空"})
			return
		}
		if len(body.Password) < app.PasswordMinLen {
			writeJSON(w, map[string]any{"ok": false, "error": "密码至少需要6位"})
			return
		}
		hash, err := app.HashPassword(body.Password)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "密码处理失败"})
			return
		}
			userID, err := userStore.CreateUser(username, hash)
			if err != nil {
				if app.IsUsernameAlreadyExists(err) {
					writeJSONStatus(w, http.StatusConflict, map[string]any{"ok": false, "error": "用户名已存在", "error_code": "USERNAME_EXISTS"})
					return
				}
				app.Errorf("register failed username=%s err=%v", username, err)
				writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "注册失败，请稍后重试", "error_code": "REGISTER_INTERNAL_ERROR"})
				return
			}
		token, err := authService.IssueJWT(userID, username)
		if err != nil {
			http.Error(w, "issue token failed", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     app.JWTCookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   app.JWTMaxAgeSec,
			SameSite: http.SameSiteStrictMode,
		})
		writeJSON(w, map[string]any{"ok": true, "user_id": userID, "username": username})
	})

	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		username := strings.TrimSpace(body.Username)
		if username == "" || body.Password == "" {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名和密码不能为空"})
			return
		}
		userID, storedHash, exists, err := userStore.GetUserByUsername(username)
			if err != nil {
				app.Errorf("login lookup failed username=%s err=%v", username, err)
				writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "登录失败，请稍后重试", "error_code": "LOGIN_INTERNAL_ERROR"})
				return
			}
		if !exists || !app.VerifyPassword(body.Password, storedHash) {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名或密码错误"})
			return
		}
		if app.NeedsPasswordRehash(storedHash) {
			if upgradedHash, err := app.HashPassword(body.Password); err == nil {
				if err := userStore.UpdateUserPasswordHash(userID, upgradedHash); err != nil {
					app.Warnf("upgrade password hash failed for user=%s: %v", userID, err)
				}
			}
		}
		token, err := authService.IssueJWT(userID, username)
		if err != nil {
			http.Error(w, "issue token failed", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     app.JWTCookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   app.JWTMaxAgeSec,
			SameSite: http.SameSiteStrictMode,
		})
		writeJSON(w, map[string]any{"ok": true, "user_id": userID, "username": username})
	})

	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:   app.JWTCookieName,
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
			claims, err := app.ExtractJWTClaims(r, authService)
			if err != nil {
				writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "未登录"})
				return
			}
			writeJSON(w, map[string]any{"ok": true, "user_id": claims.UserID, "username": claims.Username})
		})

	mux.HandleFunc("/api/admin/services", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"active_provider": "tool-routing-engine-v1",
			"services":        hubPlatform.ListServices(),
		})
	})

	mux.HandleFunc("/api/admin/services/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"active_provider": "tool-routing-engine-v1",
			"tools":           hubPlatform.ListTools(),
		})
	})

	mux.HandleFunc("/api/admin/services/routes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		auditLimit := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("audit_limit")); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 && parsed <= 1000 {
				auditLimit = parsed
			}
		}
		services := hubPlatform.ListRegisteredServices()
		instances := supervisorRegistry.List()
		writeJSON(w, map[string]any{
			"bindings":       hubPlatform.ListBindings(),
			"routing_schema": routingEngine.BuildMetadataSchema(services, instances, auditLimit),
		})
	})

	mux.HandleFunc("/api/admin/services/instances", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"items": supervisorRegistry.List(),
		})
	})

	mux.HandleFunc("/api/admin/services/audits", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		limit := 100
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 && parsed <= 1000 {
				limit = parsed
			}
		}
		writeJSON(w, map[string]any{
			"tool_call_audits": routingEngine.ListAudits(limit),
			"events":           auditStore.List(limit),
		})
	})

	mux.HandleFunc("/api/admin/services/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"ok":       true,
			"bindings": hubPlatform.RefreshBindings("manual_refresh"),
		})
		routingEngine.SyncServices(hubPlatform.ListRegisteredServices())
		auditStore.Add("routing", "refresh", "ok", nil)
	})

	mux.HandleFunc("/api/admin/services/bind", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req serviceBindRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := hubPlatform.SetManualBinding(req.ToolID, req.ServiceID); err != nil {
			http.Error(w, "bind failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		routingEngine.SetManualBinding(req.ToolID, req.ServiceID)
		routingEngine.SyncServices(hubPlatform.ListRegisteredServices())
		auditStore.Add("routing", "manual_bind", "ok", map[string]any{
			"tool_id":    strings.TrimSpace(req.ToolID),
			"service_id": strings.TrimSpace(req.ServiceID),
		})
		writeJSON(w, map[string]any{
			"ok":       true,
			"bindings": hubPlatform.ListBindings(),
		})
	})

	mux.HandleFunc("/api/admin/services/tool-probe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := app.ExtractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			ServiceID string         `json:"service_id"`
			ToolID    string         `json:"tool_id"`
			Args      map[string]any `json:"args,omitempty"`
			TimeoutMS int            `json:"timeout_ms,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		callResp, statusCode, probeErr := toolHandler.ProbeServiceTool(r.Context(), req.ServiceID, req.ToolID, req.Args, req.TimeoutMS)
		if probeErr != nil {
			writeJSONStatus(w, http.StatusServiceUnavailable, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeServiceUnavailable,
					Message: probeErr.Error(),
				},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(callResp)
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

		app.Warnf("shutdown requested from %s", r.RemoteAddr)
		writeJSON(w, map[string]any{
			"ok":      true,
			"message": "shutting down",
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		go func() {
			time.Sleep(20 * time.Millisecond)
			if lifecycleManager != nil {
				lifecycleManager.StopAll(7 * time.Second)
			}
			broadcastServiceShutdown(hubPlatform, 7*time.Second)
			appCancel()
			if server != nil {
				_ = server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				_ = server.Shutdown(ctx)
				cancel()
			}
			time.Sleep(80 * time.Millisecond)
			os.Exit(0)
		}()
	})

	staticFS := http.FileServer(http.Dir(webuiRoot))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/page/chat/", http.StatusFound)
			return
		}
		staticFS.ServeHTTP(w, r)
	})

	mux.HandleFunc("/api/system/smoke-test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		tester := app.NewSmokeTester(*addr, authService)
		result, err := tester.Run(r.Context())
		if err != nil {
			app.Errorf("System:Internal:SmokeTest:Failed: %v", err)
			writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if !result.Ok {
			app.Warnf("System:Internal:SmokeTest:Failed: %s", result.Message)
			writeJSONStatus(w, http.StatusExpectationFailed, result)
			return
		}
		app.Infof("System:Internal:SmokeTest:Success: %s", result.Message)
		writeJSON(w, result)
	})

	identityMw := app.IdentityMiddleware(authService, hubPlatform)
	server = &http.Server{
		Addr:              *addr,
		Handler:           identityMw(loggingMux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	app.Infof("System:Internal:Listening:http://%s,root=%s", *addr, appRoot)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.ListenAndServe()
	}()

	if lifecycleManager != nil {
		go func() {
			time.Sleep(180 * time.Millisecond)
			startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			snapshot := lifecycleManager.StartAll(startCtx)
			if startupSnapshotStore != nil {
				payload := map[string]any{
					"hub_version":     strings.TrimSpace(ver.Backend),
					"started_at_ms":   snapshot.StartedAtMS,
					"completed_at_ms": snapshot.CompletedAtMS,
					"services":        snapshot.Services,
				}
				if err := startupSnapshotStore.Save(strings.TrimSpace(ver.Backend), payload); err != nil {
					app.Warnf("ServiceLifecycle-Snapshot-Save-Error:%v", err)
				}
			}

			// Automatically trigger internal smoke tests after startup
			app.Infof("System:Internal:AutoSmokeTest:Starting...")
			tester := app.NewSmokeTester(*addr, authService)
			// Give services a moment to complete their internal initialization
			time.Sleep(2 * time.Second)
			smokeCtx, smokeCancel := context.WithTimeout(context.Background(), 1*time.Minute)
			defer smokeCancel()
			result, err := tester.Run(smokeCtx)
			if err != nil {
				app.Errorf("System:Internal:AutoSmokeTest:Failed: %v", err)
			} else if !result.Ok {
				app.Warnf("System:Internal:AutoSmokeTest:Failed: %s", result.Message)
			} else {
				app.Infof("System:Internal:AutoSmokeTest:Success: %s", result.Message)
			}
		}()
	}
	if err := <-serverErrCh; err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeInternalRegister(req toolproto.SupervisorRegisterRequest) (app.HubServiceRegisterRequest, string, error) {
	serviceID := strings.TrimSpace(req.ServiceID)
	if serviceID == "" {
		return app.HubServiceRegisterRequest{}, "", fmt.Errorf("service_id is required")
	}
	tools := make([]app.ServiceToolDescriptor, 0, len(req.Tools))
	for _, t := range req.Tools {
		toolID := strings.TrimSpace(t.ToolID)
		if toolID == "" {
			continue
		}
		category := ""
		typ := ""
		tool := ""
		parts := strings.Split(toolID, ".")
		if len(parts) >= 3 {
			category = parts[0]
			typ = parts[1]
			tool = strings.Join(parts[2:], ".")
		}
		streaming := ""
		if t.Streaming {
			streaming = "stream"
		}
		tools = append(tools, app.ServiceToolDescriptor{
			ToolID:               toolID,
			Category:             category,
			Type:                 typ,
			Tool:                 tool,
			Description:          toolID,
			CapabilitiesRequired: t.CapabilitiesRequired,
			TimeoutMSDefault:     t.TimeoutMS,
			Streaming:            streaming,
			WSPath:               strings.TrimSpace(t.WSPath),
		})
	}

	transportName := strings.ToLower(strings.TrimSpace(req.Transport))
	if transportName == "" {
		switch {
		case strings.TrimSpace(req.Endpoint.UDSPath) != "":
			transportName = "uds"
		case strings.TrimSpace(req.Endpoint.TCPURL) != "":
			transportName = "tcp"
		default:
			transportName = "tcp"
		}
	}

	endpoint := strings.TrimSpace(req.Endpoint.TCPURL)
	if transportName == "uds" {
		endpoint = strings.TrimSpace(req.Endpoint.UDSPath)
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(req.Endpoint.TCPURL)
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(req.Endpoint.UDSPath)
	}
	if endpoint == "" {
		endpoint = serviceID
	}

	return app.HubServiceRegisterRequest{
		Manifest: app.ServiceManifest{
			ServiceID:   serviceID,
			ServiceName: serviceID,
			Version:     strings.TrimSpace(req.Version),
			Reliability: "untrusted",
			Visibility:  "internal",
			Provides:    tools,
		},
		InstanceID: strings.TrimSpace(req.InstanceID),
		Endpoint:   endpoint,
		Healthy:    req.Healthy,
	}, transportName, nil
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func verifyServiceInternalAuth(hubPlatform *app.HubPlatform, r *http.Request, expectedServiceID string, expectedInstanceID string) (string, string, error) {
	if hubPlatform == nil {
		return "", "", fmt.Errorf("hub platform is nil")
	}
	serviceID, instanceID, serviceAuth := hubsvc.ExtractServiceAuthHeaders(r.Header)
	if serviceID == "" || instanceID == "" || serviceAuth == "" {
		return "", "", fmt.Errorf("missing service auth headers")
	}
	if sid := strings.TrimSpace(expectedServiceID); sid != "" && sid != serviceID {
		return "", "", fmt.Errorf("service auth service_id mismatch")
	}
	if iid := strings.TrimSpace(expectedInstanceID); iid != "" && iid != instanceID {
		return "", "", fmt.Errorf("service auth instance_id mismatch")
	}
	verified, err := hubPlatform.VerifyServiceAuth(serviceID, instanceID, serviceAuth)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(verified.ServiceID), strings.TrimSpace(verified.InstanceID), nil
}

func instanceStatusFromHealth(healthy bool) string {
	if healthy {
		return supervisor.InstanceStatusReady
	}
	return supervisor.InstanceStatusUnhealthy
}

func ensureServiceStoppedForRegister(hubPlatform *app.HubPlatform, serviceID string, nextInstanceID string, timeout time.Duration) (app.HubServiceRegistration, bool, error) {
	if hubPlatform == nil {
		return app.HubServiceRegistration{}, false, fmt.Errorf("hub platform is nil")
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return app.HubServiceRegistration{}, false, nil
	}
	existing, ok := hubPlatform.GetService(sid)
	if !ok {
		return app.HubServiceRegistration{}, false, nil
	}
	if iid := strings.TrimSpace(nextInstanceID); iid != "" && iid == strings.TrimSpace(existing.InstanceID) {
		return existing, true, nil
	}
	if err := stopServiceRegistration(existing, timeout); err != nil {
		return existing, true, err
	}
	hubPlatform.UnregisterService(existing.ServiceID, existing.InstanceID)
	return existing, true, nil
}

func broadcastServiceShutdown(hubPlatform *app.HubPlatform, timeout time.Duration) {
	if hubPlatform == nil {
		return
	}
	services := hubPlatform.ListRegisteredServices()
	if len(services) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, reg := range services {
		reg := reg
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := stopServiceRegistration(reg, timeout); err != nil {
				app.Warnf("broadcast shutdown failed for service=%s instance=%s pid=%d endpoint=%s err=%v", reg.ServiceID, reg.InstanceID, reg.PID, reg.Endpoint, err)
				return
			}
			hubPlatform.UnregisterService(reg.ServiceID, reg.InstanceID)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	maxWait := timeout + 2500*time.Millisecond
	select {
	case <-done:
	case <-time.After(maxWait):
		app.Warnf("broadcast shutdown timeout after %v", maxWait)
	}
}

func stopServiceRegistration(reg app.HubServiceRegistration, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 7 * time.Second
	}
	deadline := time.Now().Add(timeout)

	if shutdownURL := buildServiceControlURL(reg.Endpoint, "/admin/shutdown"); shutdownURL != "" {
		_ = postServiceShutdown(shutdownURL)
	}

	for time.Now().Before(deadline) {
		alive, pidAlive := serviceRuntimeAlive(reg.Endpoint, reg.PID)
		if !alive && !pidAlive {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	if reg.PID > 1 && isPIDAlive(reg.PID) {
		_ = syscall.Kill(reg.PID, syscall.SIGTERM)
		termDeadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(termDeadline) {
			alive, pidAlive := serviceRuntimeAlive(reg.Endpoint, reg.PID)
			if !alive && !pidAlive {
				return nil
			}
			time.Sleep(150 * time.Millisecond)
		}
	}

	if reg.PID > 1 && isPIDAlive(reg.PID) {
		_ = syscall.Kill(reg.PID, syscall.SIGKILL)
		time.Sleep(300 * time.Millisecond)
	}
	alive, pidAlive := serviceRuntimeAlive(reg.Endpoint, reg.PID)
	if alive || pidAlive {
		return fmt.Errorf("service still alive after shutdown attempts")
	}
	return nil
}

func serviceRuntimeAlive(endpoint string, pid int) (bool, bool) {
	epAlive := false
	if healthzURL := buildServiceControlURL(endpoint, "/healthz"); healthzURL != "" {
		epAlive = isServiceEndpointAlive(healthzURL)
	}
	pidAlive := pid > 1 && isPIDAlive(pid)
	return epAlive, pidAlive
}

func postServiceShutdown(shutdownURL string) error {
	req, err := http.NewRequest(http.MethodPost, shutdownURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 1300 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 500 {
		return fmt.Errorf("shutdown status=%d", resp.StatusCode)
	}
	return nil
}

func isServiceEndpointAlive(healthzURL string) bool {
	client := &http.Client{Timeout: 900 * time.Millisecond}
	resp, err := client.Get(healthzURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func buildServiceControlURL(endpoint string, targetPath string) string {
	base := strings.TrimSpace(endpoint)
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parsed.Path = strings.TrimSpace(targetPath)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func isPIDAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
