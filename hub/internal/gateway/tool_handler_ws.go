package gateway

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/security"
	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"

	"github.com/gorilla/websocket"
)

func (h *ToolHandler) HandleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity := h.resolveRequestIdentity(r)
	caller := toolproto.Caller{
		Type:      strings.ToLower(string(identity.Type)),
		UserID:    identity.ID,
		ServiceID: identity.ID,
	}
	if identity.Type != app.IdentityUser {
		caller.UserID = ""
	}
	if identity.Type != app.IdentityService {
		caller.ServiceID = ""
	}
	originCaller, originToken, err := h.resolveOriginDelegation(caller, nil, hubsvc.OriginCallerTokenFromHeaders(r.Header))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	callerReliability := "untrusted"
	if identity.Type != app.IdentityAnonymous {
		callerReliability = "trusted"
	}

	toolID := strings.TrimSpace(r.URL.Query().Get("tool_id"))
	if toolID == "" {
		startedAt := time.Now()
		serviceID, instanceID, proxyErr := h.handleLegacyWS(w, r, caller, callerReliability)
		fields := map[string]any{
			"tool_id":     "",
			"service_id":  strings.TrimSpace(serviceID),
			"instance_id": strings.TrimSpace(instanceID),
			"caller_type": strings.TrimSpace(caller.Type),
			"legacy":      true,
			"duration_ms": time.Since(startedAt).Milliseconds(),
		}
		if proxyErr != nil {
			fields["error"] = proxyErr.Error()
			h.audit.Add("gateway", "tool_ws_close", "error", fields)
		} else {
			h.audit.Add("gateway", "tool_ws_close", "ok", fields)
		}
		return
	}

	if strings.HasPrefix(toolID, "hub.") {
		if fn, ok := h.wsRegistry[toolID]; ok {
			upgrader := websocket.Upgrader{
				CheckOrigin: func(r *http.Request) bool { return true },
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			req := toolproto.CallRequest{
				ToolID: toolID,
				Context: &toolproto.Context{
					RequestID:    "req_" + app.NewRequestID(),
					TraceID:      "tr_" + app.NewRequestID(),
					Caller:       caller,
					OriginCaller: originCaller,
					OriginToken:  originToken,
				},
			}
			if err := fn(r.Context(), conn, req); err != nil {
				h.audit.Add("gateway", "tool_ws_close", "error", map[string]any{
					"tool_id":     toolID,
					"service_id":  "hub",
					"caller_type": caller.Type,
					"error":       err.Error(),
				})
			}
			return
		}
	}

	selection, ok := h.selectTool(toolID)
	if !ok {
		if h.router.HasTool(toolID) {
			http.Error(w, "no ready service instance", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "tool not found", http.StatusNotFound)
		}
		return
	}
	wsPath := findToolWSPath(selection.Service.Manifest, toolID)
	if wsPath == "" {
		http.Error(w, "streaming path not configured", http.StatusBadGateway)
		return
	}
	startedAt := time.Now()
	serviceOriginToken, err := h.hubPlatform.IssueOriginCallerToken(originCaller, selection.Service.ServiceID, "req_"+app.NewRequestID(), "tr_"+app.NewRequestID())
	if err != nil {
		http.Error(w, "issue origin caller token failed", http.StatusInternalServerError)
		return
	}
	if err := h.proxyWS(w, r, selection.Service.ServiceID, selection.Instance.InstanceID, selection.Instance.Endpoint, wsPath, caller, originCaller, serviceOriginToken, callerReliability); err != nil {
		h.audit.Add("gateway", "tool_ws_close", "error", map[string]any{
			"tool_id":     toolID,
			"service_id":  selection.Service.ServiceID,
			"instance_id": selection.Instance.InstanceID,
			"caller_type": caller.Type,
			"duration_ms": time.Since(startedAt).Milliseconds(),
			"error":       err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.audit.Add("gateway", "tool_ws_close", "ok", map[string]any{
		"tool_id":     toolID,
		"service_id":  selection.Service.ServiceID,
		"instance_id": selection.Instance.InstanceID,
		"caller_type": caller.Type,
		"duration_ms": time.Since(startedAt).Milliseconds(),
	})
}

func (h *ToolHandler) handleLegacyWS(w http.ResponseWriter, r *http.Request, caller toolproto.Caller, callerReliability string) (string, string, error) {
	targetService := strings.TrimSpace(r.URL.Query().Get("service_id"))
	if targetService == "" {
		targetService = "chat_server"
	}
	reg, ok := h.hubPlatform.GetService(targetService)
	if !ok {
		http.Error(w, targetService+" is not registered", http.StatusServiceUnavailable)
		return targetService, "", fmt.Errorf("%s is not registered", targetService)
	}
	originCaller, _, err := h.resolveOriginDelegation(caller, nil, hubsvc.OriginCallerTokenFromHeaders(r.Header))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return reg.ServiceID, reg.InstanceID, err
	}
	serviceOriginToken, err := h.hubPlatform.IssueOriginCallerToken(originCaller, reg.ServiceID, "req_"+app.NewRequestID(), "tr_"+app.NewRequestID())
	if err != nil {
		http.Error(w, "issue origin caller token failed", http.StatusInternalServerError)
		return reg.ServiceID, reg.InstanceID, err
	}
	if err := h.proxyWS(w, r, reg.ServiceID, reg.InstanceID, reg.Endpoint, "/service/tool/ws", caller, originCaller, serviceOriginToken, callerReliability); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return reg.ServiceID, reg.InstanceID, err
	}
	return reg.ServiceID, reg.InstanceID, nil
}

func (h *ToolHandler) proxyWS(w http.ResponseWriter, r *http.Request, serviceID string, instanceID string, endpoint string, wsPath string, caller toolproto.Caller, originCaller toolproto.Caller, originToken string, callerReliability string) error {
	hubAuthToken, hubAuthInstanceID, err := h.resolveHubAuth(serviceID, instanceID)
	if err != nil {
		return fmt.Errorf("resolve hub auth failed")
	}
	targetURL, err := parseServiceURL(endpoint)
	if err != nil {
		return fmt.Errorf("invalid target endpoint")
	}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	originDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originDirector(req)
		req.URL.Path = wsPath
		req.Host = targetURL.Host
		headers := security.SanitizeForwardHeaders(req.Header)
		security.InjectCallerHeaders(headers, &toolproto.Context{
			RequestID:    "req_" + app.NewRequestID(),
			TraceID:      "tr_" + app.NewRequestID(),
			Caller:       caller,
			OriginCaller: originCaller,
			OriginToken:  originToken,
		}, callerReliability)
		security.InjectHubAuthHeaders(headers, serviceID, hubAuthInstanceID, hubAuthToken)
		req.Header = headers
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		h.supervisor.MarkFailure(serviceID, instanceID)
		http.Error(rw, "tool ws proxy failed", http.StatusBadGateway)
	}
	proxy.FlushInterval = -1
	proxy.ServeHTTP(w, r)
	return nil
}

func findToolWSPath(manifest app.ServiceManifest, toolID string) string {
	for _, tool := range manifest.Provides {
		if strings.TrimSpace(tool.ToolID) != strings.TrimSpace(toolID) {
			continue
		}
		path := strings.TrimSpace(tool.WSPath)
		if path == "" && tool.Streaming {
			path = "/service/tool/ws"
		}
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return path
	}
	return ""
}

func parseServiceURL(endpoint string) (*url.URL, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, fmt.Errorf("empty endpoint")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}
