package app

import (
	"context"
	"net/http"
	"strings"

	"kagent/pkg/hubsvc"
)

// IdentityType classifies the source of a request.
type IdentityType string

const (
	IdentityUser      IdentityType = "USER"
	IdentityService   IdentityType = "SERVICE"
	IdentitySurface   IdentityType = "SURFACE"
	IdentityAnonymous IdentityType = "ANONYMOUS"
)

// Identity represents the resolved caller of an HTTP request.
type Identity struct {
	Type IdentityType // USER, SERVICE, SURFACE, ANONYMOUS
	ID   string       // unique id (e.g. user_8f2a, ai_doubao)
	Name string       // display name for logging (e.g. zhyuzh)
}

type identityContextKey struct{}
type remoteAddrContextKey struct{}

var (
	IdentityContextKey   = identityContextKey{}
	RemoteAddrContextKey = remoteAddrContextKey{}
)

// ContextWithIdentity returns a copy of ctx carrying the given Identity.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// IdentityFromContext extracts the Identity from ctx.
// If no Identity was previously injected, it returns an ANONYMOUS identity.
func IdentityFromContext(ctx context.Context) Identity {
	if id, ok := ctx.Value(identityContextKey{}).(Identity); ok {
		return id
	}
	return Identity{Type: IdentityAnonymous, Name: "anonymous"}
}

// IdentityMiddleware creates an HTTP middleware that extracts caller identity
// from the request (JWT cookie, Service Token, etc.) and injects it into the request context.
// Downstream handlers and loggers can retrieve it via IdentityFromContext.
func IdentityMiddleware(authService *AuthService, hubPlatform *HubPlatform) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := Identity{Type: IdentityAnonymous, Name: "anonymous"}

			// 1. Check Service Auth Headers (High Priority for backend-to-backend)
			if hubPlatform != nil {
				serviceID, instanceID, serviceAuth := hubsvc.ExtractServiceAuthHeaders(r.Header)
				if serviceID != "" || instanceID != "" || serviceAuth != "" {
					if verified, err := hubPlatform.VerifyServiceAuth(serviceID, instanceID, serviceAuth); err == nil {
						identity = Identity{
							Type: IdentityService,
							ID:   strings.TrimSpace(verified.ServiceID),
							Name: strings.TrimSpace(verified.ServiceID),
						}
						goto INJECT
					}
				}
			}

			// 2. Check Surface Token (Capability Token) - To be refined when surface tokens are fully implemented
			// For now, look for a header as a placeholder
			if surfToken := strings.TrimSpace(r.Header.Get("X-Surface-Token")); surfToken != "" {
				// Dummy verification for now or placeholder for future logic
				identity = Identity{
					Type: IdentitySurface,
					ID:   "surf_placeholder",
					Name: "surface",
				}
				goto INJECT
			}

			// 3. User JWT Cookie (Legacy/Frontend)
			if claims, err := ExtractJWTClaims(r, authService); err == nil {
				identity = Identity{
					Type: IdentityUser,
					ID:   strings.TrimSpace(claims.UserID),
					Name: strings.TrimSpace(claims.Username),
				}
				if identity.Name == "" {
					identity.Name = identity.ID
				}
			}

		INJECT:
			ctx := ContextWithIdentity(r.Context(), identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ExtractJWTClaims is the canonical helper for parsing JWT from the request cookie.
// This is the single source of truth — replaces duplicate definitions in main.go
// and gateway/tool_handler.go.
func ExtractJWTClaims(r *http.Request, authService *AuthService) (JWTClaims, error) {
	if authService == nil {
		return JWTClaims{}, http.ErrNoCookie
	}
	if cookie, err := r.Cookie(AccountTokenCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		if claims, parseErr := authService.ParseAccountToken(cookie.Value); parseErr == nil {
			return claims, nil
		}
	}
	cookie, err := r.Cookie(JWTCookieName)
	if err != nil {
		return JWTClaims{}, err
	}
	return authService.ParseJWT(cookie.Value)
}
