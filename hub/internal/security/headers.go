package security

import (
	"net/http"
	"strings"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

var protectedHeaders = map[string]struct{}{
	"X-Hub-Request-Id":                 {},
	"X-Hub-Trace-Id":                   {},
	"X-Caller-Type":                    {},
	"X-Caller-User-Id":                 {},
	"X-Caller-Service-Id":              {},
	"X-Caller-Surface-Id":              {},
	"X-Caller-Reliability":             {},
	hubsvc.HeaderOriginCallerType:      {},
	hubsvc.HeaderOriginCallerUserID:    {},
	hubsvc.HeaderOriginCallerServiceID: {},
	hubsvc.HeaderOriginCallerSurfaceID: {},
	hubsvc.HeaderOriginCallerToken:     {},
	hubsvc.HeaderServiceID:             {},
	hubsvc.HeaderServiceInstanceID:     {},
	hubsvc.HeaderServiceAuth:           {},
	hubsvc.HeaderHubServiceID:          {},
	hubsvc.HeaderHubInstanceID:         {},
	hubsvc.HeaderHubAuth:               {},
	hubsvc.HeaderSurfaceToken:          {},
	"X-Hub-Service-Token":              {},
	"X-Hub-Platform-Token":             {},
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

func InjectCallerHeaders(headers http.Header, context *toolproto.Context, callerReliability string) {
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
	headers.Set("X-Caller-Reliability", strings.TrimSpace(callerReliability))
	originType := ""
	originUserID := ""
	originServiceID := ""
	originSurfaceID := ""
	originToken := ""
	if context != nil {
		originType = strings.TrimSpace(context.OriginCaller.Type)
		originUserID = strings.TrimSpace(context.OriginCaller.UserID)
		originServiceID = strings.TrimSpace(context.OriginCaller.ServiceID)
		originSurfaceID = strings.TrimSpace(context.OriginCaller.SurfaceID)
		originToken = strings.TrimSpace(context.OriginToken)
	}
	headers.Set(hubsvc.HeaderOriginCallerType, originType)
	headers.Set(hubsvc.HeaderOriginCallerUserID, originUserID)
	headers.Set(hubsvc.HeaderOriginCallerServiceID, originServiceID)
	headers.Set(hubsvc.HeaderOriginCallerSurfaceID, originSurfaceID)
	headers.Set(hubsvc.HeaderOriginCallerToken, originToken)
}

func InjectHubAuthHeaders(headers http.Header, serviceID string, instanceID string, hubAuth string) {
	if headers == nil {
		return
	}
	headers.Set(hubsvc.HeaderHubServiceID, strings.TrimSpace(serviceID))
	headers.Set(hubsvc.HeaderHubInstanceID, strings.TrimSpace(instanceID))
	headers.Set(hubsvc.HeaderHubAuth, strings.TrimSpace(hubAuth))
}
