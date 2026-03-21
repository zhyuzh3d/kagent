package app

import "kagent/pkg/toolproto"

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
