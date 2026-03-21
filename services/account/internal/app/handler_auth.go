package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"kagent/pkg/toolproto"

	"golang.org/x/crypto/bcrypt"
)

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
	resp.Effects = &toolproto.Effects{SetCookies: []toolproto.SetCookieEffect{{
		Name:      tokenCookieName,
		Value:     token,
		MaxAgeSec: tokenMaxAgeSec,
	}}}
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
	resp.Effects = &toolproto.Effects{SetCookies: []toolproto.SetCookieEffect{{
		Name:      tokenCookieName,
		Value:     token,
		MaxAgeSec: tokenMaxAgeSec,
	}}}
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
	resp.Result = map[string]any{"user_id": caller.UserID, "ok": true}
	resp.Effects = &toolproto.Effects{SetCookies: []toolproto.SetCookieEffect{{
		Name:      tokenCookieName,
		Value:     "",
		MaxAgeSec: -1,
	}}}
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
	resp.Result = map[string]any{"user_id": user.UserID, "username": user.Username}
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
	resp.Result = map[string]any{"user_id": user.UserID, "sid": sid, "ok": true}
	resp.Effects = &toolproto.Effects{SetCookies: []toolproto.SetCookieEffect{{
		Name:      tokenCookieName,
		Value:     "",
		MaxAgeSec: -1,
	}}}
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
