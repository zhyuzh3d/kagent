package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/security"
	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"
)

func (h *ToolHandler) HandleCall(w http.ResponseWriter, r *http.Request) {
	req, identity, caller, ctx, ok := h.prepareCallRequest(w, r)
	if !ok {
		return
	}
	if h.handleInternalToolCall(w, req, ctx) {
		return
	}

	selection, ok := h.selectToolForCall(w, req, caller)
	if !ok {
		return
	}

	toolResp, duration, ok := h.forwardToolCall(w, r, &req, identity, selection)
	if !ok {
		return
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

func (h *ToolHandler) prepareCallRequest(w http.ResponseWriter, r *http.Request) (toolproto.CallRequest, app.Identity, toolproto.Caller, context.Context, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeToolError(w, http.StatusMethodNotAllowed, toolproto.ErrorCodeBadRequest, "method not allowed", "", "", "", "")
		return toolproto.CallRequest{}, app.Identity{}, toolproto.Caller{}, nil, false
	}
	var req toolproto.CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeToolError(w, http.StatusBadRequest, toolproto.ErrorCodeBadRequest, "invalid request body", "", "", "", "")
		return toolproto.CallRequest{}, app.Identity{}, toolproto.Caller{}, nil, false
	}
	queryToolID := strings.TrimSpace(r.URL.Query().Get("tool_id"))
	if queryToolID != "" {
		req.ToolID = queryToolID
	}
	normalized, err := toolproto.NormalizeRequest(req)
	if err != nil {
		writeToolError(w, http.StatusBadRequest, toolproto.ErrorCodeBadRequest, err.Error(), "", "", "", "")
		return toolproto.CallRequest{}, app.Identity{}, toolproto.Caller{}, nil, false
	}
	req = normalized
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

	identity := h.resolveRequestIdentity(r)
	ctx := context.WithValue(r.Context(), app.RemoteAddrContextKey, r.RemoteAddr)
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
	req.Context.Caller = caller

	originCaller, originToken, err := h.resolveOriginDelegation(caller, req.Context, "")
	if err != nil {
		writeToolError(w, http.StatusForbidden, toolproto.ErrorCodeForbidden, err.Error(), req.Context.RequestID, req.Context.TraceID, "", "")
		return toolproto.CallRequest{}, app.Identity{}, toolproto.Caller{}, nil, false
	}
	req.Context.OriginCaller = originCaller
	req.Context.OriginToken = originToken
	return req, identity, caller, ctx, true
}

func (h *ToolHandler) handleInternalToolCall(w http.ResponseWriter, req toolproto.CallRequest, ctx context.Context) bool {
	if !strings.HasPrefix(req.ToolID, "hub.") {
		return false
	}
	if fn, ok := h.registry[req.ToolID]; ok {
		resp, err := fn(ctx, req)
		if err != nil {
			writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, err.Error(), req.Context.RequestID, req.Context.TraceID, "hub", "")
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
		return true
	}
	writeToolError(w, http.StatusNotFound, toolproto.ErrorCodeToolNotFound, "internal tool not found", req.Context.RequestID, req.Context.TraceID, "hub", "")
	return true
}

func (h *ToolHandler) selectToolForCall(w http.ResponseWriter, req toolproto.CallRequest, caller toolproto.Caller) (routing.Selection, bool) {
	selection, ok := h.selectTool(req.ToolID)
	if !ok {
		if h.router.HasTool(req.ToolID) {
			writeToolError(w, http.StatusServiceUnavailable, toolproto.ErrorCodeServiceUnavailable, "no ready service instance", req.Context.RequestID, req.Context.TraceID, "", "")
		} else {
			writeToolError(w, http.StatusNotFound, toolproto.ErrorCodeToolNotFound, "tool not found", req.Context.RequestID, req.Context.TraceID, "", "")
		}
		return routing.Selection{}, false
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
		return routing.Selection{}, false
	}
	return selection, true
}

func (h *ToolHandler) forwardToolCall(w http.ResponseWriter, r *http.Request, req *toolproto.CallRequest, identity app.Identity, selection routing.Selection) (toolproto.CallResponse, time.Duration, bool) {
	timeout := resolveTimeout(req.Context.TimeoutMS)
	hubAuthToken, hubAuthInstanceID, err := h.resolveHubAuth(selection.Service.ServiceID, selection.Instance.InstanceID)
	if err != nil {
		writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, "resolve hub auth failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return toolproto.CallResponse{}, 0, false
	}
	req.Context.OriginToken, err = h.hubPlatform.IssueOriginCallerToken(req.Context.OriginCaller, selection.Service.ServiceID, req.Context.RequestID, req.Context.TraceID)
	if err != nil {
		writeToolError(w, http.StatusInternalServerError, toolproto.ErrorCodeInternalError, "issue origin caller token failed", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return toolproto.CallResponse{}, 0, false
	}
	body, _ := json.Marshal(req)
	headers := security.SanitizeForwardHeaders(r.Header)
	headers.Set("Content-Type", "application/json")

	callerReliability := "untrusted"
	if identity.Type == app.IdentityUser || identity.Type == app.IdentityService {
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
		return toolproto.CallResponse{}, 0, false
	}

	duration := time.Since(startedAt)
	var toolResp toolproto.CallResponse
	if err := json.Unmarshal(callResp.Body, &toolResp); err != nil {
		h.supervisor.MarkFailure(selection.Service.ServiceID, selection.Instance.InstanceID)
		h.router.Record(selection, req.Context.RequestID, req.Context.TraceID, req.Context.Caller.Type, callerIdentity(req.Context.Caller), false, toolproto.ErrorCodeToolExecError, duration)
		writeToolError(w, http.StatusBadGateway, toolproto.ErrorCodeToolExecError, "invalid service response", req.Context.RequestID, req.Context.TraceID, selection.Service.ServiceID, selection.Instance.InstanceID)
		return toolproto.CallResponse{}, 0, false
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
	return toolResp, duration, true
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
