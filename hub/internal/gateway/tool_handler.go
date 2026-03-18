package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
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
)

const (
	defaultToolTimeout = 30 * time.Second
	maxToolTimeout     = 120 * time.Second
)

type ToolHandler struct {
	authService *app.AuthService
	hubPlatform *app.HubPlatform
	router      *routing.Engine
	supervisor  *supervisor.Registry
	transport   *transport.Client
	audit       *observability.Store
	endpoints   map[string]transport.Endpoint
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
	}
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
	caller, callerReliability, err := h.resolveCaller(r)
	if err != nil {
		writeToolError(w, http.StatusUnauthorized, toolproto.ErrorCodeUnauthorized, "unauthorized", req.Context.RequestID, req.Context.TraceID, "", "")
		return
	}
	req.Context.Caller = caller

	// 1. Intercept Internal Tools (hub.*)
	if strings.HasPrefix(req.ToolID, "hub.") {
		h.handleInternalTool(w, r, req)
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

	timeout := resolveTimeout(req.Context.TimeoutMS)
	hubAuthToken, hubAuthInstanceID, err := h.resolveHubAuth(selection.Service.ServiceID, selection.Instance.InstanceID)
	if err != nil {
		writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, "resolve hub auth failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}

	body, _ := json.Marshal(req)
	headers := security.SanitizeForwardHeaders(r.Header)
	headers.Set("Content-Type", "application/json")
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
	caller, callerReliability, err := h.resolveCaller(r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
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
	if err := h.proxyWS(w, r, selection.Service.ServiceID, selection.Instance.InstanceID, selection.Instance.Endpoint, wsPath, caller, callerReliability); err != nil {
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
		targetService = "chat-server"
	}
	reg, ok := h.hubPlatform.GetService(targetService)
	if !ok {
		http.Error(w, targetService+" is not registered", http.StatusServiceUnavailable)
		return targetService, "", fmt.Errorf("%s is not registered", targetService)
	}
	if err := h.proxyWS(w, r, reg.ServiceID, reg.InstanceID, reg.Endpoint, "/service/tool/ws", caller, callerReliability); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return reg.ServiceID, reg.InstanceID, err
	}
	return reg.ServiceID, reg.InstanceID, nil
}

func (h *ToolHandler) proxyWS(w http.ResponseWriter, r *http.Request, serviceID string, instanceID string, endpoint string, wsPath string, caller toolproto.Caller, callerReliability string) error {
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
			RequestID: "req_" + app.NewRequestID(),
			TraceID:   "tr_" + app.NewRequestID(),
			Caller:    caller,
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

func (h *ToolHandler) resolveCaller(r *http.Request) (toolproto.Caller, string, error) {
	serviceID, instanceID, serviceAuth := hubsvc.ExtractServiceAuthHeaders(r.Header)
	if serviceID != "" || instanceID != "" || serviceAuth != "" {
		if verified, err := h.hubPlatform.VerifyServiceAuth(serviceID, instanceID, serviceAuth); err == nil {
			return toolproto.Caller{
				Type:      "service",
				UserID:    "",
				ServiceID: strings.TrimSpace(verified.ServiceID),
				SurfaceID: "",
			}, "untrusted", nil
		}
	}
	claims, err := app.ExtractJWTClaims(r, h.authService)
	if err != nil {
		return toolproto.Caller{
			Type: "anonymous",
		}, "untrusted", nil
	}
	return toolproto.Caller{
		Type:      "user",
		UserID:    strings.TrimSpace(claims.UserID),
		ServiceID: "",
		SurfaceID: "",
	}, "trusted", nil
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

func (h *ToolHandler) handleInternalTool(w http.ResponseWriter, r *http.Request, req toolproto.CallRequest) {
	switch req.ToolID {
	case "hub.system.report_log":
		h.handleInternalReportLog(w, r, req)
	default:
		writeToolError(w, http.StatusNotFound, toolproto.ErrorCodeToolNotFound, "internal tool not found", req.Context.RequestID, req.Context.TraceID, "hub", "")
	}
}

func (h *ToolHandler) handleInternalReportLog(w http.ResponseWriter, r *http.Request, req toolproto.CallRequest) {
	var body struct {
		Level   string `json:"level"`
		Module  string `json:"module"`
		Content string `json:"content"`
	}
	raw, _ := json.Marshal(req.Args)
	_ = json.Unmarshal(raw, &body)

	level := strings.ToUpper(strings.TrimSpace(body.Level))
	if level == "" {
		level = "INFO"
	}
	module := strings.TrimSpace(body.Module)
	if module == "" {
		module = "business"
	}
	content := strings.TrimSpace(body.Content)

	identity := app.IdentityFromContext(r.Context())
	tag := "HUB"
	switch identity.Type {
	case app.IdentityService:
		tag = strings.ToUpper(identity.Name)
	case app.IdentityUser:
		tag = "PAGE"
	case app.IdentitySurface:
		tag = "SURF"
	}

	// Category 3: Service:Report:
	prefix := "Service:Report"
	if identity.Type == app.IdentityAnonymous || identity.Type == app.IdentityUser {
		// If it's a user/anonymous tool call to hub.report_log,
		// maybe it's semi-system or fake reporting.
		// We keep it as Service:Report for now as it's a Tool Call.
	}

	app.InfofCtxTag(r.Context(), tag, "%s:%s:%s", prefix, module, content)

	resp := toolproto.CallResponse{
		Ok:     true,
		Result: map[string]any{"ok": true},
		Meta: toolproto.Meta{
			RequestID: req.Context.RequestID,
			TraceID:   req.Context.TraceID,
			ServiceID: "hub",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
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
