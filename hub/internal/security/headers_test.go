package security

import (
	"net/http"
	"testing"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func TestSanitizeForwardHeadersStripsProtected(t *testing.T) {
	src := http.Header{}
	src.Set("X-Hub-Request-Id", "bad-req")
	src.Set("X-Hub-Trace-Id", "bad-trace")
	src.Set("X-Caller-User-Id", "bad-user")
	src.Set(hubsvc.HeaderOriginCallerToken, "bad-origin")
	src.Set(hubsvc.HeaderHubAuth, "bad-auth")
	src.Set(hubsvc.HeaderSurfaceToken, "bad-surface")
	src.Set("X-Custom-Header", "ok")

	out := SanitizeForwardHeaders(src)
	if got := out.Get("X-Hub-Request-Id"); got != "" {
		t.Fatalf("expected X-Hub-Request-Id removed, got %q", got)
	}
	if got := out.Get("X-Hub-Trace-Id"); got != "" {
		t.Fatalf("expected X-Hub-Trace-Id removed, got %q", got)
	}
	if got := out.Get("X-Caller-User-Id"); got != "" {
		t.Fatalf("expected X-Caller-User-Id removed, got %q", got)
	}
	if got := out.Get(hubsvc.HeaderOriginCallerToken); got != "" {
		t.Fatalf("expected %s removed, got %q", hubsvc.HeaderOriginCallerToken, got)
	}
	if got := out.Get(hubsvc.HeaderHubAuth); got != "" {
		t.Fatalf("expected %s removed, got %q", hubsvc.HeaderHubAuth, got)
	}
	if got := out.Get(hubsvc.HeaderSurfaceToken); got != "" {
		t.Fatalf("expected %s removed, got %q", hubsvc.HeaderSurfaceToken, got)
	}
	if got := out.Get("X-Custom-Header"); got != "ok" {
		t.Fatalf("expected custom header preserved, got %q", got)
	}
}

func TestInjectCallerHeadersOverwritesTrustedFields(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Hub-Request-Id", "old")
	headers.Set("X-Caller-User-Id", "old-user")

	InjectCallerHeaders(headers, &toolproto.Context{
		RequestID: "req-1",
		TraceID:   "tr-1",
		Caller: toolproto.Caller{
			Type:      "user",
			UserID:    "u-1",
			ServiceID: "svc-1",
			SurfaceID: "surface-1",
		},
		OriginCaller: toolproto.Caller{
			Type:      "user",
			UserID:    "u-origin",
			ServiceID: "svc-origin",
			SurfaceID: "surface-origin",
		},
		OriginToken: "origin-token",
	}, "untrusted")

	if got := headers.Get("X-Hub-Request-Id"); got != "req-1" {
		t.Fatalf("expected request id injected, got %q", got)
	}
	if got := headers.Get("X-Hub-Trace-Id"); got != "tr-1" {
		t.Fatalf("expected trace id injected, got %q", got)
	}
	if got := headers.Get("X-Caller-Type"); got != "user" {
		t.Fatalf("expected caller type injected, got %q", got)
	}
	if got := headers.Get("X-Caller-User-Id"); got != "u-1" {
		t.Fatalf("expected caller user id injected, got %q", got)
	}
	if got := headers.Get("X-Caller-Service-Id"); got != "svc-1" {
		t.Fatalf("expected caller service id injected, got %q", got)
	}
	if got := headers.Get("X-Caller-Surface-Id"); got != "surface-1" {
		t.Fatalf("expected caller surface id injected, got %q", got)
	}
	if got := headers.Get("X-Caller-Reliability"); got != "untrusted" {
		t.Fatalf("expected caller reliability injected, got %q", got)
	}
	if got := headers.Get(hubsvc.HeaderOriginCallerUserID); got != "u-origin" {
		t.Fatalf("expected origin caller user id injected, got %q", got)
	}
	if got := headers.Get(hubsvc.HeaderOriginCallerToken); got != "origin-token" {
		t.Fatalf("expected origin caller token injected, got %q", got)
	}
}

func TestInjectHubAuthHeaders(t *testing.T) {
	headers := http.Header{}
	InjectHubAuthHeaders(headers, "chat_server", "ins-1", "token-1")
	if got := headers.Get(hubsvc.HeaderHubServiceID); got != "chat_server" {
		t.Fatalf("expected service id injected, got %q", got)
	}
	if got := headers.Get(hubsvc.HeaderHubInstanceID); got != "ins-1" {
		t.Fatalf("expected instance id injected, got %q", got)
	}
	if got := headers.Get(hubsvc.HeaderHubAuth); got != "token-1" {
		t.Fatalf("expected hub auth injected, got %q", got)
	}
}
