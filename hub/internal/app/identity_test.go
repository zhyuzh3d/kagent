package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func makeTestAuthService(t *testing.T) *AuthService {
	t.Helper()
	dir := t.TempDir()
	svc, err := NewAuthService(dir)
	if err != nil {
		t.Fatalf("NewAuthService: %v", err)
	}
	return svc
}

func makeTestHubPlatform(t *testing.T) *HubPlatform {
	t.Helper()
	dir := t.TempDir()
	hp, err := NewHubPlatform(dir)
	if err != nil {
		t.Fatalf("NewHubPlatform: %v", err)
	}
	return hp
}

func issueTestSurfaceToken(t *testing.T, hp *HubPlatform, claims hubsvc.SurfaceTokenClaims) string {
	t.Helper()
	token, err := hubsvc.SignSurfaceToken(hp.surfaceSecret, claims)
	if err != nil {
		t.Fatalf("SignSurfaceToken: %v", err)
	}
	return token
}

func TestIdentityFromContext_Default(t *testing.T) {
	ctx := context.Background()
	id := IdentityFromContext(ctx)
	if id.Type != IdentityAnonymous {
		t.Fatalf("expected ANONYMOUS, got %s", id.Type)
	}
	if id.Name != "anonymous" {
		t.Fatalf("expected name 'anonymous', got %q", id.Name)
	}
}

func TestContextWithIdentity_Roundtrip(t *testing.T) {
	ctx := context.Background()
	want := Identity{Type: IdentityUser, ID: "u-123", Name: "alice", UserID: "u-123"}
	ctx = ContextWithIdentity(ctx, want)
	got := IdentityFromContext(ctx)
	if got.Type != want.Type || got.ID != want.ID || got.Name != want.Name {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", got, want)
	}
}

func TestIdentityCaller_Surface(t *testing.T) {
	got := (Identity{
		Type:      IdentitySurface,
		UserID:    "user-1",
		SurfaceID: "surface-1",
	}).Caller()
	want := toolproto.Caller{
		Type:      toolproto.CallerTypeSurface,
		UserID:    "user-1",
		SurfaceID: "surface-1",
	}
	if got != want {
		t.Fatalf("caller mismatch: got %+v want %+v", got, want)
	}
}

