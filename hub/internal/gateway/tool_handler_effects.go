package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	app "kagent/hub/internal/app"
	"kagent/pkg/toolproto"
)

var effectKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

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
