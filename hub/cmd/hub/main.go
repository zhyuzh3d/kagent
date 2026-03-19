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
)

func main() {
	publicConfigPath := flag.String("public-config", "config/config.json", "path to public config json")
	userConfigPath := flag.String("user-config", "data/users/default/user_custom_config.json", "path to user custom config json")
	sqlitePath := flag.String("sqlite-path", "data/hub/users.db", "path to sqlite auth user store")
	servicesConfigPath := flag.String("services-config", "config/services.json", "path to hub managed services lifecycle config")
	addr := flag.String("addr", "127.0.0.1:18080", "listen addr")
	chatServiceURL := flag.String("chat-service-url", "http://127.0.0.1:18082", "chat service base url")
	accountServiceURL := flag.String("account-service-url", "http://127.0.0.1:18083", "account service base url")
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
	serviceDirs := []struct {
		serviceID string
		dir       string
	}{
		{serviceID: "chat-server", dir: "chat_server"},
		{serviceID: "account", dir: "account"},
		{serviceID: "ai-doubao", dir: "ai_doubao"},
		{serviceID: "file", dir: "file_storage"},
		{serviceID: "database", dir: "sql_db"},
		{serviceID: "surface-manager", dir: "surface-manager"},
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
			"chat-server":     {Transport: "tcp", TCPURL: strings.TrimSpace(*chatServiceURL)},
			"account":         {Transport: "tcp", TCPURL: strings.TrimSpace(*accountServiceURL)},
			"file":            {Transport: "tcp", TCPURL: strings.TrimSpace(*fileServiceURL)},
			"database":        {Transport: "tcp", TCPURL: strings.TrimSpace(*databaseServiceURL)},
			"surface-manager": {Transport: "tcp", TCPURL: strings.TrimSpace(*surfaceManagerURL)},
		},
	)

	supervisorHandler := supervisor.NewSupervisorHandler(
		hubPlatform,
		supervisorRegistry,
		routingEngine,
		auditStore,
	)
	supervisorHandler.SetOnServiceReady(func(serviceID string) {
		if strings.TrimSpace(serviceID) != "account" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := toolHandler.SyncAccountState(ctx); err != nil {
			app.Warnf("sync account state failed: %v", err)
			return
		}
		app.Infof("account security state synced")
	})

	_ = hubgateway.NewAdminHandler(
		authService,
		hubPlatform,
		supervisorRegistry,
		routingEngine,
		auditStore,
		toolHandler,
	)

	systemHandler := hubgateway.NewSystemHandler(
		hubPlatform,
		runtimeCfg,
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
		manager, managerErr := supervisor.NewLifecycleManager(appRoot, registerURL, cfg, hubPlatform, supervisorRegistry, transportClient)
		if managerErr != nil {
			app.Warnf("ServiceLifecycle-Init-Error:%v", managerErr)
		} else {
			lifecycleManager = manager
			systemHandler.UpdateLifecycleManager(manager)
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

	app.Infof("System:Internal:Startup:Listen: %s", *addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		app.Errorf("System:Internal:Startup:ServerError: %v", err)
		os.Exit(1)
	}
}
