package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/observability"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/security"
	"kagent/hub/internal/supervisor"
	"kagent/hub/internal/transport"
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
	if strings.TrimSpace(req.Context.RequestID) == "" {
		req.Context.RequestID = "req_" + app.NewRequestID()
	}
	if strings.TrimSpace(req.Context.TraceID) == "" {
		req.Context.TraceID = "tr_" + app.NewRequestID()
	}
	if token := strings.TrimSpace(r.Header.Get("X-Hub-Service-Token")); token != "" {
		serviceClaims, verr := h.hubPlatform.VerifyServiceSessionToken(token)
		if verr != nil {
			writeToolError(w, http.StatusUnauthorized, toolproto.ErrorCodeUnauthorized, "invalid service session token", req.Context.RequestID, req.Context.TraceID, "", "")
			return
		}
		if !isServiceCallerAllowedTool(req.ToolID) {
			writeToolError(w, http.StatusForbidden, toolproto.ErrorCodeForbidden, "service caller is not allowed for this tool", req.Context.RequestID, req.Context.TraceID, serviceClaims.ServiceID, serviceClaims.InstanceID)
			return
		}
		req.Context.Caller = toolproto.Caller{
			Type:      "service",
			UserID:    "",
			ServiceID: strings.TrimSpace(serviceClaims.ServiceID),
			SurfaceID: "",
		}
	} else {
		claims, err := extractJWTClaims(r, h.authService)
		if err != nil {
			writeToolError(w, http.StatusUnauthorized, toolproto.ErrorCodeUnauthorized, "unauthorized", req.Context.RequestID, req.Context.TraceID, "", "")
			return
		}
		req.Context.Caller = toolproto.Caller{
			Type:      "user",
			UserID:    strings.TrimSpace(claims.UserID),
			ServiceID: "",
			SurfaceID: strings.TrimSpace(req.Context.Caller.SurfaceID),
		}
	}

	services := h.hubPlatform.ListRegisteredServices()
	instances := h.supervisor.List()
	h.router.SyncServices(services)
	selection, ok := h.router.Select(req.ToolID, services, instances)
	if !ok {
		if h.router.HasTool(req.ToolID) {
			writeToolError(w, http.StatusServiceUnavailable, toolproto.ErrorCodeServiceUnavailable, "no ready service instance", req.Context.RequestID, req.Context.TraceID, "", "")
		} else {
			writeToolError(w, http.StatusNotFound, toolproto.ErrorCodeToolNotFound, "tool not found", req.Context.RequestID, req.Context.TraceID, "", "")
		}
		return
	}

	timeout := resolveTimeout(req.Context.TimeoutMS)
	serviceToken, _, err := h.hubPlatform.IssueServiceSessionToken(selection.Service.ServiceID, selection.Instance.InstanceID, 10*time.Minute)
	if err != nil {
		writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, "issue service token failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return
	}

	body, _ := json.Marshal(req)
	headers := security.SanitizeForwardHeaders(r.Header)
	headers.Set("Content-Type", "application/json")
	security.InjectCallerHeaders(headers, req.Context, serviceToken, "")

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

func mapStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func isServiceCallerAllowedTool(toolID string) bool {
	tid := strings.TrimSpace(toolID)
	return strings.HasPrefix(tid, "storage.database.") || strings.HasPrefix(tid, "storage.share.")
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

func extractJWTClaims(r *http.Request, authService *app.AuthService) (app.JWTClaims, error) {
	cookie, err := r.Cookie(app.JWTCookieName)
	if err != nil {
		return app.JWTClaims{}, err
	}
	return authService.ParseJWT(cookie.Value)
}
