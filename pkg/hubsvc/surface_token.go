package hubsvc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	HeaderSurfaceToken = "X-Surface-Token"

	SurfaceTokenKindSession    = "surface_session"
	SurfaceTokenKindCapability = "surface_capability"
)

type SurfaceTokenClaims struct {
	Kind       string `json:"kind"`
	UserID     string `json:"user_id"`
	SurfaceID  string `json:"surface_id"`
	Scope      string `json:"scope,omitempty"`
	PathPrefix string `json:"path_prefix,omitempty"`
	ExpMS      int64  `json:"exp_ms"`
	Nonce      string `json:"nonce"`
}

func SignSurfaceToken(secret []byte, claims SurfaceTokenClaims) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("surface token secret is empty")
	}
	claims.Kind = strings.TrimSpace(claims.Kind)
	claims.UserID = strings.TrimSpace(claims.UserID)
	claims.SurfaceID = strings.TrimSpace(claims.SurfaceID)
	claims.Scope = strings.TrimSpace(claims.Scope)
	claims.PathPrefix = strings.TrimSpace(claims.PathPrefix)
	claims.Nonce = strings.TrimSpace(claims.Nonce)
	if claims.ExpMS <= time.Now().UnixMilli() {
		return "", fmt.Errorf("surface token expiration must be in the future")
	}
	if claims.UserID == "" || claims.SurfaceID == "" {
		return "", fmt.Errorf("surface token missing user_id or surface_id")
	}
	payloadRaw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal surface token claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadRaw)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func VerifySurfaceToken(secret []byte, token string) (SurfaceTokenClaims, error) {
	if len(secret) == 0 {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token secret is empty")
	}
	clean := strings.TrimSpace(token)
	if clean == "" {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token is empty")
	}
	parts := strings.Split(clean, ".")
	if len(parts) != 2 {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token format is invalid")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token signature is invalid")
	}
	if !hmac.Equal(got, expected) {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token signature mismatch")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token payload is invalid")
	}
	var claims SurfaceTokenClaims
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token claims parse failed")
	}
	claims.Kind = strings.TrimSpace(claims.Kind)
	claims.UserID = strings.TrimSpace(claims.UserID)
	claims.SurfaceID = strings.TrimSpace(claims.SurfaceID)
	claims.Scope = strings.TrimSpace(claims.Scope)
	claims.PathPrefix = strings.TrimSpace(claims.PathPrefix)
	claims.Nonce = strings.TrimSpace(claims.Nonce)
	if claims.ExpMS <= time.Now().UnixMilli() {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token is expired")
	}
	if claims.UserID == "" || claims.SurfaceID == "" {
		return SurfaceTokenClaims{}, fmt.Errorf("surface token missing user_id or surface_id")
	}
	return claims, nil
}
