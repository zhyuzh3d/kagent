package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"kagent/pkg/toolproto"
)

const (
	passwordMinLen      = 6
	tokenMaxAgeSec      = 30 * 24 * 3600
	tokenCookieName     = "token"
	defaultResponseCode = toolproto.ErrorCodeToolExecError
)

type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username,omitempty"`
	SID      string `json:"sid"`
	IatMS    int64  `json:"iat_ms"`
	ExpMS    int64  `json:"exp_ms"`
	KID      string `json:"kid"`
}

type Handler struct {
	store       Store
	signing     SigningKey
	serviceID   string
	instance    string
	endpoint    string
	initFn      func(context.Context) (SigningKey, error)
	ready       bool
	initing     bool
	lastInitErr string

	mu       sync.RWMutex
	shutdown func(reason string)
}

func NewHandler(store Store, instanceID string, endpoint string, initFn func(context.Context) (SigningKey, error)) *Handler {
	return &Handler{
		store:     store,
		serviceID: serviceID,
		instance:  strings.TrimSpace(instanceID),
		endpoint:  strings.TrimSpace(endpoint),
		initFn:    initFn,
	}
}

func (h *Handler) SetShutdown(fn func(reason string)) {
	h.mu.Lock()
	h.shutdown = fn
	h.mu.Unlock()
}

func (h *Handler) HandleTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	startedAt := time.Now()
	resp := toolproto.CallResponse{
		Ok:    false,
		Meta:  responseMeta(req.Context, h.serviceID, h.instance),
		Error: &toolproto.Error{Code: defaultResponseCode, Message: "tool execution failed"},
	}
	switch req.ToolID {
	case "account.auth.register":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handleRegister(ctx, req)
	case "account.auth.login":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handleLogin(ctx, req)
	case "account.auth.logout":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handleLogout(ctx, req)
	case "account.auth.me":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handleMe(ctx, req)
	case "account.auth.password_change":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handlePasswordChange(ctx, req)
	case "service.lifecycle.health":
		resp = h.handleHealth(req)
	case "service.lifecycle.state.get":
		resp = h.handleStateGet(req)
	case "service.lifecycle.shutdown":
		resp = h.handleShutdown(req)
	case "account.system.keys.get":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handleKeysGet(ctx, req)
	case "account.session.dump_active":
		if !h.Ready() {
			resp = serviceUnavailable(resp, "service not initialized")
			break
		}
		resp = h.handleDumpActive(ctx, req)
	default:
		resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeToolNotFound, Message: "tool not found"}
	}
	resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
	if !resp.Ok && resp.Error == nil {
		resp.Error = &toolproto.Error{Code: defaultResponseCode, Message: "tool execution failed"}
	}
	return resp, nil
}
