package security

import (
	"net/http"
	"strings"

	"kagent/pkg/toolproto"
)

var protectedHeaders = map[string]struct{}{
	"X-Hub-Request-Id":     {},
	"X-Hub-Trace-Id":       {},
	"X-Caller-Type":        {},
	"X-Caller-User-Id":     {},
	"X-Caller-Service-Id":  {},
	"X-Caller-Surface-Id":  {},
	"X-Hub-Service-Token":  {},
	"X-Hub-Platform-Token": {},
}

func SanitizeForwardHeaders(src http.Header) http.Header {
	dst := http.Header{}
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if _, ok := protectedHeaders[canonical]; ok {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	return dst
}

func InjectCallerHeaders(headers http.Header, context *toolproto.Context, serviceToken string, platformToken string) {
	if headers == nil {
		return
	}
	requestID := ""
	traceID := ""
	callerType := ""
	callerUserID := ""
	callerServiceID := ""
	callerSurfaceID := ""
	if context != nil {
		requestID = strings.TrimSpace(context.RequestID)
		traceID = strings.TrimSpace(context.TraceID)
		callerType = strings.TrimSpace(context.Caller.Type)
		callerUserID = strings.TrimSpace(context.Caller.UserID)
		callerServiceID = strings.TrimSpace(context.Caller.ServiceID)
		callerSurfaceID = strings.TrimSpace(context.Caller.SurfaceID)
	}
	headers.Set("X-Hub-Request-Id", requestID)
	headers.Set("X-Hub-Trace-Id", traceID)
	headers.Set("X-Caller-Type", callerType)
	headers.Set("X-Caller-User-Id", callerUserID)
	headers.Set("X-Caller-Service-Id", callerServiceID)
	headers.Set("X-Caller-Surface-Id", callerSurfaceID)
	headers.Set("X-Hub-Service-Token", strings.TrimSpace(serviceToken))
	headers.Set("X-Hub-Platform-Token", strings.TrimSpace(platformToken))
}
