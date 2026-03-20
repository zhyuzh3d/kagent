package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"

	"github.com/gorilla/websocket"
)

// SystemHandler handles Auth, Debug, Version, Config, Healthz, Smoke-test, and Shutdown endpoints.
type SystemHandler struct {
	hubPlatform      *app.HubPlatform
	version          *app.VersionInfo
	lifecycleManager *supervisor.LifecycleManager
	webuiRoot        string
	addr             string

	// Server and AppCancel are set by main.go after initialization
	Server    *http.Server
	AppCancel context.CancelFunc
}

// NewSystemHandler creates a new SystemHandler with required dependencies.
func NewSystemHandler(
	hubPlatform *app.HubPlatform,
	version *app.VersionInfo,
	lifecycleManager *supervisor.LifecycleManager,
	webuiRoot string,
	addr string,
) *SystemHandler {
	return &SystemHandler{
		hubPlatform:      hubPlatform,
		version:          version,
		lifecycleManager: lifecycleManager,
		webuiRoot:        webuiRoot,
		addr:             addr,
	}
}

func (h *SystemHandler) RegisterTools(th interface {
	RegisterTool(toolID string, fn func(context.Context, toolproto.CallRequest) (toolproto.CallResponse, error))
	RegisterWSTool(toolID string, fn func(context.Context, *websocket.Conn, toolproto.CallRequest) error)
}) {
	if th == nil {
		return
	}
	th.RegisterTool("hub.system.version.get", h.handleVersionTool)
	th.RegisterTool("hub.system.state.get", h.handleStateGetTool)
	th.RegisterTool("hub.system.smoke.test", h.handleSmokeTestTool)
	th.RegisterTool("hub.system.report_log", h.handleReportLogTool)
	th.RegisterTool("hub.system.health", h.handleHealthTool)
	th.RegisterTool("hub.system.shutdown", h.handleShutdownTool)
	th.RegisterWSTool("hub.system.logs.subscribe", h.handleLogsSubscribeWSTool)
}

func (h *SystemHandler) handleLogsSubscribeWSTool(ctx context.Context, conn *websocket.Conn, req toolproto.CallRequest) error {
	identity := app.IdentityFromContext(ctx)
	if identity.Type != app.IdentityUser {
		return fmt.Errorf("logs subscription restricted to users")
	}

	ch, unsubscribe := app.SubscribeLogs()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case entry, ok := <-ch:
			if !ok {
				return nil
			}
			if err := conn.WriteJSON(entry); err != nil {
				return err
			}
		}
	}
}

func (h *SystemHandler) handleVersionTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	return toolproto.CallResponse{
		Ok:     true,
		Result: h.version,
	}, nil
}

func (h *SystemHandler) handleStateGetTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	services := h.hubPlatform.ListServices()
	tools := h.hubPlatform.ListTools()
	bindings := h.hubPlatform.ListBindings()
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"status":        "ready",
			"healthy":       true,
			"timestamp_ms":  time.Now().UnixMilli(),
			"version":       h.version,
			"service_count": len(services),
			"tool_count":    len(tools),
			"binding_count": len(bindings),
			"services":      services,
			"tools":         tools,
			"bindings":      bindings,
		},
	}, nil
}

func (h *SystemHandler) handleSmokeTestTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	// Smoke test should be restricted to loopback OR special admin user
	// For now, we allow it to be called via any tool port but we check for loopback in the handler if needed.
	// Actually, if it's via tool port, it's reached the Hub.
	tester := app.NewSmokeTester(h.addr)
	result, err := tester.Run(ctx)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{
		Ok:     result.Ok,
		Result: result,
	}, nil
}

func (h *SystemHandler) handleReportLogTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	body := parseReportLogArgs(req.Args)

	identity := app.IdentityFromContext(ctx)
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
		page = identity.Name
	}
	if page == "" {
		page = strings.ToLower(tag)
	}

	app.InfofCtxTag(ctx, tag, "Service:Report:%s:%s:%s", page, body.Module, strings.ReplaceAll(body.Content, ": ", ":"))
	return toolproto.CallResponse{
		Ok:     true,
		Result: map[string]any{"ok": true},
	}, nil
}

type reportLogArgs struct {
	Level   string
	Module  string
	Content string
	Page    string
}

func parseReportLogArgs(args map[string]any) reportLogArgs {
	if args == nil {
		return reportLogArgs{}
	}
	return reportLogArgs{
		Level:   asReportLogString(args["level"]),
		Module:  asReportLogString(args["module"]),
		Content: asReportLogString(args["content"]),
		Page:    asReportLogString(args["page"]),
	}
}

func asReportLogString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func (h *SystemHandler) handleHealthTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"ok":           true,
			"timestamp_ms": time.Now().UnixMilli(),
		},
	}, nil
}

func (h *SystemHandler) handleShutdownTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	remoteAddr, _ := ctx.Value(app.RemoteAddrContextKey).(string)
	if !app.IsLoopbackRemoteAddr(remoteAddr) {
		return toolproto.CallResponse{}, fmt.Errorf("shutdown restricted to loopback")
	}

	app.WarnfCtx(ctx, "shutdown requested from %s via tool", remoteAddr)
	go func() {
		time.Sleep(100 * time.Millisecond)
		if h.lifecycleManager != nil {
			h.lifecycleManager.StopAll(7 * time.Second)
		}
		supervisor.BroadcastServiceShutdown(h.hubPlatform, 7*time.Second)
		if h.AppCancel != nil {
			h.AppCancel()
		}
		if h.Server != nil {
			_ = h.Server.Close()
			sctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			_ = h.Server.Shutdown(sctx)
			cancel()
		}
		time.Sleep(80 * time.Millisecond)
		os.Exit(0)
	}()

	return toolproto.CallResponse{
		Ok:     true,
		Result: map[string]any{"message": "shutting down"},
	}, nil
}

// --- System Handlers ---

// HandleStaticFiles handles static web files and redirects root to chat.
func (h *SystemHandler) HandleStaticFiles(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/page/chat/", http.StatusFound)
		return
	}
	http.FileServer(http.Dir(h.webuiRoot)).ServeHTTP(w, r)
}

// UpdateLifecycleManager updates the lifecycle manager reference.
func (h *SystemHandler) UpdateLifecycleManager(manager *supervisor.LifecycleManager) {
	h.lifecycleManager = manager
}
