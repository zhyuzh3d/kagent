package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/observability"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/security"
	"kagent/hub/internal/supervisor"
	"kagent/hub/internal/transport"
	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"

	"github.com/gorilla/websocket"
)

const (
	defaultToolTimeout = 30 * time.Second
	maxToolTimeout     = 120 * time.Second
)

var effectKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type InternalToolFunc func(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error)
type InternalWSToolFunc func(ctx context.Context, conn *websocket.Conn, req toolproto.CallRequest) error

type ToolHandler struct {
	authService *app.AuthService
	hubPlatform *app.HubPlatform
	router      *routing.Engine
	supervisor  *supervisor.Registry
	transport   *transport.Client
	audit       *observability.Store
	endpoints   map[string]transport.Endpoint
	registry    map[string]InternalToolFunc
	wsRegistry  map[string]InternalWSToolFunc
}

func NewToolHandler(authService *app.AuthService, hubPlatform *app.HubPlatform, router *routing.Engine, supervisorRegistry *supervisor.Registry, transportClient *transport.Client, auditStore *observability.Store, defaultEndpoints map[string]transport.Endpoint) *ToolHandler {
	endpoints := map[string]transport.Endpoint{}
	for serviceID, endpoint := range defaultEndpoints {
		sid := strings.TrimSpace(serviceID)
		if sid == "" {
			continue
		}
		normalized := endpoint
		if strings.TrimSpace(normalized.Transport) == "" {
			normalized.Transport = inferTransport(normalized)
		}
		endpoints[sid] = normalized
	}
	return &ToolHandler{
		authService: authService,
		hubPlatform: hubPlatform,
		router:      router,
		supervisor:  supervisorRegistry,
		transport:   transportClient,
		audit:       auditStore,
		endpoints:   endpoints,
		registry:    map[string]InternalToolFunc{},
		wsRegistry:  map[string]InternalWSToolFunc{},
	}
}

func (h *ToolHandler) RegisterTool(toolID string, fn func(context.Context, toolproto.CallRequest) (toolproto.CallResponse, error)) {
	h.registry[toolID] = fn
}

func (h *ToolHandler) RegisterWSTool(toolID string, fn func(context.Context, *websocket.Conn, toolproto.CallRequest) error) {
	h.wsRegistry[toolID] = fn
}

