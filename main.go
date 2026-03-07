package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	app "kagent/internal"

	"github.com/gorilla/websocket"
)

func main() {
	configPath := flag.String("config", "config/configx.json", "path to config json")
	modelName := flag.String("model", "doubao", "model name in config")
	addr := flag.String("addr", "127.0.0.1:18080", "listen addr")
	flag.Parse()

	cfg, err := app.LoadModelConfig(*configPath, *modelName)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	ver, verr := app.LoadVersionInfo("version.json")
	if verr != nil {
		log.Printf("load version.json failed: %v", verr)
		ver = &app.VersionInfo{Format: "calver-yymmddnn", Backend: "unknown", WebUI: "unknown"}
	}
	log.Printf("kagent version backend=%s webui=%s", ver.Backend, ver.WebUI)

	mux := http.NewServeMux()
	var server *http.Server
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(ver)
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

		log.Printf("shutdown requested from %s", r.RemoteAddr)

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
			log.Printf("ws upgrade failed: %v", err)
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		s := app.NewSession(conn, cfg)
		go func() {
			ctx, cancel := context.WithCancel(appCtx)
			defer cancel()
			if err := s.Run(ctx); err != nil {
				log.Printf("session ended with error: %v", err)
			}
		}()
	})

	server = &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("kagent T0 server listening on http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
}
