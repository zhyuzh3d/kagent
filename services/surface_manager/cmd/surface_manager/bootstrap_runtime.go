package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/surface_manager/internal/app"
)

func runSurfaceManager() {
	addr := flag.String("addr", "127.0.0.1:18086", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "SURFACE_MANAGER")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	if _, err := hubsvc.LoadProjectConfig(filepath.Join(appRoot, "services", "surface_manager")); err != nil {
		app.Errorf("load service config failed: %v", err)
		os.Exit(1)
	}
	dataRoot := filepath.Join(appRoot, "data")
	webuiRoot := filepath.Join(appRoot, "webui")
	surfaceRoot := filepath.Join(webuiRoot, "surface")
	serviceSecretPath := filepath.Join(appRoot, "services", "surface_manager", "run", ".service_secret")
	processStorePath := filepath.Join(appRoot, "services", "surface_manager", "run", ".service_pid")
	runtimeManifestPath := filepath.Join(appRoot, "services", "surface_manager", "run", "manifest_runtime.json")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}
	if err := hubsvc.CleanupPreviousServiceProcess(processStorePath, "surface_manager"); err != nil {
		app.Errorf("cleanup previous process failed: %v", err)
		os.Exit(1)
	}

	surfaceFS, err := app.NewSurfaceFSService(dataRoot)
	if err != nil {
		app.Errorf("init surfacefs failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("surface_manager")
	if strings.TrimSpace(serviceBootstrap.ServiceID) != strings.TrimSpace(manifest.ServiceID) {
		app.Errorf("bootstrap service_id mismatch: expect=%s got=%s", strings.TrimSpace(manifest.ServiceID), strings.TrimSpace(serviceBootstrap.ServiceID))
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(serviceBootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	hubToolCallURL := buildHubToolCallURL(registerURL)
	instance := strings.TrimSpace(serviceBootstrap.InstanceID)
	if instance == "" {
		instance = strings.TrimSpace(*instanceID)
	}
	if instance == "" {
		instance = "surface_manager-" + app.NewRequestID()
	}
	var lifecycleMu sync.RWMutex
	var initialized bool
	var initializing bool
	var lastInitErr string
	var store *app.HubStore
	currentStatus := func() string {
		lifecycleMu.RLock()
		defer lifecycleMu.RUnlock()
		switch {
		case initialized:
			return "ready"
		case initializing:
			return "initializing"
		case strings.TrimSpace(lastInitErr) != "":
			return "failed"
		default:
			return "registered"
		}
	}
	currentHealthy := func() bool {
		lifecycleMu.RLock()
		defer lifecycleMu.RUnlock()
		return strings.TrimSpace(lastInitErr) == ""
	}
	runInit := func(ctx context.Context) error {
		lifecycleMu.Lock()
		if initialized {
			lifecycleMu.Unlock()
			return nil
		}
		if initializing {
			lifecycleMu.Unlock()
			return nil
		}
		initializing = true
		lastInitErr = ""
		lifecycleMu.Unlock()

		nextStore := app.NewHubStore(hubToolCallURL, serviceBootstrap, manifest.ServiceID, 8*time.Second)
		if err := nextStore.EnsureSchema(ctx); err != nil {
			lifecycleMu.Lock()
			initializing = false
			lastInitErr = err.Error()
			lifecycleMu.Unlock()
			_ = nextStore.Close()
			return fmt.Errorf("init hub-backed store failed: %w", err)
		}
		if err := app.SyncSurfaceCatalog(ctx, nextStore, surfaceRoot); err != nil {
			app.Warnf("surface catalog scan skipped: %v", err)
		}
		lifecycleMu.Lock()
		oldStore := store
		store = nextStore
		initializing = false
		initialized = true
		lastInitErr = ""
		lifecycleMu.Unlock()
		if oldStore != nil {
			_ = oldStore.Close()
		}
		return nil
	}
	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("surface_manager shutdown: %s", strings.TrimSpace(reason))
			lifecycleMu.RLock()
			activeStore := store
			lifecycleMu.RUnlock()
			if activeStore != nil {
				_ = activeStore.Close()
			}
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
		writeJSON(w, app.AIServiceInfo{ServiceID: manifest.ServiceID, ServiceName: manifest.ServiceName, Version: manifest.Version, Provider: "surface_manager", Capabilities: caps, Transport: "http"})
	})
	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceListToolsResponse{ServiceID: manifest.ServiceID, Tools: manifestTools(manifest)})
	})
	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
		handleSurfaceToolExec(
			w, r, manifest, instance, *addr, surfaceRoot, hubToolCallURL, serviceBootstrap, surfaceFS,
			currentHealthy, currentStatus,
			func() string {
				lifecycleMu.RLock()
				defer lifecycleMu.RUnlock()
				return strings.TrimSpace(lastInitErr)
			},
			func() *app.HubStore {
				lifecycleMu.RLock()
				defer lifecycleMu.RUnlock()
				return store
			},
		)
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
	initCtx, initCancel := context.WithTimeout(context.Background(), 8*time.Second)
	if err := runInit(initCtx); err != nil {
		initCancel()
		app.Errorf("surface_manager init failed: %v", err)
		os.Exit(1)
	}
	initCancel()
	app.Infof("surface_manager listening=http://%s", *addr)
	ln, err := hubsvc.Listen(*addr)
	if err != nil {
		app.Errorf("server listen failed: %v", err)
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
	if registerURL != "" {
		healthy := true
		registerCall := toolproto.CallRequest{
			ToolID: "hub.governance.service.register",
			Args: map[string]any{
				"service_id":  strings.TrimSpace(manifest.ServiceID),
				"instance_id": strings.TrimSpace(instance),
				"version":     strings.TrimSpace(manifest.Version),
				"transport":   "tcp",
				"endpoint": map[string]any{
					"tcp_url": "http://" + strings.TrimSpace(*addr),
				},
				"tools":   toSupervisorTools(manifest),
				"healthy": &healthy,
			},
			Context: &toolproto.Context{
				RequestID: "reg-" + app.NewRequestID(),
				TraceID:   "tr-" + app.NewRequestID(),
				Caller: toolproto.Caller{
					Type:      "service",
					ServiceID: manifest.ServiceID,
				},
			},
		}
		rawResp, statusCode, err := postHubToolCall(registerURL, serviceBootstrap, registerCall)
		if err != nil {
			app.Errorf("register surface_manager to hub failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if statusCode >= 300 {
			app.Errorf("register surface_manager to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
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
		app.Infof("register surface_manager to hub status=%d", statusCode)
	}
	if hubToolCallURL := buildHubToolCallURL(registerURL); hubToolCallURL != "" {
		startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow, currentStatus, currentHealthy)
	}
	if err := <-serveErrCh; err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}
