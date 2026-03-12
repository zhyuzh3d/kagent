package main

import (
	"context"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	app "kagent/internal"

	"github.com/gorilla/websocket"
)

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

	cfg, err := app.LoadModelConfig(*configPath, *modelName)
	if err != nil {
		app.Errorf("load config failed: %v", err)
		os.Exit(1)
	}
	runtimeCfg, err := app.NewRuntimeConfigManager(*publicConfigPath, *userConfigPath)
	if err != nil {
		app.Errorf("load runtime config failed: %v", err)
		os.Exit(1)
	}
	if err := app.CleanupLegacyStorage("data", *sqlitePath); err != nil {
		app.Warnf("cleanup legacy storage skipped: %v", err)
	}
	sqliteStore, err := app.NewSQLiteStore(*sqlitePath, *userID, *projectID, *threadID)
	if err != nil {
		app.Errorf("init sqlite store failed: %v", err)
		os.Exit(1)
	}
	defer sqliteStore.Close()

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	ver, verr := app.LoadVersionInfo("version.json")
	if verr != nil {
		app.Warnf("load version.json failed: %v", verr)
		ver = &app.VersionInfo{Format: "calver-yymmddnn", Backend: "unknown", WebUI: "unknown"}
	}
	app.Infof("kagent version backend=%s webui=%s", ver.Backend, ver.WebUI)

	mux := http.NewServeMux()
	var server *http.Server
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(ver)
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

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"message": "shutting down",
		})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		go func() {
			// Give the response a brief moment to flush before tearing down the server.
			time.Sleep(20 * time.Millisecond)
			appCancel()
			if server != nil {
				_ = server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				_ = server.Shutdown(ctx)
				cancel()
			}
			// Hard fallback: ensure the process actually exits quickly.
			time.Sleep(80 * time.Millisecond)
			os.Exit(0)
		}()
	})
	// Serve the webui directory as the root static file server.
	// Files like /favicon.ico, /chat/index.html, /img/* are served directly.
	staticFS := http.FileServer(http.Dir("webui"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/page/chat/", http.StatusFound)
			return
		}
		staticFS.ServeHTTP(w, r)
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
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			app.Errorf("ws upgrade failed: %v", err)
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		s := app.NewSession(conn, cfg, runtimeCfg, sqliteStore)
		go func() {
			ctx, cancel := context.WithCancel(appCtx)
			defer cancel()
			if err := s.Run(ctx); err != nil {
				app.Errorf("session ended with error: %v", err)
			}
		}()
	})

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	app.Infof("kagent T0 server listening on http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}
