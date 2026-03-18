package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kagent/pkg/toolproto"
	app "kagent/services/auth/internal/app"
)

func main() {
	sqlitePath := flag.String("sqlite-path", "data/kagent.db", "path to sqlite db")
	userID := flag.String("user-id", "default", "runtime user id")
	projectID := flag.String("project-id", "project-default", "runtime project id")
	threadID := flag.String("thread-id", "chat-default", "runtime thread id")
	addr := flag.String("addr", "127.0.0.1:18083", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "AUTH")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	sqlitePathResolved := app.ResolvePathFromRoot(appRoot, *sqlitePath)
	dataRoot := filepath.Join(appRoot, "data")

	sqliteStore, err := app.NewSQLiteStore(sqlitePathResolved, *userID, *projectID, *threadID)
	if err != nil {
		app.Errorf("init sqlite store failed: %v", err)
		os.Exit(1)
	}
	defer sqliteStore.Close()
	authService, err := app.NewAuthService(dataRoot)
	if err != nil {
		app.Errorf("init auth service failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("auth")
	instance := strings.TrimSpace(*instanceID)
	if instance == "" {
		instance = "auth-" + app.NewRequestID()
	}
	if strings.TrimSpace(*hubRegisterURL) != "" {
		healthy := true
		registerPayload := toolproto.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: toolproto.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools:   toSupervisorTools(manifest),
			Healthy: &healthy,
		}
		raw, _ := json.Marshal(registerPayload)
		req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(*hubRegisterURL), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			app.Errorf("register auth to hub failed: %v", err)
			os.Exit(1)
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register auth to hub status=%d", resp.StatusCode)
			os.Exit(1)
		}
		app.Infof("register auth to hub status=%d", resp.StatusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("auth-service shutdown: %s", strings.TrimSpace(reason))
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
		writeJSON(w, map[string]any{"ok": true, "timestamp_ms": time.Now().UnixMilli()})
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
			Provider:     "auth",
			Capabilities: caps,
			Transport:    "http",
		})
	})
	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceListToolsResponse{
			ServiceID: manifest.ServiceID,
			Tools:     manifestTools(manifest),
		})
	})

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
		uid, err := sqliteStore.CreateUser(username, hash)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名已存在"})
			return
		}
		token, err := authService.IssueJWT(uid, username)
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
		writeJSON(w, map[string]any{"ok": true, "user_id": uid, "username": username})
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
		uid, storedHash, exists, err := sqliteStore.GetUserByUsername(username)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !exists || !app.VerifyPassword(body.Password, storedHash) {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名或密码错误"})
			return
		}
		if app.NeedsPasswordRehash(storedHash) {
			if upgradedHash, err := app.HashPassword(body.Password); err == nil {
				if err := sqliteStore.UpdateUserPasswordHash(uid, upgradedHash); err != nil {
					app.Warnf("upgrade password hash failed for user=%s: %v", uid, err)
				}
			}
		}
		token, err := authService.IssueJWT(uid, username)
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
		writeJSON(w, map[string]any{"ok": true, "user_id": uid, "username": username})
	})

	mux.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: app.JWTCookieName, Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]any{"ok": false, "error": "未登录"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "user_id": claims.UserID, "username": claims.Username})
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

	server = &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if hbURL := buildHubHeartbeatURL(strings.TrimSpace(*hubRegisterURL)); hbURL != "" {
		startHubHeartbeatGuard(hbURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), shutdownNow)
	}
	app.Infof("auth service listening=http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}

func builtinManifest(serviceID string) app.ServiceManifest {
	for _, m := range app.BuiltinServiceManifests() {
		if strings.TrimSpace(m.ServiceID) == strings.TrimSpace(serviceID) {
			return m
		}
	}
	return app.ServiceManifest{ServiceID: serviceID, ServiceName: serviceID, Version: "1.0.0"}
}

func manifestTools(manifest app.ServiceManifest) []app.AIServiceToolDescriptor {
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
	return tools
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func extractJWTClaims(r *http.Request, authService *app.AuthService) (app.JWTClaims, error) {
	cookie, err := r.Cookie(app.JWTCookieName)
	if err != nil {
		return app.JWTClaims{}, err
	}
	return authService.ParseJWT(cookie.Value)
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
				"status":      "ready",
				"healthy":     true,
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

func _unused(_ ...any) {
	_ = fmt.Sprintf
}
