package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

const (
	serviceID      = "account"
	serviceVersion = "1.0.0"
)

type Config struct {
	Addr           string
	HubRegisterURL string
	InstanceID     string
}

type App struct {
	Addr                string
	ServiceID           string
	ServiceVersion      string
	InstanceID          string
	Bootstrap           hubsvc.BootstrapSecret
	RegisterURL         string
	HeartbeatEvery      time.Duration
	Shutdown            func(reason string)
	BootstrapPath       string
	ProcessStore        string
	RuntimeManifestPath string

	server   *http.Server
	executor *Handler
	adapter  *HTTPHandler
}

func New(cfg Config) (*App, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		addr = "127.0.0.1:18083"
	}
	appRoot, err := detectRepoRoot()
	if err != nil {
		Warnf("detect app root fallback: %v", err)
	}
	if _, err := hubsvc.LoadProjectConfig(filepath.Join(appRoot, "services", serviceID)); err != nil {
		return nil, fmt.Errorf("load service config failed: %w", err)
	}
	serviceSecretPath := filepath.Join(appRoot, "services", serviceID, "run", ".service_secret")
	bootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		return nil, fmt.Errorf("load bootstrap secret failed: %w", err)
	}
	if strings.TrimSpace(bootstrap.ServiceID) != serviceID {
		return nil, fmt.Errorf("bootstrap service_id mismatch: expect=%s got=%s", serviceID, strings.TrimSpace(bootstrap.ServiceID))
	}
	registerURL := strings.TrimSpace(bootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(cfg.HubRegisterURL)
	}
	instance := strings.TrimSpace(bootstrap.InstanceID)
	if instance == "" {
		instance = strings.TrimSpace(cfg.InstanceID)
	}
	if instance == "" {
		instance = serviceID + "-" + newID()
	}
	client := NewClient(registerURL, bootstrap, 8*time.Second)
	executor := NewHandler(client, instance, "http://"+addr, func(ctx context.Context) (SigningKey, error) {
		if err := client.EnsureSchema(ctx); err != nil {
			return SigningKey{}, fmt.Errorf("ensure account schema failed: %w", err)
		}
		signingKey, err := client.GetOrCreateSigningKey(ctx)
		if err != nil {
			return SigningKey{}, fmt.Errorf("init signing key failed: %w", err)
		}
		return signingKey, nil
	})
	app := &App{
		Addr:                addr,
		ServiceID:           serviceID,
		ServiceVersion:      serviceVersion,
		InstanceID:          instance,
		Bootstrap:           bootstrap,
		RegisterURL:         registerURL,
		HeartbeatEvery:      3 * time.Second,
		BootstrapPath:       serviceSecretPath,
		ProcessStore:        filepath.Join(appRoot, "services", serviceID, "run", ".service_pid"),
		RuntimeManifestPath: filepath.Join(appRoot, "services", serviceID, "run", "manifest_runtime.json"),
		executor:            executor,
	}
	app.adapter = NewHTTPHandler(serviceID, instance, bootstrap, executor)
	app.Shutdown = app.shutdownNow
	executor.SetShutdown(app.shutdownNow)
	return app, nil
}