func (h *ToolHandler) HandleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeToolError(w, http.StatusMethodNotAllowed, toolproto.ErrorCodeBadRequest, "method not allowed", "", "", "", "")
		return
	}
	var req toolproto.CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeToolError(w, http.StatusBadRequest, toolproto.ErrorCodeBadRequest, "invalid request body", "", "", "", "")
		return
	}
	req, err := toolproto.NormalizeRequest(req)
	if err != nil {
		writeToolError(w, http.StatusBadRequest, toolproto.ErrorCodeBadRequest, err.Error(), "", "", "", "")
		return
	}
	if req.Context == nil {
		req.Context = &toolproto.Context{}
	}
	if req.Context.Meta == nil {
		req.Context.Meta = map[string]any{}
	}
	req.Context.Meta["hub_only"] = false
	if strings.TrimSpace(req.Context.RequestID) == "" {
		req.Context.RequestID = "req_" + app.NewRequestID()
	}
	if strings.TrimSpace(req.Context.TraceID) == "" {
		req.Context.TraceID = "tr_" + app.NewRequestID()
	}
	identity := app.IdentityFromContext(r.Context())
	ctx := context.WithValue(r.Context(), app.RemoteAddrContextKey, r.RemoteAddr)

	caller := toolproto.Caller{
		Type:      strings.ToLower(string(identity.Type)),
		UserID:    identity.ID,
		ServiceID: identity.ID, // For services, ID is the serviceID
	}
	if identity.Type != app.IdentityUser {
		caller.UserID = ""
	}
	if identity.Type != app.IdentityService {
		caller.ServiceID = ""
	}
	req.Context.Caller = caller
	originCaller, originToken, err := h.resolveOriginDelegation(caller, req.Context, "")
	if err != nil {
		writeToolError(w, http.StatusForbidden, toolproto.ErrorCodeForbidden, err.Error(), req.Context.RequestID, req.Context.TraceID, "", "")
		return
	}
	req.Context.OriginCaller = originCaller
	req.Context.OriginToken = originToken

	// 1. Intercept Internal Tools (hub.*)
	if strings.HasPrefix(req.ToolID, "hub.") {
		if fn, ok := h.registry[req.ToolID]; ok {
			resp, err := fn(ctx, req)
			if err != nil {
				writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, err.Error(), req.Context.RequestID, req.Context.TraceID, "hub", "")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		writeToolError(w, http.StatusNotFound, toolproto.ErrorCodeToolNotFound, "internal tool not found", req.Context.RequestID, req.Context.TraceID, "hub", "")
		return
	}

	selection, ok := h.selectTool(req.ToolID)
	if !ok {
		if h.router.HasTool(req.ToolID) {
			writeToolError(w, http.StatusServiceUnavailable, toolproto.ErrorCodeServiceUnavailable, "no ready service instance", req.Context.RequestID, req.Context.TraceID, "", "")
		} else {
			writeToolError(w, http.StatusNotFound, toolproto.ErrorCodeToolNotFound, "tool not found", req.Context.RequestID, req.Context.TraceID, "", "")
		}
		return
	}
	toolDescriptor, hasDescriptor := findToolDescriptor(selection.Service.Manifest, req.ToolID)
	if hasDescriptor && !isCallerTypeAllowed(caller.Type, toolDescriptor.AllowedCallerTypes) {
		statusCode := http.StatusForbidden
		errorCode := toolproto.ErrorCodeForbidden
		message := "caller type is not allowed for this tool"
		if strings.EqualFold(strings.TrimSpace(caller.Type), "anonymous") {
			statusCode = http.StatusUnauthorized
			errorCode = toolproto.ErrorCodeUnauthorized
			message = "authentication required"
		}
		writeToolError(w, statusCode, errorCode, message, req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}

	timeout := resolveTimeout(req.Context.TimeoutMS)
	hubAuthToken, hubAuthInstanceID, err := h.resolveHubAuth(selection.Service.ServiceID, selection.Instance.InstanceID)
	if err != nil {
		writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, "resolve hub auth failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}
	req.Context.OriginToken, err = h.hubPlatform.IssueOriginCallerToken(req.Context.OriginCaller, selection.Service.ServiceID, req.Context.RequestID, req.Context.TraceID)
	if err != nil {
		writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, "issue origin caller token failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}

	body, _ := json.Marshal(req)
	headers := security.SanitizeForwardHeaders(r.Header)
	headers.Set("Content-Type", "application/json")

	// Resolve caller reliability for headers
	callerReliability := "untrusted"
	if identity.Type == app.IdentityUser || identity.Type == app.IdentityService {
		// In a real system, we'd check if the token/auth was fully verified.
		// IdentityMiddleware already verified them if they are not ANONYMOUS.
		callerReliability = "trusted"
	}

	security.InjectCallerHeaders(headers, req.Context, callerReliability)
	security.InjectHubAuthHeaders(headers, selection.Service.ServiceID, hubAuthInstanceID, hubAuthToken)

	endpoint := h.resolveEndpoint(selection)
	startedAt := time.Now()
	callResp, err := h.transport.Call(r.Context(), endpoint, http.MethodPost, "/service/tool/exec", headers, body, timeout)
	if err != nil {
		duration := time.Since(startedAt)
		h.supervisor.MarkFailure(selection.Service.ServiceID, selection.Instance.InstanceID)
		h.router.Record(selection, req.Context.RequestID, req.Context.TraceID, req.Context.Caller.Type, callerIdentity(req.Context.Caller), false, toolproto.ErrorCodeServiceUnavailable, duration)
		h.audit.Add("gateway", "tool_call", "service_unavailable", map[string]any{
			"tool_id":     req.ToolID,
			"service_id":  selection.Service.ServiceID,
			"instance_id": selection.Instance.InstanceID,
		})
		writeToolError(w, http.StatusServiceUnavailable, toolproto.ErrorCodeServiceUnavailable, "service call failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}

	duration := time.Since(startedAt)
	var toolResp toolproto.CallResponse
	if err := json.Unmarshal(callResp.Body, &toolResp); err != nil {
		h.supervisor.MarkFailure(selection.Service.ServiceID, selection.Instance.InstanceID)
		h.router.Record(selection, req.Context.RequestID, req.Context.TraceID, req.Context.Caller.Type, callerIdentity(req.Context.Caller), false, toolproto.ErrorCodeToolExecError, duration)
		writeToolError(w, http.StatusBadGateway, toolproto.ErrorCodeToolExecError, "invalid service response", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}

	toolResp.Meta.RequestID = req.Context.RequestID
	toolResp.Meta.TraceID = req.Context.TraceID
	toolResp.Meta.ServiceID = selection.Service.ServiceID
	toolResp.Meta.InstanceID = selection.Instance.InstanceID
	toolResp.Meta.DurationMS = duration.Milliseconds()
	if !toolResp.Ok && toolResp.Error == nil {
		toolResp.Error = &toolproto.Error{
			Code:    toolproto.ErrorCodeToolExecError,
			Message: "service returned failed response without error body",
		}
	}
	if toolResp.Ok {
		h.applyToolEffects(w, r, selection.Service.ServiceID, toolResp.Effects)
		if strings.TrimSpace(selection.Service.ServiceID) == "account" {
			h.syncAccountSessionFromResult(req.ToolID, toolResp.Result, req.Context.Caller)
		}
	}

	success := toolResp.Ok
	errorCode := ""
	if !success && toolResp.Error != nil {
		errorCode = toolResp.Error.Code
	}
	if success {
		h.supervisor.MarkSuccess(selection.Service.ServiceID, selection.Instance.InstanceID)
	} else {
		h.supervisor.MarkFailure(selection.Service.ServiceID, selection.Instance.InstanceID)
	}
	h.router.Record(selection, req.Context.RequestID, req.Context.TraceID, req.Context.Caller.Type, callerIdentity(req.Context.Caller), success, errorCode, duration)
	h.audit.Add("gateway", "tool_call", mapStatus(success), map[string]any{
		"tool_id":     req.ToolID,
		"service_id":  selection.Service.ServiceID,
		"instance_id": selection.Instance.InstanceID,
		"duration_ms": duration.Milliseconds(),
		"error_code":  errorCode,
	})

	statusCode := http.StatusOK
	if !toolResp.Ok && toolResp.Error != nil {
		statusCode = toolproto.HTTPStatusFromCode(toolResp.Error.Code)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(toolResp)
}

func (h *ToolHandler) ProbeServiceTool(ctx context.Context, serviceID string, toolID string, args map[string]any, timeoutMS int) (toolproto.CallResponse, int, error) {
	if h == nil {
		return toolproto.CallResponse{}, 0, fmt.Errorf("tool handler is nil")
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return toolproto.CallResponse{}, 0, fmt.Errorf("service_id is required")
	}
	tid := strings.TrimSpace(toolID)
	if tid == "" {
		return toolproto.CallResponse{}, 0, fmt.Errorf("tool_id is required")
	}
	reg, ok := h.hubPlatform.GetService(sid)
	if !ok {
		return toolproto.CallResponse{}, 0, fmt.Errorf("service not found: %s", sid)
	}
	if strings.TrimSpace(reg.Status) != app.ServiceStatusActive || !reg.Healthy {
		return toolproto.CallResponse{}, 0, fmt.Errorf("service is not healthy: %s", sid)
	}
	instance, hasInstance := pickReadyHealthyInstance(h.supervisor.GetByService(sid))
	if !hasInstance {
		instance = supervisor.Instance{
			ServiceID:  sid,
			InstanceID: strings.TrimSpace(reg.InstanceID),
			Endpoint:   strings.TrimSpace(reg.Endpoint),
			Status:     supervisor.InstanceStatusReady,
			Healthy:    true,
			Transport:  inferTransportFromURL(strings.TrimSpace(reg.Endpoint)),
		}
	}

	if args == nil {
		args = map[string]any{}
	}
	req := toolproto.CallRequest{
		ToolID: tid,
		Args:   args,
		Context: &toolproto.Context{
			RequestID: "req_" + app.NewRequestID(),
			TraceID:   "tr_" + app.NewRequestID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: "hub",
			},
			OriginCaller: toolproto.Caller{
				Type:      "service",
				ServiceID: "hub",
			},
			Meta: map[string]any{
				"hub_only": true,
			},
		},
	}
	req, err := toolproto.NormalizeRequest(req)
	if err != nil {
		return toolproto.CallResponse{}, 0, err
	}
	hubAuthToken, hubAuthInstanceID, err := h.resolveHubAuth(reg.ServiceID, instance.InstanceID)
	if err != nil {
		return toolproto.CallResponse{}, 0, fmt.Errorf("resolve hub auth failed: %w", err)
	}
	req.Context.OriginToken, err = h.hubPlatform.IssueOriginCallerToken(req.Context.OriginCaller, reg.ServiceID, req.Context.RequestID, req.Context.TraceID)
	if err != nil {
		return toolproto.CallResponse{}, 0, fmt.Errorf("issue origin caller token failed: %w", err)
	}
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	security.InjectCallerHeaders(headers, req.Context, "trusted")
	security.InjectHubAuthHeaders(headers, reg.ServiceID, hubAuthInstanceID, hubAuthToken)
	body, _ := json.Marshal(req)
	selection := routing.Selection{
		Service:  reg,
		Instance: instance,
	}
	endpoint := h.resolveEndpoint(selection)
	callResp, err := h.transport.Call(ctx, endpoint, http.MethodPost, "/service/tool/exec", headers, body, resolveTimeout(timeoutMS))
	if err != nil {
		return toolproto.CallResponse{}, 0, err
	}
	var out toolproto.CallResponse
	if err := json.Unmarshal(callResp.Body, &out); err != nil {
		return toolproto.CallResponse{}, 0, fmt.Errorf("invalid probe response: %w", err)
	}
	out.Meta.RequestID = req.Context.RequestID
	out.Meta.TraceID = req.Context.TraceID
	out.Meta.ServiceID = reg.ServiceID
	out.Meta.InstanceID = instance.InstanceID
	statusCode := http.StatusOK
	if !out.Ok && out.Error != nil {
		statusCode = toolproto.HTTPStatusFromCode(out.Error.Code)
	}
	return out, statusCode, nil
}

func (h *ToolHandler) HandleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	identity := app.IdentityFromContext(r.Context())
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

func (h *ToolHandler) resolveHubAuth(serviceID string, instanceID string) (string, string, error) {
	auth, ok := h.hubPlatform.ServiceHubAuth(serviceID)
	if !ok {
		return "", "", fmt.Errorf("missing service auth")
	}
	actualInstanceID := strings.TrimSpace(auth.InstanceID)
	expectedInstanceID := strings.TrimSpace(instanceID)
	if expectedInstanceID != "" && actualInstanceID != expectedInstanceID {
		return "", "", fmt.Errorf("service auth instance mismatch")
	}
	token := strings.TrimSpace(auth.H2SToken)
	if token == "" {
		return "", "", fmt.Errorf("missing hub auth token")
	}
	if actualInstanceID == "" {
		actualInstanceID = expectedInstanceID
	}
	return token, actualInstanceID, nil
}

func (h *ToolHandler) resolveOriginDelegation(caller toolproto.Caller, reqCtx *toolproto.Context, headerToken string) (toolproto.Caller, string, error) {
	origin := caller
	token := strings.TrimSpace(headerToken)
	if reqCtx != nil && strings.TrimSpace(reqCtx.OriginToken) != "" {
		token = strings.TrimSpace(reqCtx.OriginToken)
	}
	if strings.EqualFold(strings.TrimSpace(caller.Type), toolproto.CallerTypeService) && token != "" {
		claims, err := h.hubPlatform.VerifyOriginCallerToken(token, caller.ServiceID)
		if err != nil {
			return toolproto.Caller{}, "", fmt.Errorf("invalid origin caller token: %w", err)
		}
		origin = claims.OriginCaller
		token = strings.TrimSpace(token)
	}
	if strings.TrimSpace(origin.Type) == "" {
		origin = caller
	}
	return origin, token, nil
}

func (h *ToolHandler) selectTool(toolID string) (routing.Selection, bool) {
	services := h.hubPlatform.ListRegisteredServices()
	instances := h.supervisor.List()
	h.router.SyncServices(services)
	return h.router.Select(toolID, services, instances)
}

func findToolWSPath(manifest app.ServiceManifest, toolID string) string {
	for _, tool := range manifest.Provides {
		if strings.TrimSpace(tool.ToolID) != strings.TrimSpace(toolID) {
			continue
		}
		path := strings.TrimSpace(tool.WSPath)
		if path == "" && strings.TrimSpace(tool.Streaming) != "" {
			path = "/service/tool/ws"
		}
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return path
	}
	return ""
}

func findToolDescriptor(manifest app.ServiceManifest, toolID string) (app.ServiceToolDescriptor, bool) {
	target := strings.TrimSpace(toolID)
	if target == "" {
		return app.ServiceToolDescriptor{}, false
	}
	for _, tool := range manifest.Provides {
		if strings.TrimSpace(tool.ToolID) == target {
			return tool, true
		}
	}
	return app.ServiceToolDescriptor{}, false
}

func isCallerTypeAllowed(callerType string, allowedCallerTypes []string) bool {
	if len(allowedCallerTypes) == 0 {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(callerType))
	for _, item := range allowedCallerTypes {
		if target == strings.ToLower(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
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

func (h *ToolHandler) resolveEndpoint(selection routing.Selection) transport.Endpoint {
	instance := selection.Instance
	resolved := transport.Endpoint{
		Transport: strings.TrimSpace(instance.Transport),
	}
	if strings.EqualFold(resolved.Transport, "uds") {
		resolved.UDSPath = strings.TrimSpace(instance.Endpoint)
	} else {
		resolved.TCPURL = strings.TrimSpace(instance.Endpoint)
	}
	defaultEndpoint := h.endpoints[strings.TrimSpace(selection.Service.ServiceID)]
	if strings.TrimSpace(resolved.Transport) == "" {
		resolved.Transport = inferTransport(defaultEndpoint)
	}
	if strings.TrimSpace(resolved.UDSPath) == "" {
		resolved.UDSPath = strings.TrimSpace(defaultEndpoint.UDSPath)
	}
	if strings.TrimSpace(resolved.TCPURL) == "" {
		resolved.TCPURL = strings.TrimSpace(defaultEndpoint.TCPURL)
	}
	if strings.EqualFold(resolved.Transport, "uds") && strings.TrimSpace(resolved.UDSPath) == "" {
		resolved.Transport = "tcp"
	}
	if strings.TrimSpace(resolved.Transport) == "" {
		resolved.Transport = "tcp"
	}
	return resolved
}

func (h *ToolHandler) SyncAccountState(ctx context.Context) error {
	if h == nil {
		return fmt.Errorf("tool handler is nil")
	}
	keysResp, _, err := h.ProbeServiceTool(ctx, "account", "account.system.keys.get", map[string]any{}, 3000)
	if err != nil {
		return fmt.Errorf("probe account.system.keys.get: %w", err)
	}
	if !keysResp.Ok {
		return fmt.Errorf("account.system.keys.get returned not ok")
	}
	keys, keyErr := parseAccountPublicKeys(keysResp.Result)
	if keyErr != nil {
		return keyErr
	}

	sessionsResp, _, err := h.ProbeServiceTool(ctx, "account", "account.session.dump_active", map[string]any{}, 3000)
	if err != nil {
		return fmt.Errorf("probe account.session.dump_active: %w", err)
	}
	if !sessionsResp.Ok {
		return fmt.Errorf("account.session.dump_active returned not ok")
	}
	sessions, sessionErr := parseAccountSessions(sessionsResp.Result)
	if sessionErr != nil {
		return sessionErr
	}
	h.authService.SetAccountPublicKeys(keys)
	h.authService.ReplaceActiveSessions(sessions)
	return nil
}

func parseAccountPublicKeys(result any) ([]app.AccountPublicKey, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal keys result failed: %w", err)
	}
	var wrapper toolproto.AccountPublicKeysResult
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("decode account keys payload failed: %w", err)
	}
	if len(wrapper.Keys) == 0 {
		return nil, fmt.Errorf("invalid account keys payload")
	}
	out := make([]app.AccountPublicKey, 0, len(wrapper.Keys))
	for _, item := range wrapper.Keys {
		out = append(out, app.AccountPublicKey{
			KID:       strings.TrimSpace(item.KID),
			Alg:       strings.TrimSpace(item.Alg),
			PublicKey: strings.TrimSpace(item.PublicKey),
		})
	}
	return out, nil
}

func parseAccountSessions(result any) (map[string]string, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal sessions result failed: %w", err)
	}
	var wrapper toolproto.AccountActiveSessionsResult
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("decode sessions payload failed: %w", err)
	}
	if len(wrapper.Items) == 0 {
		return nil, fmt.Errorf("invalid account sessions payload")
	}
	out := map[string]string{}
	for _, item := range wrapper.Items {
		userID := strings.TrimSpace(item.UserID)
		sessionID := strings.TrimSpace(item.SID)
		if userID == "" || sessionID == "" {
			continue
		}
		out[userID] = sessionID
	}
	return out, nil
}

func (h *ToolHandler) applyToolEffects(w http.ResponseWriter, r *http.Request, serviceID string, effects *toolproto.Effects) {
	if effects == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return
	}
	for _, item := range effects.SetCookies {
		key := strings.TrimSpace(item.Name)
		if !effectKeyPattern.MatchString(key) {
			continue
		}
		maxAge := item.MaxAgeSec
		switch {
		case maxAge < 0:
			maxAge = -1
		case maxAge == 0:
			maxAge = app.JWTMaxAgeSec
		case maxAge > app.JWTMaxAgeSec:
			maxAge = app.JWTMaxAgeSec
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "svc." + sid + "." + key,
			Value:    strings.TrimSpace(item.Value),
			Path:     "/",
			MaxAge:   maxAge,
			SameSite: http.SameSiteStrictMode,
			HttpOnly: true,
			Secure:   r != nil && r.TLS != nil,
		})
	}
	for _, item := range effects.SetHeaders {
		key := strings.TrimSpace(item.Name)
		if !effectKeyPattern.MatchString(key) {
			continue
		}
		headerName := "X-Svc-" + sid + "-" + strings.ReplaceAll(key, "_", "-")
		w.Header().Set(headerName, strings.TrimSpace(item.Value))
	}
}

func (h *ToolHandler) syncAccountSessionFromResult(toolID string, result any, caller toolproto.Caller) {
	if h == nil || h.authService == nil {
		return
	}
	tool := strings.TrimSpace(toolID)
	switch tool {
	case "account.auth.register", "account.auth.login", "account.auth.password_change":
		payload, ok := result.(map[string]any)
		if !ok {
			return
		}
		userID := strings.TrimSpace(asString(payload["user_id"]))
		sid := strings.TrimSpace(asString(payload["sid"]))
		if userID == "" || sid == "" {
			return
		}
		h.authService.SetActiveSession(userID, sid)
	case "account.auth.logout":
		payload, _ := result.(map[string]any)
		userID := ""
		if payload != nil {
			userID = strings.TrimSpace(asString(payload["user_id"]))
		}
		if userID == "" {
			userID = strings.TrimSpace(caller.UserID)
		}
		if userID != "" {
			h.authService.SetActiveSession(userID, "")
		}
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

func resolveTimeout(timeoutMS int) time.Duration {
	timeout := defaultToolTimeout
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
		if timeout > maxToolTimeout {
			timeout = maxToolTimeout
		}
	}
	return timeout
}

func inferTransport(endpoint transport.Endpoint) string {
	if strings.TrimSpace(endpoint.Transport) != "" {
		return strings.TrimSpace(endpoint.Transport)
	}
	if strings.TrimSpace(endpoint.UDSPath) != "" {
		return "uds"
	}
	return "tcp"
}

func inferTransportFromURL(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "unix://") || strings.HasPrefix(value, "uds://") {
		return "uds"
	}
	if strings.HasPrefix(value, "/") {
		return "uds"
	}
	return "tcp"
}

func mapStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func pickReadyHealthyInstance(instances []supervisor.Instance) (supervisor.Instance, bool) {
	for _, instance := range instances {
		if strings.TrimSpace(instance.Status) != supervisor.InstanceStatusReady {
			continue
		}
		if !instance.Healthy {
			continue
		}
		return instance, true
	}
	return supervisor.Instance{}, false
}

func callerIdentity(caller toolproto.Caller) string {
	switch strings.ToLower(strings.TrimSpace(caller.Type)) {
	case "service":
		return strings.TrimSpace(caller.ServiceID)
	case "surface":
		if sid := strings.TrimSpace(caller.SurfaceID); sid != "" {
			return sid
		}
		return strings.TrimSpace(caller.UserID)
	default:
		return strings.TrimSpace(caller.UserID)
	}
}

func writeToolError(w http.ResponseWriter, statusCode int, code string, message string, requestID string, traceID string, serviceID string, instanceID string) {
	resp := toolproto.CallResponse{
		Ok:     false,
		Result: nil,
		Error: &toolproto.Error{
			Code:    strings.TrimSpace(code),
			Message: strings.TrimSpace(message),
		},
		Meta: toolproto.Meta{
			RequestID:  strings.TrimSpace(requestID),
			TraceID:    strings.TrimSpace(traceID),
			ServiceID:  strings.TrimSpace(serviceID),
			InstanceID: strings.TrimSpace(instanceID),
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}
