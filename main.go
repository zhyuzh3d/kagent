package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	app "kagent/internal"

	"github.com/gorilla/websocket"
)

type enableSurfaceRequest struct {
	Enabled *bool `json:"enabled"`
}

type surfaceCapabilityRequest struct {
	SurfaceSessionToken string `json:"surface_session_token"`
	Scope               string `json:"scope"`
	PathPrefix          string `json:"path_prefix,omitempty"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

type surfaceFSReadRequest struct {
	CapabilityToken string `json:"capability_token"`
	SurfaceID       string `json:"surface_id"`
	Path            string `json:"path"`
}

type surfaceFSWriteRequest struct {
	CapabilityToken string `json:"capability_token"`
	SurfaceID       string `json:"surface_id"`
	Path            string `json:"path"`
	DataBase64      string `json:"data_base64"`
}

type surfaceFSDeleteRequest struct {
	CapabilityToken string `json:"capability_token"`
	SurfaceID       string `json:"surface_id"`
	Path            string `json:"path"`
	Recursive       bool   `json:"recursive"`
}

func main() {
	configPath := flag.String("config", "config/configx.json", "path to private config json")
	publicConfigPath := flag.String("public-config", "config/config.json", "path to public config json")
	userConfigPath := flag.String("user-config", "data/users/default/user_custom_config.json", "path to user custom config json")
	sqlitePath := flag.String("sqlite-path", "data/kagent.db", "path to sqlite message store")
	userID := flag.String("user-id", "default", "runtime user id")
	projectID := flag.String("project-id", "project-default", "runtime project id")
	threadID := flag.String("thread-id", "chat-default", "runtime thread id")
	chatID := flag.String("chat-id", "", "deprecated alias of --thread-id")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18080", "listen addr")
	flag.Parse()
	if strings.TrimSpace(*chatID) != "" {
		*threadID = strings.TrimSpace(*chatID)
	}

	app.InitLogger(app.LevelDebug)

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	configPathResolved := app.ResolvePathFromRoot(appRoot, *configPath)
	publicConfigPathResolved := app.ResolvePathFromRoot(appRoot, *publicConfigPath)
	userConfigPathResolved := app.ResolvePathFromRoot(appRoot, *userConfigPath)
	sqlitePathResolved := app.ResolvePathFromRoot(appRoot, *sqlitePath)
	dataRoot := filepath.Join(appRoot, "data")
	webuiRoot := filepath.Join(appRoot, "webui")
	surfaceRoot := filepath.Join(webuiRoot, "surface")
	versionPath := filepath.Join(appRoot, "version.json")

	cfg, err := app.LoadModelConfig(configPathResolved, *modelName)
	if err != nil {
		app.Errorf("load config failed: %v", err)
		os.Exit(1)
	}
	aiServiceCfg := cfg.EffectiveAIService()
	runtimeCfg, err := app.NewRuntimeConfigManager(publicConfigPathResolved, userConfigPathResolved)
	if err != nil {
		app.Errorf("load runtime config failed: %v", err)
		os.Exit(1)
	}
	if err := app.CleanupLegacyStorage(dataRoot, sqlitePathResolved); err != nil {
		app.Warnf("cleanup legacy storage skipped: %v", err)
	}
	sqliteStore, err := app.NewSQLiteStore(sqlitePathResolved, *userID, *projectID, *threadID)
	if err != nil {
		app.Errorf("init sqlite store failed: %v", err)
		os.Exit(1)
	}
	defer sqliteStore.Close()
	// Initial IDs for backend log / setup (optional)
	_ = sqliteStore.RuntimeUserID()

	if err := app.SyncSurfaceCatalog(sqliteStore, surfaceRoot); err != nil {
		app.Warnf("surface catalog scan skipped: %v", err)
	}
	surfaceFS, err := app.NewSurfaceFSService(dataRoot)
	if err != nil {
		app.Errorf("init surfacefs failed: %v", err)
		os.Exit(1)
	}
	authService, err := app.NewAuthService(dataRoot)
	if err != nil {
		app.Errorf("init auth service failed: %v", err)
		os.Exit(1)
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	localProviderFactory := app.NewLocalProviderFactory()
	var aiServiceManager *app.AIServiceManager
	if app.IsServiceMode(cfg) {
		aiServiceManager = app.NewAIServiceManager(aiServiceCfg)
		if err := aiServiceManager.Start(appCtx); err != nil {
			app.Warnf("ai service manager start failed, fallback to local provider: %v", err)
		}
		if aiServiceManager != nil {
			ok := aiServiceManager.WaitForHealthy(appCtx, time.Duration(aiServiceCfg.StartupGracePeriodMS)*time.Millisecond)
			if ok {
				app.Infof("ai service is healthy at startup: %s", aiServiceCfg.BaseURL)
			} else {
				app.Warnf("ai service startup health check timeout, fallback to local provider until service is healthy")
			}
			defer aiServiceManager.Stop()
		}
	}
	selectProviderFactory := func() app.ProviderFactory {
		if app.IsServiceMode(cfg) && aiServiceManager != nil && aiServiceManager.IsHealthy() {
			return app.NewServiceProviderFactory(aiServiceCfg)
		}
		return localProviderFactory
	}

	ver, verr := app.LoadVersionInfo(versionPath)
	if verr != nil {
		app.Warnf("load version.json failed: %v", verr)
		ver = &app.VersionInfo{Format: "calver-yymmddnn", Backend: "unknown", WebUI: "unknown"}
	}
	app.Infof("kagent version backend=%s webui=%s", ver.Backend, ver.WebUI)

	mux := http.NewServeMux()
	var server *http.Server

	// Request logging middleware
	loggingMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		app.Infof("→ %s %s", r.Method, r.URL.Path)
		mux.ServeHTTP(w, r)
		app.Infof("← %s %s (%v)", r.Method, r.URL.Path, time.Since(start))
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
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		app.Infof("[FRONTEND] [%s] [%s] %s", body.Level, body.Module, body.Content)
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
		userID, err := sqliteStore.CreateUser(username, hash)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名已存在"})
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
		userID, storedHash, exists, err := sqliteStore.GetUserByUsername(username)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !exists || !app.VerifyPassword(body.Password, storedHash) {
			writeJSON(w, map[string]any{"ok": false, "error": "用户名或密码错误"})
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
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]any{"ok": false, "error": "未登录"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "user_id": claims.UserID, "username": claims.Username})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			list, err := sqliteStore.ListProjectsForUser(claims.UserID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, list)
		case http.MethodPost:
			var body struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			app.Infof("API CreateProject: user=%s title=%s", claims.UserID, body.Title)
			id, err := sqliteStore.CreateProject(claims.UserID, body.Title)
			if err != nil {
				app.Errorf("api create project failed for %s: %v", claims.UserID, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "project_id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		parts := strings.Split(path, "/")
		projectID := parts[0]

		if len(parts) == 2 && parts[1] == "threads" {
			// /api/projects/:id/threads
			switch r.Method {
			case http.MethodGet:
				list, err := sqliteStore.ListThreadsForProject(claims.UserID, projectID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, list)
			case http.MethodPost:
				var body struct {
					Title string `json:"title"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid body", http.StatusBadRequest)
					return
				}
				id, err := sqliteStore.CreateThread(claims.UserID, projectID, body.Title)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				writeJSON(w, map[string]any{"ok": true, "thread_id": id})
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		// /api/projects/:id
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Title      string `json:"title"`
				OrderIndex int    `json:"order_index"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			if err := sqliteStore.UpdateProject(projectID, body.Title, body.OrderIndex); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
		case http.MethodDelete:
			if err := sqliteStore.DeleteProject(projectID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/threads/", func(w http.ResponseWriter, r *http.Request) {
		_, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		threadID := strings.TrimPrefix(r.URL.Path, "/api/threads/")
		switch r.Method {
		case http.MethodPatch:
			var body struct {
				Title      string `json:"title"`
				OrderIndex int    `json:"order_index"`
				ProjectID  string `json:"project_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			if err := sqliteStore.UpdateThread(threadID, body.Title, body.OrderIndex, body.ProjectID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
		case http.MethodDelete:
			if err := sqliteStore.DeleteThread(threadID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/surfaces", func(w http.ResponseWriter, r *http.Request) {
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		surfaces, err := sqliteStore.ListSurfacesForUser(claims.UserID)
		if err != nil {
			http.Error(w, "query surfaces failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"user_id": claims.UserID,
			"total":   len(surfaces),
			"items":   surfaces,
		})
	})

	mux.HandleFunc("/api/surfaces/", func(w http.ResponseWriter, r *http.Request) {
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		pathPart := strings.TrimPrefix(r.URL.Path, "/api/surfaces/")
		pathPart = strings.Trim(pathPart, "/")
		parts := strings.Split(pathPart, "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		surfaceID, err := url.PathUnescape(parts[0])
		if err != nil || strings.TrimSpace(surfaceID) == "" {
			http.Error(w, "invalid surface_id", http.StatusBadRequest)
			return
		}
		action := parts[1]

		switch action {
		case "enable":
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var req enableSurfaceRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
				http.Error(w, "invalid enable payload", http.StatusBadRequest)
				return
			}
			if err := sqliteStore.SetSurfaceEnabled(claims.UserID, surfaceID, *req.Enabled); err != nil {
				http.Error(w, "set surface enabled failed", http.StatusInternalServerError)
				return
			}
			entry, ok, err := sqliteStore.GetSurfaceForUser(claims.UserID, surfaceID)
			if err != nil {
				http.Error(w, "query surface failed", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, entry)
			return

		case "session-token":
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			entry, ok, err := sqliteStore.GetSurfaceForUser(claims.UserID, surfaceID)
			if err != nil {
				http.Error(w, "query surface failed", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			if !entry.Available {
				http.Error(w, "surface is not available", http.StatusForbidden)
				return
			}
			token, expMS, err := surfaceFS.IssueSurfaceSessionToken(claims.UserID, surfaceID, 30*time.Minute)
			if err != nil {
				http.Error(w, "issue session token failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{
				"surface_id":            surfaceID,
				"surface_session_token": token,
				"exp_ms":                expMS,
			})
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/surfacefs/capability", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req surfaceCapabilityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid capability payload", http.StatusBadRequest)
			return
		}
		ttl := 5 * time.Minute
		if req.TTLSeconds > 0 {
			ttl = time.Duration(req.TTLSeconds) * time.Second
		}
		token, expMS, err := surfaceFS.IssueCapabilityTokenFromSession(req.SurfaceSessionToken, req.Scope, req.PathPrefix, ttl)
		if err != nil {
			http.Error(w, "issue capability failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"capability_token": token,
			"exp_ms":           expMS,
			"scope":            req.Scope,
			"path_prefix":      req.PathPrefix,
		})
	})

	mux.HandleFunc("/api/surfacefs/read", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req surfaceFSReadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid surfacefs read payload", http.StatusBadRequest)
			return
		}
		raw, err := surfaceFS.ReadFile(req.CapabilityToken, req.SurfaceID, req.Path)
		if err != nil {
			http.Error(w, "surfacefs read failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"surface_id":  req.SurfaceID,
			"path":        req.Path,
			"size_bytes":  len(raw),
			"data_base64": base64.StdEncoding.EncodeToString(raw),
		})
	})

	mux.HandleFunc("/api/surfacefs/write", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req surfaceFSWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid surfacefs write payload", http.StatusBadRequest)
			return
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.DataBase64))
		if err != nil {
			http.Error(w, "invalid data_base64", http.StatusBadRequest)
			return
		}
		size, err := surfaceFS.WriteFile(req.CapabilityToken, req.SurfaceID, req.Path, raw)
		if err != nil {
			http.Error(w, "surfacefs write failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"surface_id": req.SurfaceID,
			"path":       req.Path,
			"size_bytes": size,
			"ok":         true,
		})
	})

	mux.HandleFunc("/api/surfacefs/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req surfaceFSReadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid surfacefs list payload", http.StatusBadRequest)
			return
		}
		items, err := surfaceFS.ListDir(req.CapabilityToken, req.SurfaceID, req.Path)
		if err != nil {
			http.Error(w, "surfacefs list failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"surface_id": req.SurfaceID,
			"path":       req.Path,
			"items":      items,
		})
	})

	mux.HandleFunc("/api/surfacefs/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req surfaceFSDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid surfacefs delete payload", http.StatusBadRequest)
			return
		}
		if err := surfaceFS.DeletePath(req.CapabilityToken, req.SurfaceID, req.Path, req.Recursive); err != nil {
			http.Error(w, "surfacefs delete failed: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{
			"surface_id": req.SurfaceID,
			"path":       req.Path,
			"ok":         true,
		})
	})

	mux.HandleFunc("/api/admin/services", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		activeFactory := selectProviderFactory().Name()
		if aiServiceManager == nil {
			writeJSON(w, map[string]any{
				"active_provider": activeFactory,
				"services":        []any{},
			})
			return
		}
		writeJSON(w, map[string]any{
			"active_provider": activeFactory,
			"services":        []any{aiServiceManager.Snapshot()},
		})
	})

	mux.HandleFunc("/api/admin/services/ai-doubao/restart", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if aiServiceManager == nil {
			http.Error(w, "service mode is disabled", http.StatusBadRequest)
			return
		}
		if err := aiServiceManager.Restart(); err != nil {
			http.Error(w, "restart failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"ok":      true,
			"service": aiServiceManager.Snapshot(),
		})
	})

	mux.HandleFunc("/surfacefs/static/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		st := strings.TrimSpace(r.URL.Query().Get("st"))
		if st == "" {
			http.Error(w, "missing st token", http.StatusUnauthorized)
			return
		}
		tail := strings.TrimPrefix(r.URL.Path, "/surfacefs/static/")
		tail = strings.TrimPrefix(tail, "/")
		parts := strings.SplitN(tail, "/", 2)
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		surfaceID, err := url.PathUnescape(parts[0])
		if err != nil {
			http.Error(w, "invalid surface_id", http.StatusBadRequest)
			return
		}
		relPath, err := url.PathUnescape(parts[1])
		if err != nil {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		absPath, err := surfaceFS.ResolveStaticFile(st, surfaceID, relPath)
		if err != nil {
			http.Error(w, "surfacefs static denied: "+err.Error(), http.StatusForbidden)
			return
		}
		http.ServeFile(w, r, absPath)
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

	upgrader := websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			host := r.Host
			return strings.HasPrefix(host, "127.0.0.1") || strings.HasPrefix(host, "localhost")
		},
	}
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		claims, err := extractJWTClaims(r, authService)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			app.Errorf("ws upgrade failed: %v", err)
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

		userStore, err := app.NewSQLiteStore(sqlitePathResolved, claims.UserID, pID, tID)
		if err != nil {
			app.Errorf("ws user store failed for %s: %v", claims.UserID, err)
			conn.Close()
			return
		}
		providerFactory := selectProviderFactory()
		app.Infof("create session with provider factory=%s user=%s project=%s thread=%s", providerFactory.Name(), claims.UserID, pID, tID)
		s := app.NewSession(conn, cfg, runtimeCfg, userStore, providerFactory)
		go func() {
			ctx, cancel := context.WithCancel(appCtx)
			defer cancel()
			defer userStore.Close()
			if err := s.Run(ctx); err != nil {
				app.Errorf("session ended with error: %v", err)
			}
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

	server = &http.Server{
		Addr:              *addr,
		Handler:           loggingMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	app.Infof("kagent server root=%s listening=http://%s", appRoot, *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
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