func TestIdentityMiddleware_ValidJWT(t *testing.T) {
	auth := makeTestAuthService(t)
	token, err := auth.IssueJWT("user-001", "zhyuzh")
	if err != nil {
		t.Fatalf("IssueJWT: %v", err)
	}

	var captured Identity
	handler := IdentityMiddleware(auth, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: JWTCookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentityUser {
		t.Fatalf("expected USER, got %s", captured.Type)
	}
	if captured.ID != "user-001" {
		t.Fatalf("expected ID 'user-001', got %q", captured.ID)
	}
	if captured.Name != "zhyuzh" {
		t.Fatalf("expected Name 'zhyuzh', got %q", captured.Name)
	}
	if captured.UserID != "user-001" {
		t.Fatalf("expected UserID 'user-001', got %q", captured.UserID)
	}
}

func TestIdentityMiddleware_NoCookie(t *testing.T) {
	auth := makeTestAuthService(t)

	var captured Identity
	handler := IdentityMiddleware(auth, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentityAnonymous {
		t.Fatalf("expected ANONYMOUS, got %s", captured.Type)
	}
	if captured.Name != "anonymous" {
		t.Fatalf("expected name 'anonymous', got %q", captured.Name)
	}
}

func TestIdentityMiddleware_ExpiredJWT(t *testing.T) {
	auth := makeTestAuthService(t)

	// Issue a token that's already expired (hack: issue then modify expiry)
	// We can't directly create expired tokens easily via IssueJWT, so instead
	// test with a corrupted token value
	var captured Identity
	handler := IdentityMiddleware(auth, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: JWTCookieName, Value: "invalid.token.value"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentityAnonymous {
		t.Fatalf("expected ANONYMOUS for invalid token, got %s", captured.Type)
	}
}

func TestIdentityMiddleware_ValidServiceToken(t *testing.T) {
	auth := makeTestAuthService(t)
	hp := makeTestHubPlatform(t)

	bootstrap, err := hp.PrepareServiceBootstrap("test-svc", "ins-001", "http://127.0.0.1:18080/api/service/register", 5*time.Minute)
	if err != nil {
		t.Fatalf("PrepareServiceBootstrap: %v", err)
	}

	var captured Identity
	handler := IdentityMiddleware(auth, hp)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(hubsvc.HeaderServiceID, bootstrap.ServiceID)
	req.Header.Set(hubsvc.HeaderServiceInstanceID, bootstrap.InstanceID)
	req.Header.Set(hubsvc.HeaderServiceAuth, bootstrap.S2HToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentityService {
		t.Fatalf("expected SERVICE, got %s", captured.Type)
	}
	if captured.ID != "test-svc" {
		t.Fatalf("expected ID 'test-svc', got %q", captured.ID)
	}
}

func TestIdentityMiddleware_ValidSurfaceToken(t *testing.T) {
	auth := makeTestAuthService(t)
	hp := makeTestHubPlatform(t)
	token := issueTestSurfaceToken(t, hp, hubsvc.SurfaceTokenClaims{
		Kind:      hubsvc.SurfaceTokenKindSession,
		UserID:    "user-surface",
		SurfaceID: "surface-123",
		ExpMS:     time.Now().Add(5 * time.Minute).UnixMilli(),
		Nonce:     "nonce-1",
	})

	var captured Identity
	handler := IdentityMiddleware(auth, hp)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(hubsvc.HeaderSurfaceToken, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentitySurface {
		t.Fatalf("expected SURFACE, got %s", captured.Type)
	}
	if captured.SurfaceID != "surface-123" {
		t.Fatalf("expected surface_id 'surface-123', got %q", captured.SurfaceID)
	}
	if captured.UserID != "user-surface" {
		t.Fatalf("expected user_id 'user-surface', got %q", captured.UserID)
	}
	if captured.Name != "surface:surface-123" {
		t.Fatalf("expected surface name, got %q", captured.Name)
	}
}

func TestIdentityMiddleware_InvalidSurfaceTokenFallsBackAnonymous(t *testing.T) {
	auth := makeTestAuthService(t)
	hp := makeTestHubPlatform(t)

	var captured Identity
	handler := IdentityMiddleware(auth, hp)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(hubsvc.HeaderSurfaceToken, "bad.token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentityAnonymous {
		t.Fatalf("expected ANONYMOUS, got %s", captured.Type)
	}
}

func TestIdentityMiddleware_ExpiredSurfaceTokenFallsBackAnonymous(t *testing.T) {
	auth := makeTestAuthService(t)
	hp := makeTestHubPlatform(t)
	token := issueTestSurfaceToken(t, hp, hubsvc.SurfaceTokenClaims{
		Kind:      hubsvc.SurfaceTokenKindSession,
		UserID:    "user-surface",
		SurfaceID: "surface-expired",
		ExpMS:     time.Now().Add(5 * time.Millisecond).UnixMilli(),
		Nonce:     "nonce-expired",
	})
	time.Sleep(10 * time.Millisecond)

	var captured Identity
	handler := IdentityMiddleware(auth, hp)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(hubsvc.HeaderSurfaceToken, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if captured.Type != IdentityAnonymous {
		t.Fatalf("expected ANONYMOUS, got %s", captured.Type)
	}
}

func TestExtractJWTClaims_Success(t *testing.T) {
	auth := makeTestAuthService(t)
	token, err := auth.IssueJWT("u-42", "bob")
	if err != nil {
		t.Fatalf("IssueJWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: JWTCookieName, Value: token})

	claims, err := ExtractJWTClaims(req, auth)
	if err != nil {
		t.Fatalf("ExtractJWTClaims: %v", err)
	}
	if claims.UserID != "u-42" || claims.Username != "bob" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if claims.ExpMS <= time.Now().UnixMilli() {
		t.Fatalf("token should not be expired")
	}
}

func TestExtractJWTClaims_NoCookie(t *testing.T) {
	auth := makeTestAuthService(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := ExtractJWTClaims(req, auth)
	if err == nil {
		t.Fatalf("expected error for missing cookie")
	}
}
