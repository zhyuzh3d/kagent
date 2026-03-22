package gateway

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"

	"github.com/gorilla/websocket"
)

const surfaceReloadQueryKey = "_surface_reload"

var (
	htmlAssetURLPattern    = regexp.MustCompile(`(?i)\b(href|src)\s*=\s*(['"])([^"'<>]+)(['"])`)
	jsImportFromPattern    = regexp.MustCompile(`(?m)(\bfrom\s*)(['"])([^'"]+)(['"])`)
	jsImportBarePattern    = regexp.MustCompile(`(?m)(\bimport\s*)(['"])([^'"]+)(['"])`)
	jsImportDynamicPattern = regexp.MustCompile(`(?m)(\bimport\s*\(\s*)(['"])([^'"]+)(['"])(\s*\))`)
	cssAssetURLPattern     = regexp.MustCompile(`url\(\s*(['"]?)([^'")]+)(['"]?)\s*\)`)
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
	if strings.HasPrefix(r.URL.Path, "/surface/") {
		h.serveSurfaceStaticNoCache(w, r)
		return
	}
	http.FileServer(http.Dir(h.webuiRoot)).ServeHTTP(w, r)
}

// UpdateLifecycleManager updates the lifecycle manager reference.
func (h *SystemHandler) UpdateLifecycleManager(manager *supervisor.LifecycleManager) {
	h.lifecycleManager = manager
}

func (h *SystemHandler) serveSurfaceStaticNoCache(w http.ResponseWriter, r *http.Request) {
	resolvedPath, info, err := resolveStaticPathNoRedirect(h.webuiRoot, r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	reloadToken := strings.TrimSpace(r.URL.Query().Get(surfaceReloadQueryKey))
	if rewritten, contentType, ok := rewriteSurfaceResponse(resolvedPath, reloadToken); ok {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rewritten)
		return
	}

	if contentType := mime.TypeByExtension(filepath.Ext(resolvedPath)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	http.ServeContent(w, r, info.Name(), time.Time{}, file)
}

func resolveStaticPathNoRedirect(root string, requestPath string) (string, os.FileInfo, error) {
	cleanPath := path.Clean("/" + strings.TrimSpace(requestPath))
	if cleanPath == "/" {
		return "", nil, fmt.Errorf("empty static path")
	}
	relativePath := strings.TrimPrefix(cleanPath, "/")
	resolvedRoot := filepath.Clean(root)
	resolvedPath := filepath.Join(resolvedRoot, filepath.FromSlash(relativePath))
	if rel, err := filepath.Rel(resolvedRoot, resolvedPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("static path escapes root")
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", nil, err
	}
	if info.IsDir() {
		resolvedPath = filepath.Join(resolvedPath, "index.html")
		info, err = os.Stat(resolvedPath)
		if err != nil {
			return "", nil, err
		}
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("static path is not a regular file")
	}
	return resolvedPath, info, nil
}

func rewriteSurfaceResponse(resolvedPath string, reloadToken string) ([]byte, string, bool) {
	if strings.TrimSpace(reloadToken) == "" {
		return nil, "", false
	}
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, "", false
	}
	switch strings.ToLower(filepath.Ext(resolvedPath)) {
	case ".html":
		return []byte(rewriteHTMLAssetURLs(string(raw), reloadToken)), "text/html; charset=utf-8", true
	case ".js", ".mjs":
		return []byte(rewriteJSImportSpecifiers(string(raw), reloadToken)), "text/javascript; charset=utf-8", true
	case ".css":
		return []byte(rewriteCSSAssetURLs(string(raw), reloadToken)), "text/css; charset=utf-8", true
	default:
		return nil, "", false
	}
}

func rewriteHTMLAssetURLs(raw string, reloadToken string) string {
	return htmlAssetURLPattern.ReplaceAllStringFunc(raw, func(match string) string {
		parts := htmlAssetURLPattern.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		return parts[1] + "=" + parts[2] + appendSurfaceReloadParam(parts[3], reloadToken) + parts[4]
	})
}

func rewriteJSImportSpecifiers(raw string, reloadToken string) string {
	rewritten := jsImportFromPattern.ReplaceAllStringFunc(raw, func(match string) string {
		parts := jsImportFromPattern.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		return parts[1] + parts[2] + appendSurfaceReloadParam(parts[3], reloadToken) + parts[4]
	})
	rewritten = jsImportBarePattern.ReplaceAllStringFunc(rewritten, func(match string) string {
		parts := jsImportBarePattern.FindStringSubmatch(match)
		if len(parts) != 5 {
			return match
		}
		trimmedPrefix := strings.TrimSpace(parts[1])
		if trimmedPrefix != "import" {
			return match
		}
		return parts[1] + parts[2] + appendSurfaceReloadParam(parts[3], reloadToken) + parts[4]
	})
	return jsImportDynamicPattern.ReplaceAllStringFunc(rewritten, func(match string) string {
		parts := jsImportDynamicPattern.FindStringSubmatch(match)
		if len(parts) != 6 {
			return match
		}
		return parts[1] + parts[2] + appendSurfaceReloadParam(parts[3], reloadToken) + parts[4] + parts[5]
	})
}

func rewriteCSSAssetURLs(raw string, reloadToken string) string {
	return cssAssetURLPattern.ReplaceAllStringFunc(raw, func(match string) string {
		parts := cssAssetURLPattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		quote := parts[1]
		if quote == "" {
			return "url(" + appendSurfaceReloadParam(parts[2], reloadToken) + ")"
		}
		return "url(" + quote + appendSurfaceReloadParam(parts[2], reloadToken) + parts[3] + ")"
	})
}

func appendSurfaceReloadParam(rawURL string, reloadToken string) string {
	target := strings.TrimSpace(rawURL)
	if target == "" || strings.TrimSpace(reloadToken) == "" {
		return target
	}
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "blob:") ||
		strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(lower, "#") ||
		strings.HasPrefix(lower, "/") {
		return target
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return target
	}
	query := parsed.Query()
	query.Set(surfaceReloadQueryKey, reloadToken)
	parsed.RawQuery = query.Encode()
	var buf bytes.Buffer
	buf.WriteString(parsed.Path)
	if parsed.RawQuery != "" {
		buf.WriteByte('?')
		buf.WriteString(parsed.RawQuery)
	}
	if parsed.Fragment != "" {
		buf.WriteByte('#')
		buf.WriteString(parsed.Fragment)
	}
	return buf.String()
}