func (a *App) Run() error {
	if err := hubsvc.CleanupPreviousServiceProcess(a.ProcessStore, a.ServiceID); err != nil {
		return fmt.Errorf("cleanup previous process failed: %w", err)
	}
	initCtx, initCancel := context.WithTimeout(context.Background(), 8*time.Second)
	err := a.executor.Initialize(initCtx)
	initCancel()
	if err != nil {
		return fmt.Errorf("initialize account service failed: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/service/tool/exec", a.adapter.HandleToolExec)
	server := &http.Server{
		Addr:              a.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	a.server = server
	ln, err := hubsvc.Listen(a.Addr)
	if err != nil {
		return err
	}
	startedAtMS := time.Now().UnixMilli()
	if err := hubsvc.RecordCurrentServiceProcess(a.ProcessStore, a.ServiceID, startedAtMS); err != nil {
		return fmt.Errorf("record current process failed: %w", err)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(ln)
	}()
	if a.RegisterURL != "" {
		result, err := a.registerToHub()
		if err != nil {
			a.shutdownNow("register to hub failed: " + err.Error())
			return err
		}
		if err := hubsvc.WriteServiceRuntimeManifest(a.RuntimeManifestPath, result); err != nil {
			return fmt.Errorf("write runtime manifest failed: %w", err)
		}
		if result.HeartbeatIntervalSec > 0 {
			a.HeartbeatEvery = time.Duration(result.HeartbeatIntervalSec) * time.Second
		}
		if err := hubsvc.DeleteBootstrapSecret(a.BootstrapPath); err != nil {
			Warnf("delete bootstrap secret failed: %v", err)
		}
		a.startHubHeartbeatGuard()
	}
	Infof("account service listening=http://%s", a.Addr)
	if err := <-serveErrCh; err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (a *App) shutdownNow(reason string) {
	Warnf("account service shutdown: %s", strings.TrimSpace(reason))
	if a.server != nil {
		_ = a.server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		_ = a.server.Shutdown(ctx)
		cancel()
	}
	time.Sleep(80 * time.Millisecond)
	os.Exit(0)
}

func (a *App) registerToHub() (toolproto.SupervisorRegisterResult, error) {
	healthy := true
	callReq := toolproto.CallRequest{
		ToolID: "hub.governance.service.register",
		Args: map[string]any{
			"service_id":  a.ServiceID,
			"instance_id": strings.TrimSpace(a.InstanceID),
			"version":     a.ServiceVersion,
			"transport":   "tcp",
			"endpoint": map[string]any{
				"tcp_url": "http://" + strings.TrimSpace(a.Addr),
			},
			"tools":   supervisorTools(a.ServiceVersion),
			"healthy": &healthy,
		},
		Context: &toolproto.Context{
			RequestID: "reg-" + newID(),
			TraceID:   "tr-" + newID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: a.ServiceID,
			},
		},
	}
	rawResp, statusCode, err := postHubToolCall(a.RegisterURL, a.Bootstrap, callReq, 5*time.Second)
	if err != nil {
		return toolproto.SupervisorRegisterResult{}, err
	}
	if statusCode >= 300 {
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
	}
	registerResp, err := hubsvc.DecodeSupervisorRegisterResult(rawResp)
	if err != nil {
		return toolproto.SupervisorRegisterResult{}, err
	}
	return registerResp, nil
}

func (a *App) startHubHeartbeatGuard() {
	if strings.TrimSpace(a.RegisterURL) == "" || a.Shutdown == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(a.HeartbeatEvery)
		defer ticker.Stop()
		send := func() error {
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  a.ServiceID,
					"instance_id": a.InstanceID,
					"status":      a.executor.CurrentStatus(),
					"healthy":     boolPtr(a.executor.Healthy()),
					"pid":         os.Getpid(),
					"endpoint":    "http://" + strings.TrimSpace(a.Addr),
				},
				Context: &toolproto.Context{
					RequestID: "hb-" + newID(),
					TraceID:   "tr-" + newID(),
					Caller: toolproto.Caller{
						Type:      "service",
						ServiceID: a.ServiceID,
					},
				},
			}
			rawResp, statusCode, err := postHubToolCall(a.RegisterURL, a.Bootstrap, callReq, 2200*time.Millisecond)
			if err != nil {
				return err
			}
			if statusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			}
			return nil
		}
		if err := send(); err != nil {
			a.Shutdown("hub heartbeat failed: " + err.Error())
			return
		}
		for range ticker.C {
			if err := send(); err != nil {
				a.Shutdown("hub heartbeat failed: " + err.Error())
				return
			}
		}
	}()
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest, timeout time.Duration) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(&http.Client{Timeout: timeout}, hubToolCallURL, serviceAuth, req)
}

func supervisorTools(version string) []toolproto.ServiceTool {
	return []toolproto.ServiceTool{
		{
			ToolID:             "service.lifecycle.health",
			Version:            version,
			Description:        "服务健康状况探测",
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
		{
			ToolID:             "service.lifecycle.state.get",
			Version:            version,
			Description:        "获取服务运行时生命周期状态快照",
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
		{
			ToolID:             "service.lifecycle.shutdown",
			Version:            version,
			Description:        "强制停止服务进程",
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
		{
			ToolID:             "account.auth.register",
			Version:            version,
			Description:        "注册新用户账号并初始化基础资料",
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"anonymous", "user"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"username": map[string]any{"type": "string", "description": "用户名"},
					"password": map[string]any{"type": "string", "description": "密码 (最少6位)"},
				},
				"required": []string{"username", "password"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok":      map[string]any{"type": "boolean"},
					"user_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			ToolID:             "account.auth.login",
			Version:            version,
			Description:        "用户登录并获取访问凭证 (Token)",
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"anonymous", "user"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"username": map[string]any{"type": "string"},
					"password": map[string]any{"type": "string"},
				},
				"required": []string{"username", "password"},
			},
			OutputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ok":            map[string]any{"type": "boolean"},
					"access_token":  map[string]any{"type": "string"},
					"refresh_token": map[string]any{"type": "string"},
					"expires_in":    map[string]any{"type": "integer"},
				},
			},
		},
		{
			ToolID:             "account.auth.logout",
			Version:            version,
			Description:        "注销当前登录状态并清除 Cookie",
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"user"},
		},
		{
			ToolID:             "account.auth.me",
			Version:            version,
			Description:        "获取当前登录用户的个人资料",
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"user"},
		},
		{
			ToolID:             "account.auth.password_change",
			Version:            version,
			Description:        "修改当前登录用户的密码",
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"user"},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"old_password": map[string]any{"type": "string"},
					"new_password": map[string]any{"type": "string"},
				},
				"required": []string{"old_password", "new_password"},
			},
		},
		{
			ToolID:             "account.system.keys.get",
			Version:            version,
			Description:        "【系统级】获取用于验证 Token 的公钥列表",
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
		{
			ToolID:             "account.session.dump_active",
			Version:            version,
			Description:        "【系统级】导出当前所有活动的 Session",
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func detectRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd, fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
