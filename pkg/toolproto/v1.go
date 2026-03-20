package toolproto

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	ErrorCodeBadRequest         = "BAD_REQUEST"
	ErrorCodeUnauthorized       = "UNAUTHORIZED"
	ErrorCodeForbidden          = "FORBIDDEN"
	ErrorCodeToolNotFound       = "TOOL_NOT_FOUND"
	ErrorCodeRouteNotFound      = "ROUTE_NOT_FOUND"
	ErrorCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrorCodeToolTimeout        = "TOOL_TIMEOUT"
	ErrorCodeToolExecError      = "TOOL_EXEC_ERROR"
	ErrorCodeRateLimited        = "RATE_LIMITED"
	ErrorCodeConflict           = "CONFLICT"
	ErrorCodeInternalError      = "INTERNAL_ERROR"

	CallerTypeAnonymous = "anonymous"
	CallerTypeUser      = "user"
	CallerTypeAdmin     = "admin"
	CallerTypeSurface   = "surface"
	CallerTypePage      = "page"
	CallerTypeService   = "service"
	CallerTypeHub       = "hub"
	CallerTypeAll       = "all"
)

type Caller struct {
	Type      string `json:"type,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	SurfaceID string `json:"surface_id,omitempty"`
}

type Context struct {
	RequestID      string         `json:"request_id,omitempty"`
	TraceID        string         `json:"trace_id,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	TimeoutMS      int            `json:"timeout_ms,omitempty"`
	Caller         Caller         `json:"caller,omitempty"`
	OriginCaller   Caller         `json:"origin_caller,omitempty"`
	OriginToken    string         `json:"origin_caller_token,omitempty"`
	Capabilities   []string       `json:"capabilities,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}

type CallRequest struct {
	ToolID  string         `json:"tool_id"`
	Args    map[string]any `json:"args"`
	Context *Context       `json:"context,omitempty"`
}

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Retryable bool           `json:"retryable,omitempty"`
}

type Meta struct {
	RequestID  string `json:"request_id,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
	ServiceID  string `json:"service_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type CallResponse struct {
	Ok      bool     `json:"ok"`
	Result  any      `json:"result,omitempty"`
	Error   *Error   `json:"error,omitempty"`
	Meta    Meta     `json:"meta,omitempty"`
	Effects *Effects `json:"effects,omitempty"`
}

type Effects struct {
	SetCookies []SetCookieEffect `json:"set_cookies,omitempty"`
	SetHeaders []SetHeaderEffect `json:"set_headers,omitempty"`
}

type SetCookieEffect struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	MaxAgeSec int    `json:"max_age_sec,omitempty"`
}

type SetHeaderEffect struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type WSCallFrame struct {
	Type    string         `json:"type"`
	ToolID  string         `json:"tool_id"`
	Args    map[string]any `json:"args"`
	Context *Context       `json:"context,omitempty"`
}

type WSEvent struct {
	Type      string         `json:"type"`
	Event     string         `json:"event"`
	RequestID string         `json:"request_id,omitempty"`
	TraceID   string         `json:"trace_id,omitempty"`
	Seq       int64          `json:"seq,omitempty"`
	Done      bool           `json:"done,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func NormalizeRequest(req CallRequest) (CallRequest, error) {
	req.ToolID = strings.TrimSpace(req.ToolID)
	if req.ToolID == "" {
		return CallRequest{}, fmt.Errorf("tool_id is required")
	}
	if !ValidToolID(req.ToolID) {
		return CallRequest{}, fmt.Errorf("tool_id must use category.type.tool format")
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}
	if req.Context == nil {
		req.Context = &Context{}
	}
	req.Context.Caller.Type = strings.TrimSpace(strings.ToLower(req.Context.Caller.Type))
	req.Context.Caller.UserID = strings.TrimSpace(req.Context.Caller.UserID)
	req.Context.Caller.ServiceID = strings.TrimSpace(req.Context.Caller.ServiceID)
	req.Context.Caller.SurfaceID = strings.TrimSpace(req.Context.Caller.SurfaceID)
	req.Context.OriginCaller.Type = strings.TrimSpace(strings.ToLower(req.Context.OriginCaller.Type))
	req.Context.OriginCaller.UserID = strings.TrimSpace(req.Context.OriginCaller.UserID)
	req.Context.OriginCaller.ServiceID = strings.TrimSpace(req.Context.OriginCaller.ServiceID)
	req.Context.OriginCaller.SurfaceID = strings.TrimSpace(req.Context.OriginCaller.SurfaceID)
	req.Context.OriginToken = strings.TrimSpace(req.Context.OriginToken)
	req.Context.RequestID = strings.TrimSpace(req.Context.RequestID)
	req.Context.TraceID = strings.TrimSpace(req.Context.TraceID)
	req.Context.IdempotencyKey = strings.TrimSpace(req.Context.IdempotencyKey)
	return req, nil
}

func ValidToolID(toolID string) bool {
	parts := strings.Split(strings.TrimSpace(toolID), ".")
	if len(parts) < 3 {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}

func SplitToolID(toolID string) (string, string, string) {
	parts := strings.Split(strings.TrimSpace(toolID), ".")
	if len(parts) < 3 {
		return "", "", ""
	}
	return parts[0], parts[1], strings.Join(parts[2:], ".")
}

func UniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.TrimSpace(value)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func NormalizeAllowedCallerTypes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	hasAll := false
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if clean == "" {
			continue
		}
		if clean == CallerTypeAll {
			hasAll = true
		}
		out = append(out, clean)
	}
	if hasAll {
		return []string{CallerTypeAll}
	}
	return UniqueNonEmptyStrings(out)
}

func HTTPStatusFromCode(code string) int {
	switch strings.TrimSpace(code) {
	case ErrorCodeBadRequest:
		return http.StatusBadRequest
	case ErrorCodeUnauthorized:
		return http.StatusUnauthorized
	case ErrorCodeForbidden:
		return http.StatusForbidden
	case ErrorCodeToolNotFound, ErrorCodeRouteNotFound:
		return http.StatusNotFound
	case ErrorCodeConflict:
		return http.StatusConflict
	case ErrorCodeRateLimited:
		return http.StatusTooManyRequests
	case ErrorCodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case ErrorCodeToolTimeout:
		return http.StatusGatewayTimeout
	case ErrorCodeToolExecError, ErrorCodeInternalError:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
