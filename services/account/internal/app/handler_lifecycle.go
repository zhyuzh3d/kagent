package app

import (
	"context"
	"os"
	"strings"
	"time"

	"kagent/pkg/toolproto"
)

func (h *Handler) handleHealth(req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	if !isSelfServiceCaller(req.Context.Caller) {
		return forbidden(resp, "forbidden")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"ok":          true,
		"service_id":  h.serviceID,
		"instance_id": h.instance,
		"pid":         os.Getpid(),
		"endpoint":    h.endpoint,
	}
	return resp
}

func (h *Handler) handleStateGet(req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	if !isSelfServiceCaller(req.Context.Caller) {
		return forbidden(resp, "forbidden")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"service_id":      h.serviceID,
		"instance_id":     h.instance,
		"pid":             os.Getpid(),
		"endpoint":        h.endpoint,
		"healthy":         h.Healthy(),
		"status":          h.CurrentStatus(),
		"ready":           h.Ready(),
		"initialized":     h.Ready(),
		"last_init_error": h.LastInitError(),
		"timestamp_ms":    time.Now().UnixMilli(),
	}
	return resp
}

func (h *Handler) Initialize(ctx context.Context) error {
	h.mu.Lock()
	if h.ready {
		h.mu.Unlock()
		return nil
	}
	if h.initing {
		h.mu.Unlock()
		return nil
	}
	h.initing = true
	h.lastInitErr = ""
	initFn := h.initFn
	h.mu.Unlock()

	signingKey, err := initFn(ctx)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.initing = false
	if err != nil {
		h.lastInitErr = err.Error()
		return err
	}
	h.signing = signingKey
	h.ready = true
	h.lastInitErr = ""
	return nil
}

func (h *Handler) handleShutdown(req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	if !isSelfServiceCaller(req.Context.Caller) {
		return forbidden(resp, "forbidden")
	}
	h.mu.RLock()
	fn := h.shutdown
	h.mu.RUnlock()
	if fn != nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			fn("shutdown requested via tool")
		}()
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{"ok": true, "message": "shutting_down"}
	return resp
}

func isSelfServiceCaller(caller toolproto.Caller) bool {
	return strings.EqualFold(strings.TrimSpace(caller.Type), "service") && strings.TrimSpace(caller.ServiceID) == serviceID
}

func (h *Handler) Ready() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ready
}

func (h *Handler) Healthy() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return strings.TrimSpace(h.lastInitErr) == ""
}

func (h *Handler) CurrentStatus() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	switch {
	case h.ready:
		return "ready"
	case h.initing:
		return "initializing"
	case strings.TrimSpace(h.lastInitErr) != "":
		return "failed"
	default:
		return "registered"
	}
}

func (h *Handler) LastInitError() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return strings.TrimSpace(h.lastInitErr)
}

func (h *Handler) handleKeysGet(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	if !hubAccessAllowed(req.Context.Caller, req.Context) {
		return forbidden(resp, "forbidden")
	}
	keys, err := h.store.ListPublicKeys(ctx)
	if err != nil {
		return internalError(resp, "query keys failed")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = toolproto.AccountPublicKeysResult{Keys: keys}
	return resp
}

func (h *Handler) handleDumpActive(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	if !hubAccessAllowed(req.Context.Caller, req.Context) {
		return forbidden(resp, "forbidden")
	}
	items, err := h.store.ListActiveSessions(ctx)
	if err != nil {
		return internalError(resp, "dump sessions failed")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = toolproto.AccountActiveSessionsResult{Items: items}
	return resp
}

func hubAccessAllowed(caller toolproto.Caller, ctx *toolproto.Context) bool {
	if isHubOnlyContext(ctx) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(caller.Type), "service") && strings.TrimSpace(caller.ServiceID) == "hub"
}

func isHubOnlyContext(ctx *toolproto.Context) bool {
	if ctx == nil || ctx.Meta == nil {
		return false
	}
	raw, ok := ctx.Meta["hub_only"]
	if !ok {
		return false
	}
	switch tv := raw.(type) {
	case bool:
		return tv
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}
