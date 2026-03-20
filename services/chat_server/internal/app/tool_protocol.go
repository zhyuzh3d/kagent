package app

import (
	"kagent/pkg/hubsvc"
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
type SupervisorRegisterResult = toolproto.SupervisorRegisterResult
type LifecycleState = toolproto.LifecycleState

func NormalizeCallRequest(req CallRequest) (CallRequest, error) {
	return toolproto.NormalizeRequest(req)
}

func HTTPStatusFromCode(code string) int {
	return toolproto.HTTPStatusFromCode(code)
}

func DecodeSupervisorRegisterResult(responseBody []byte) (SupervisorRegisterResult, error) {
	return hubsvc.DecodeSupervisorRegisterResult(responseBody)
}
