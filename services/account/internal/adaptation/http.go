package adaptation

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	"kagent/services/account/internal/business"
)

type Executor interface {
	HandleTool(context.Context, toolproto.CallRequest) (toolproto.CallResponse, error)
}

type Handler struct {
	serviceID  string
	instanceID string
	bootstrap  hubsvc.BootstrapSecret
	executor   Executor
}

func New(serviceID string, instanceID string, bootstrap hubsvc.BootstrapSecret, executor Executor) *Handler {
	return &Handler{
		serviceID:  strings.TrimSpace(serviceID),
		instanceID: strings.TrimSpace(instanceID),
		bootstrap:  bootstrap,
		executor:   executor,
	}
}

func (h *Handler) HandleToolExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeToolResponse(w, http.StatusMethodNotAllowed, toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeBadRequest,
				Message: "method not allowed",
			},
		})
		return
	}
	if err := hubsvc.VerifyHubAuthHeaders(r.Header, h.serviceID, h.instanceID, h.bootstrap.H2SToken); err != nil {
		writeToolResponse(w, http.StatusForbidden, toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeForbidden,
				Message: "invalid hub auth",
			},
		})
		return
	}
	var req toolproto.CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeBadRequest,
				Message: "invalid request body",
			},
		})
		return
	}
	req, err := toolproto.NormalizeRequest(req)
	if err != nil {
		writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeBadRequest,
				Message: err.Error(),
			},
		})
		return
	}
	if req.Context == nil {
		req.Context = &toolproto.Context{}
	}
	if req.Context.Meta == nil {
		req.Context.Meta = map[string]any{}
	}
	if _, ok := req.Context.Meta["hub_only"]; !ok {
		req.Context.Meta["hub_only"] = false
	}
	req.Context.Caller = resolveCaller(r, req.Context)
	startedAt := time.Now()
	resp, err := h.executor.HandleTool(r.Context(), req)
	if err != nil {
		writeToolResponse(w, http.StatusInternalServerError, toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeInternalError,
				Message: err.Error(),
			},
			Meta: responseMeta(req.Context, h.serviceID, h.instanceID),
		})
		return
	}
	resp.Meta.RequestID = strings.TrimSpace(req.Context.RequestID)
	resp.Meta.TraceID = strings.TrimSpace(req.Context.TraceID)
	resp.Meta.ServiceID = h.serviceID
	resp.Meta.InstanceID = h.instanceID
	if resp.Meta.DurationMS <= 0 {
		resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
	}
	statusCode := http.StatusOK
	if !resp.Ok && resp.Error != nil {
		statusCode = toolproto.HTTPStatusFromCode(resp.Error.Code)
	}
	writeToolResponse(w, statusCode, resp)
}

func resolveCaller(r *http.Request, ctx *toolproto.Context) toolproto.Caller {
	caller := toolproto.Caller{
		Type:      strings.ToLower(strings.TrimSpace(r.Header.Get("X-Caller-Type"))),
		UserID:    strings.TrimSpace(r.Header.Get("X-Caller-User-Id")),
		ServiceID: strings.TrimSpace(r.Header.Get("X-Caller-Service-Id")),
		SurfaceID: strings.TrimSpace(r.Header.Get("X-Caller-Surface-Id")),
	}
	if caller.Type == "" && ctx != nil {
		caller.Type = strings.ToLower(strings.TrimSpace(ctx.Caller.Type))
		caller.UserID = strings.TrimSpace(ctx.Caller.UserID)
		caller.ServiceID = strings.TrimSpace(ctx.Caller.ServiceID)
		caller.SurfaceID = strings.TrimSpace(ctx.Caller.SurfaceID)
	}
	if caller.Type == "" {
		caller.Type = "anonymous"
	}
	return caller
}

func writeToolResponse(w http.ResponseWriter, statusCode int, resp toolproto.CallResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}

func responseMeta(ctx *toolproto.Context, serviceID string, instanceID string) toolproto.Meta {
	meta := toolproto.Meta{
		ServiceID:  strings.TrimSpace(serviceID),
		InstanceID: strings.TrimSpace(instanceID),
	}
	if ctx != nil {
		meta.RequestID = strings.TrimSpace(ctx.RequestID)
		meta.TraceID = strings.TrimSpace(ctx.TraceID)
	}
	return meta
}

var _ Executor = (*business.Handler)(nil)
