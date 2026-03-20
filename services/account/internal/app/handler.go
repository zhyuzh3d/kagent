package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"kagent/pkg/toolproto"

	"golang.org/x/crypto/bcrypt"
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
	case "service.lifecycle.init":
		resp = h.handleInit(ctx, req)
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

func (h *Handler) handleRegister(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	username := strings.TrimSpace(trimmedString(req.Args["username"]))
	password := trimmedString(req.Args["password"])
	if username == "" {
		return badRequest(resp, "username is required")
	}
	if len(password) < passwordMinLen {
		return badRequest(resp, "password too short")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return internalError(resp, "hash password failed")
	}
	userID, err := h.store.CreateUser(ctx, username, hash)
	if err != nil {
		if errors.Is(err, ErrUsernameExists) {
			return conflict(resp, "username already exists")
		}
		return internalError(resp, "register failed")
	}
	sid := newSessionID()
	if err := h.store.SetActiveSession(ctx, userID, sid); err != nil {
		return internalError(resp, "set active session failed")
	}
	token, expMS, err := issueToken(h.signing, userID, username, sid)
	if err != nil {
		return internalError(resp, "issue token failed")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"user_id":  userID,
		"username": username,
		"sid":      sid,
		"exp_ms":   expMS,
	}
	resp.Effects = &toolproto.Effects{
		SetCookies: []toolproto.SetCookieEffect{{
			Name:      tokenCookieName,
			Value:     token,
			MaxAgeSec: tokenMaxAgeSec,
		}},
	}
	return resp
}

func (h *Handler) handleLogin(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	username := strings.TrimSpace(trimmedString(req.Args["username"]))
	password := trimmedString(req.Args["password"])
	if username == "" || password == "" {
		return badRequest(resp, "username and password are required")
	}
	user, ok, err := h.store.GetUserByUsername(ctx, username)
	if err != nil {
		return internalError(resp, "login query failed")
	}
	if !ok || !verifyPassword(password, user.PasswordHash) {
		return unauthorized(resp, "invalid username or password")
	}
	sid := newSessionID()
	if err := h.store.SetActiveSession(ctx, user.UserID, sid); err != nil {
		return internalError(resp, "set active session failed")
	}
	token, expMS, err := issueToken(h.signing, user.UserID, user.Username, sid)
	if err != nil {
		return internalError(resp, "issue token failed")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"user_id":  user.UserID,
		"username": user.Username,
		"sid":      sid,
		"exp_ms":   expMS,
	}
	resp.Effects = &toolproto.Effects{
		SetCookies: []toolproto.SetCookieEffect{{
			Name:      tokenCookieName,
			Value:     token,
			MaxAgeSec: tokenMaxAgeSec,
		}},
	}
	return resp
}

func (h *Handler) handleLogout(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	caller := req.Context.Caller
	if !strings.EqualFold(caller.Type, "user") || strings.TrimSpace(caller.UserID) == "" {
		return unauthorized(resp, "login required")
	}
	if err := h.store.ClearActiveSession(ctx, caller.UserID); err != nil {
		return internalError(resp, "logout failed")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"user_id": caller.UserID,
		"ok":      true,
	}
	resp.Effects = &toolproto.Effects{
		SetCookies: []toolproto.SetCookieEffect{{
			Name:      tokenCookieName,
			Value:     "",
			MaxAgeSec: -1,
		}},
	}
	return resp
}

func (h *Handler) handleMe(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	caller := req.Context.Caller
	if !strings.EqualFold(caller.Type, "user") || strings.TrimSpace(caller.UserID) == "" {
		return unauthorized(resp, "login required")
	}
	user, ok, err := h.store.GetUserByID(ctx, caller.UserID)
	if err != nil {
		return internalError(resp, "query user failed")
	}
	if !ok {
		return unauthorized(resp, "user not found")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"user_id":  user.UserID,
		"username": user.Username,
	}
	return resp
}

