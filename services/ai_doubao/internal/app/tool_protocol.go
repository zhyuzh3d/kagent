package app

import (
	"encoding/json"

	"kagent/pkg/toolproto"
)

const (
	ErrorCodeBadRequest         = toolproto.ErrorCodeBadRequest
	ErrorCodeUnauthorized       = toolproto.ErrorCodeUnauthorized
	ErrorCodeForbidden          = toolproto.ErrorCodeForbidden
	ErrorCodeToolNotFound       = toolproto.ErrorCodeToolNotFound
	ErrorCodeRouteNotFound      = toolproto.ErrorCodeRouteNotFound
	ErrorCodeServiceUnavailable = toolproto.ErrorCodeServiceUnavailable
	ErrorCodeToolTimeout        = toolproto.ErrorCodeToolTimeout
	ErrorCodeToolExecError      = toolproto.ErrorCodeToolExecError
	ErrorCodeRateLimited        = toolproto.ErrorCodeRateLimited
	ErrorCodeConflict           = toolproto.ErrorCodeConflict
	ErrorCodeInternalError      = toolproto.ErrorCodeInternalError
)

type Caller = toolproto.Caller
type CallContext = toolproto.Context
type CallRequest = toolproto.CallRequest
type ToolError = toolproto.Error
type Meta = toolproto.Meta
type CallResponse = toolproto.CallResponse
type Effects = toolproto.Effects
type Endpoint = toolproto.Endpoint
type ServiceTool = toolproto.ServiceTool
type SupervisorRegisterRequest = toolproto.SupervisorRegisterRequest
type LifecycleState = toolproto.LifecycleState

func NormalizeCallRequest(req CallRequest) (CallRequest, error) {
	return toolproto.NormalizeRequest(req)
}

func HTTPStatusFromCode(code string) int {
	return toolproto.HTTPStatusFromCode(code)
}

func DecodeToolResultMap(result any) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	if m, ok := result.(map[string]any); ok {
		return m
	}
	raw, _ := json.Marshal(result)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
