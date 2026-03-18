package app

import (
	"encoding/json"
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
)

type Caller struct {
	Type      string `json:"type,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	SurfaceID string `json:"surface_id,omitempty"`
}

type CallContext struct {
	RequestID      string         `json:"request_id,omitempty"`
	TraceID        string         `json:"trace_id,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	TimeoutMS      int            `json:"timeout_ms,omitempty"`
	Caller         Caller         `json:"caller,omitempty"`
	Capabilities   []string       `json:"capabilities,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}

type CallRequest struct {
	ToolID  string         `json:"tool_id"`
	Args    map[string]any `json:"args"`
	Context *CallContext   `json:"context,omitempty"`
}

type ToolError struct {
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
	Ok     bool       `json:"ok"`
	Result any        `json:"result,omitempty"`
	Error  *ToolError `json:"error,omitempty"`
	Meta   Meta       `json:"meta,omitempty"`
}

type Endpoint struct {
	UDSPath string `json:"uds_path,omitempty"`
	TCPURL  string `json:"tcp_url,omitempty"`
}

type ServiceTool struct {
	ToolID               string   `json:"tool_id"`
	Version              string   `json:"version,omitempty"`
	Streaming            bool     `json:"streaming,omitempty"`
	WSPath               string   `json:"ws_path,omitempty"`
	TimeoutMS            int      `json:"timeout_ms,omitempty"`
	CapabilitiesRequired []string `json:"capabilities_required,omitempty"`
}

type SupervisorRegisterRequest struct {
	ServiceID  string        `json:"service_id"`
	InstanceID string        `json:"instance_id"`
	Version    string        `json:"version,omitempty"`
	Transport  string        `json:"transport,omitempty"`
	Endpoint   Endpoint      `json:"endpoint"`
	Tools      []ServiceTool `json:"tools,omitempty"`
	Weight     int           `json:"weight,omitempty"`
	Tags       []string      `json:"tags,omitempty"`
	HealthPath string        `json:"health_path,omitempty"`
	Healthy    *bool         `json:"healthy,omitempty"`
}

type SupervisorRegisterResult struct {
	ServiceSessionToken            string `json:"service_session_token"`
	ExpiresInSec                   int    `json:"expires_in_sec"`
	HeartbeatIntervalSec           int    `json:"heartbeat_interval_sec"`
	InverseHeartbeatIntervalSec    int    `json:"inverse_heartbeat_interval_sec"`
	InverseHeartbeatFailuresToExit int    `json:"inverse_heartbeat_failures_to_exit"`
	DrainGracePeriodSec            int    `json:"drain_grace_period_sec"`
}

func NormalizeCallRequest(req CallRequest) (CallRequest, error) {
	req.ToolID = strings.TrimSpace(req.ToolID)
	if req.ToolID == "" {
		return CallRequest{}, fmt.Errorf("tool_id is required")
	}
	if !validToolID(req.ToolID) {
		return CallRequest{}, fmt.Errorf("tool_id must use category.type.tool format")
	}
	if req.Args == nil {
		req.Args = map[string]any{}
	}
	if req.Context == nil {
		req.Context = &CallContext{}
	}
	req.Context.Caller.Type = strings.TrimSpace(strings.ToLower(req.Context.Caller.Type))
	req.Context.Caller.UserID = strings.TrimSpace(req.Context.Caller.UserID)
	req.Context.Caller.ServiceID = strings.TrimSpace(req.Context.Caller.ServiceID)
	req.Context.Caller.SurfaceID = strings.TrimSpace(req.Context.Caller.SurfaceID)
	req.Context.RequestID = strings.TrimSpace(req.Context.RequestID)
	req.Context.TraceID = strings.TrimSpace(req.Context.TraceID)
	req.Context.IdempotencyKey = strings.TrimSpace(req.Context.IdempotencyKey)
	return req, nil
}

func validToolID(toolID string) bool {
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

func DecodeSupervisorRegisterResult(responseBody []byte) (SupervisorRegisterResult, error) {
	var callResp CallResponse
	if err := json.Unmarshal(responseBody, &callResp); err != nil {
		return SupervisorRegisterResult{}, fmt.Errorf("decode register response: %w", err)
	}
	if !callResp.Ok {
		msg := "register rejected"
		if callResp.Error != nil && strings.TrimSpace(callResp.Error.Message) != "" {
			msg = strings.TrimSpace(callResp.Error.Message)
		}
		return SupervisorRegisterResult{}, fmt.Errorf("%s", msg)
	}
	rawResult, err := json.Marshal(callResp.Result)
	if err != nil {
		return SupervisorRegisterResult{}, fmt.Errorf("marshal register result: %w", err)
	}
	var out SupervisorRegisterResult
	if err := json.Unmarshal(rawResult, &out); err != nil {
		return SupervisorRegisterResult{}, fmt.Errorf("decode register result: %w", err)
	}
	return out, nil
}