func (h *Handler) handlePasswordChange(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	caller := req.Context.Caller
	if !strings.EqualFold(caller.Type, "user") || strings.TrimSpace(caller.UserID) == "" {
		return unauthorized(resp, "login required")
	}
	oldPassword := trimmedString(req.Args["old_password"])
	newPassword := trimmedString(req.Args["new_password"])
	if len(newPassword) < passwordMinLen {
		return badRequest(resp, "new password too short")
	}
	user, ok, err := h.store.GetUserByID(ctx, caller.UserID)
	if err != nil {
		return internalError(resp, "query user failed")
	}
	if !ok {
		return unauthorized(resp, "user not found")
	}
	if !verifyPassword(oldPassword, user.PasswordHash) {
		return unauthorized(resp, "old password mismatch")
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return internalError(resp, "hash password failed")
	}
	if err := h.store.UpdatePasswordHash(ctx, user.UserID, newHash); err != nil {
		return internalError(resp, "update password failed")
	}
	sid := newSessionID()
	if err := h.store.SetActiveSession(ctx, user.UserID, sid); err != nil {
		return internalError(resp, "set active session failed")
	}
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{
		"user_id": user.UserID,
		"sid":     sid,
		"ok":      true,
	}
	resp.Effects = &toolproto.Effects{
		SetCookies: []toolproto.SetCookieEffect{{
			Name:      tokenCookieName,
			Value:     "",
			MaxAgeSec: -1,
		}},
	}
	return resp
}

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

func (h *Handler) handleInit(ctx context.Context, req toolproto.CallRequest) toolproto.CallResponse {
	resp := baseResponse(req, h)
	if !isSelfServiceCaller(req.Context.Caller) {
		return forbidden(resp, "forbidden")
	}
	h.mu.Lock()
	if h.ready {
		h.mu.Unlock()
		resp.Ok = true
		resp.Error = nil
		resp.Result = map[string]any{"ok": true, "status": "ready"}
		return resp
	}
	if h.initing {
		h.mu.Unlock()
		resp.Ok = true
		resp.Error = nil
		resp.Result = map[string]any{"ok": true, "status": "initializing"}
		return resp
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
		return serviceUnavailable(resp, err.Error())
	}
	h.signing = signingKey
	h.ready = true
	h.lastInitErr = ""
	resp.Ok = true
	resp.Error = nil
	resp.Result = map[string]any{"ok": true, "status": "ready"}
	return resp
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
	resp.Result = map[string]any{
		"ok":      true,
		"message": "shutting_down",
	}
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

func baseResponse(req toolproto.CallRequest, h *Handler) toolproto.CallResponse {
	return toolproto.CallResponse{
		Ok:    false,
		Meta:  responseMeta(req.Context, h.serviceID, h.instance),
		Error: &toolproto.Error{Code: defaultResponseCode, Message: "tool execution failed"},
	}
}

func badRequest(resp toolproto.CallResponse, msg string) toolproto.CallResponse {
	resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: msg}
	return resp
}

func unauthorized(resp toolproto.CallResponse, msg string) toolproto.CallResponse {
	resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: msg}
	return resp
}

func serviceUnavailable(resp toolproto.CallResponse, msg string) toolproto.CallResponse {
	resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeServiceUnavailable, Message: msg}
	return resp
}

func forbidden(resp toolproto.CallResponse, msg string) toolproto.CallResponse {
	resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeForbidden, Message: msg}
	return resp
}

func conflict(resp toolproto.CallResponse, msg string) toolproto.CallResponse {
	resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeConflict, Message: msg}
	return resp
}

func internalError(resp toolproto.CallResponse, msg string) toolproto.CallResponse {
	resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: msg}
	return resp
}

func hashPassword(password string) (string, error) {
	if len(password) < passwordMinLen {
		return "", fmt.Errorf("password too short")
	}
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return "bcrypt$" + string(hashBytes), nil
}

func verifyPassword(password string, stored string) bool {
	clean := strings.TrimSpace(stored)
	if clean == "" {
		return false
	}
	if strings.HasPrefix(clean, "bcrypt$") {
		return bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(clean, "bcrypt$")), []byte(password)) == nil
	}
	return false
}

func issueToken(key SigningKey, userID string, username string, sid string) (string, int64, error) {
	if len(key.PrivateKey) == 0 {
		return "", 0, fmt.Errorf("signing key is not initialized")
	}
	now := time.Now().UnixMilli()
	exp := time.Now().Add(time.Duration(tokenMaxAgeSec) * time.Second).UnixMilli()
	claims := Claims{
		UserID:   strings.TrimSpace(userID),
		Username: strings.TrimSpace(username),
		SID:      strings.TrimSpace(sid),
		IatMS:    now,
		ExpMS:    exp,
		KID:      strings.TrimSpace(key.KID),
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", 0, err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	signature := ed25519.Sign(key.PrivateKey, []byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), exp, nil
}

func newSessionID() string {
	return "sid-" + newID()
}

func trimmedString(v any) string {
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv)
	case fmt.Stringer:
		return strings.TrimSpace(tv.String())
	default:
		return ""
	}
}
