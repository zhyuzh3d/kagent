package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	hubgateway "kagent/hub/internal/gateway"
	"kagent/hub/internal/observability"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/supervisor"
	"kagent/hub/internal/transport"
	"kagent/pkg/hubsvc"
)

func main() {
	sqlitePath := flag.String("sqlite-path", "data/hub/users.db", "path to sqlite auth user store")
	servicesConfigPath := flag.String("services-config", "hub/config/services.json", "path to hub managed services lifecycle config")
	addr := flag.String("addr", "127.0.0.1:18080", "listen addr")
	chatServiceURL := flag.String("chat-server-url", "http://127.0.0.1:18082", "chat_server service base url")
	accountServiceURL := flag.String("account-service-url", "http://127.0.0.1:18083", "account service base url")
	fileStorageURL := flag.String("file-storage-url", "http://127.0.0.1:18084", "file_storage service base url")
	sqlDBURL := flag.String("sql-db-url", "http://127.0.0.1:18085", "sql_db service base url")
	surfaceManagerURL := flag.String("surface-manager-url", "http://127.0.0.1:18086", "surface_manager service base url")
	autoguiURL := flag.String("autogui-url", "http://127.0.0.1:18087", "autogui service base url")
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
	if _, err := hubsvc.EnsureProjectConfigFiles(filepath.Join(appRoot, "hub")); err != nil {
		app.Warnf("HubConfigFile-Ensure-Error:%v", err)
	} else {
		if _, err := hubsvc.LoadProjectConfig(filepath.Join(appRoot, "hub")); err != nil {
			app.Warnf("HubConfigFile-Load-Error:%v", err)
		}
	}
	sqlitePathResolved := app.ResolvePathFromRoot(appRoot, *sqlitePath)
	servicesConfigPathResolved := app.ResolvePathFromRoot(appRoot, *servicesConfigPath)
	dataRoot := filepath.Join(appRoot, "data")
	webuiRoot := filepath.Join(appRoot, "webui")
	versionPath := filepath.Join(appRoot, "version.json")

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
	hubPlatform.SetBuiltinTools(hubgateway.HubManifest().Tools)
	servicesRoot := filepath.Join(appRoot, "services")
	serviceDirs := []struct {
		serviceID string
		dir       string
	}{
		{serviceID: "chat_server", dir: "chat_server"},
		{serviceID: "account", dir: "account"},
		{serviceID: "ai_doubao", dir: "ai_doubao"},
		{serviceID: "file_storage", dir: "file_storage"},
		{serviceID: "sql_db", dir: "sql_db"},
		{serviceID: "surface_manager", dir: "surface_manager"},
		{serviceID: "autogui", dir: "autogui"},
	}
	for _, item := range serviceDirs {
		if err := app.EnsureServiceConfigFiles(filepath.Join(servicesRoot, item.dir)); err != nil {
			app.Warnf("ServiceConfigFile-Ensure-Error:%s-%v", item.serviceID, err)
		}
	}
	_, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	ver, verr := app.LoadVersionInfo(versionPath)
	if verr != nil {
		app.Warnf("VersionFile-Read-Error:%v", verr)
		ver = &app.VersionInfo{Format: "calver-yymmddnn", Backend: "unknown", WebUI: "unknown"}
	}
	app.Infof("System:Internal:Version:backend=%s,webui=%s", ver.Backend, ver.WebUI)

	mux := http.NewServeMux()
	var server *http.Server
	var lifecycleManager *supervisor.LifecycleManager
	supervisorRegistry := supervisor.NewRegistry()
	routingEngine := routing.NewEngine()
	auditStore := observability.NewStore(3000)
	transportClient := transport.NewClient(true)

	// Create handler groups
	toolHandler := hubgateway.NewToolHandler(
		authService,
		hubPlatform,
		routingEngine,
		supervisorRegistry,
		transportClient,
		auditStore,
		map[string]transport.Endpoint{
			"chat_server":     {Transport: "tcp", TCPURL: strings.TrimSpace(*chatServiceURL)},
			"account":         {Transport: "tcp", TCPURL: strings.TrimSpace(*accountServiceURL)},
			"file_storage":    {Transport: "tcp", TCPURL: strings.TrimSpace(*fileStorageURL)},
			"sql_db":          {Transport: "tcp", TCPURL: strings.TrimSpace(*sqlDBURL)},
			"surface_manager": {Transport: "tcp", TCPURL: strings.TrimSpace(*surfaceManagerURL)},
			"autogui":         {Transport: "tcp", TCPURL: strings.TrimSpace(*autoguiURL)},
		},
	)

	supervisorHandler := supervisor.NewSupervisorHandler(
		hubPlatform,
		supervisorRegistry,
		routingEngine,
		auditStore,
	)
	syncAccountStateWithRetry := func() error {
		var lastErr error
		for attempt := 1; attempt <= 12; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := toolHandler.SyncAccountState(ctx)
			cancel()
			if err == nil {
				return nil
			}
			lastErr = err
			time.Sleep(300 * time.Millisecond)
		}
		return lastErr
	}

	adminHandler := hubgateway.NewAdminHandler(
		authService,
		hubPlatform,
		supervisorRegistry,
		routingEngine,
		auditStore,
		toolHandler,
		lifecycleManager,
		startupSnapshotStore,
		servicesConfigPathResolved,
		appRoot,
	)

	systemHandler := hubgateway.NewSystemHandler(
		hubPlatform,
		ver,
		lifecycleManager,
		webuiRoot,
		*addr,
	)
	systemHandler.AppCancel = appCancel

	// Register internal tools in ToolHandler
	supervisorHandler.RegisterTools(toolHandler)
	systemHandler.RegisterTools(toolHandler)

	// Register routes
	// 1. Tool API
	mux.HandleFunc("/api/tool/call", toolHandler.HandleCall)
	mux.HandleFunc("/api/tool/ws", toolHandler.HandleWS)

	// 4. System API (Legacy REST - Transitioning to hub.system.*)
	// All system interfaces moved to hub.system.* tools

	// 5. Static Files and Root
	mux.HandleFunc("/", systemHandler.HandleStaticFiles)

	// Initialize lifecycle manager if config exists
	if cfg, cfgErr := supervisor.LoadLifecycleConfig(servicesConfigPathResolved); cfgErr != nil {
		app.Warnf("ServiceLifecycle-Config-Load-Error:%v", cfgErr)
	} else {
		registerURL := "http://" + strings.TrimSpace(*addr) + "/api/tool/call"
		manager, managerErr := supervisor.NewLifecycleManager(appRoot, servicesConfigPathResolved, registerURL, cfg, hubPlatform, supervisorRegistry)
		if managerErr != nil {
			app.Warnf("ServiceLifecycle-Init-Error:%v", managerErr)
		} else {
			lifecycleManager = manager
			systemHandler.UpdateLifecycleManager(manager)
			adminHandler.UpdateLifecycleManager(manager)
		}
	}

	// Apply middlewares
	identityMiddleware := app.IdentityMiddleware(authService, hubPlatform)
	handler := identityMiddleware(hubgateway.NewLoggingMiddleware(mux, []string{"/api/tool/call", "/api/tool/ws"}))

	// Start server
	server = &http.Server{
		Addr:         *addr,
		Handler:      handler,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	systemHandler.Server = server

	listener, err := hubsvc.Listen(*addr)
	if err != nil {
		app.Errorf("System:Internal:Startup:ServerError: %v", err)
		os.Exit(1)
	}
	app.Infof("System:Internal:Startup:Listen: %s", *addr)
	if lifecycleManager != nil {
		go func() {
			startCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			app.Infof("System:Internal:Startup:Lifecycle:StartAll: begin")
			snapshot := lifecycleManager.StartAll(startCtx)
			readyCount := 0
			registeredCount := 0
			skippedCount := 0
			for _, svc := range snapshot.Services {
				if svc.Registered {
					registeredCount++
				}
				if svc.Ready {
					readyCount++
					app.Infof("System:Internal:Startup:ServiceReady: service=%s pid=%d instance=%s endpoint=%s attempts=%d", svc.ServiceID, svc.PID, svc.Instance, svc.Endpoint, svc.Attempts)
					continue
				}
				if strings.TrimSpace(svc.Status) == supervisor.InstanceStatusSkipped {
					skippedCount++
					app.Warnf("System:Internal:Startup:ServiceSkipped: service=%s dir=%s status=%s err=%s", svc.ServiceID, svc.Dir, svc.Status, svc.ErrorText)
					continue
				}
				app.Warnf("System:Internal:Startup:ServiceFailed: service=%s dir=%s exec=%s attempts=%d err=%s", svc.ServiceID, svc.Dir, svc.ExecPath, svc.Attempts, svc.ErrorText)
			}
			for _, svc := range snapshot.Services {
				if strings.TrimSpace(svc.ServiceID) == "account" && svc.Ready {
					if err := syncAccountStateWithRetry(); err != nil {
						app.Warnf("sync account state failed: %v", err)
					} else {
						app.Infof("account security state synced")
					}
					break
				}
			}
			app.Infof("System:Internal:Startup:Lifecycle:Done: registered=%d ready=%d skipped=%d total=%d duration_ms=%d", registeredCount, readyCount, skippedCount, len(snapshot.Services), snapshot.CompletedAtMS-snapshot.StartedAtMS)
			if startupSnapshotStore != nil {
				if err := startupSnapshotStore.Save(ver.Backend, snapshot); err != nil {
					app.Warnf("StartupSnapshotStore-Save-Error:%v", err)
				}
			}
			go func() {
				if err := syncAccountStateWithRetry(); err != nil {
					app.Warnf("System:Internal:SmokeTest:PreSyncFailed: %v", err)
				}
				smokeCtx, smokeCancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer smokeCancel()
				tester := app.NewSmokeTester(*addr)
				result, err := tester.Run(smokeCtx)
				if err != nil {
					app.Warnf("System:Internal:SmokeTest:RunFailed: %v", err)
					return
				}
				if result == nil {
					app.Warnf("System:Internal:SmokeTest:EmptyResult")
					return
				}
				if result.Ok {
					app.Infof("System:Internal:SmokeTest:Passed: user=%s project=%s thread=%s stages=%d", result.Username, result.ProjectID, result.ThreadID, len(result.Stages))
					return
				}
				app.Warnf("System:Internal:SmokeTest:Failed: %s", result.Message)
			}()
		}()
	}
	if err := server.Serve(listener); err != http.ErrServerClosed {
		app.Errorf("System:Internal:Startup:ServerError: %v", err)
		os.Exit(1)
	}
}
