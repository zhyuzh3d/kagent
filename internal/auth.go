package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	JWTCookieName    = "kagent_token"
	JWTMaxAgeSec     = 30 * 24 * 3600 // 30 days
	PasswordMinLen   = 6
	passwordSaltLen  = 16
	jwtSecretLen     = 32
	jwtSecretFile    = ".jwt_secret"
)

type JWTClaims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	ExpMS    int64  `json:"exp_ms"`
}

// AuthService provides JWT token management and password hashing.
type AuthService struct {
	secret []byte
}

// NewAuthService creates an AuthService. It loads or generates a persistent
// JWT signing secret stored at <dataRoot>/.jwt_secret.
func NewAuthService(dataRoot string) (*AuthService, error) {
	secretPath := filepath.Join(strings.TrimSpace(dataRoot), jwtSecretFile)
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, fmt.Errorf("auth secret init: %w", err)
	}
	return &AuthService{secret: secret}, nil
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err == nil && len(decoded) >= jwtSecretLen {
			return decoded[:jwtSecretLen], nil
		}
	}
	secret := make([]byte, jwtSecretLen)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate jwt secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create secret dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write jwt secret: %w", err)
	}
	return secret, nil
}

// HashPassword produces a salted SHA-256 hash string: "salt:hash" (hex encoded).
func HashPassword(password string) (string, error) {
	if len(password) < PasswordMinLen {
		return "", fmt.Errorf("password must be at least %d characters", PasswordMinLen)
	}
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	saltHex := hex.EncodeToString(salt)
	hash := sha256.Sum256([]byte(saltHex + ":" + password))
	return saltHex + ":" + hex.EncodeToString(hash[:]), nil
}

// VerifyPassword checks a password against a "salt:hash" string.
func VerifyPassword(password string, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	saltHex := parts[0]
	hash := sha256.Sum256([]byte(saltHex + ":" + password))
	return hex.EncodeToString(hash[:]) == parts[1]
}

// IssueJWT creates a signed JWT token string valid for JWTMaxAgeSec.
func (a *AuthService) IssueJWT(userID string, username string) (string, error) {
	if a == nil || len(a.secret) == 0 {
		return "", fmt.Errorf("auth service not initialized")
	}
	claims := JWTClaims{
		UserID:   strings.TrimSpace(userID),
		Username: strings.TrimSpace(username),
		ExpMS:    time.Now().Add(time.Duration(JWTMaxAgeSec) * time.Second).UnixMilli(),
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal jwt claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// ParseJWT verifies and decodes a JWT token string into claims.
func (a *AuthService) ParseJWT(token string) (JWTClaims, error) {
	if a == nil || len(a.secret) == 0 {
		return JWTClaims{}, fmt.Errorf("auth service not initialized")
	}
	clean := strings.TrimSpace(token)
	if clean == "" {
		return JWTClaims{}, fmt.Errorf("token is empty")
	}
	parts := strings.SplitN(clean, ".", 2)
	if len(parts) != 2 {
		return JWTClaims{}, fmt.Errorf("invalid token format")
	}
	payload := parts[0]
	sigGiven, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return JWTClaims{}, fmt.Errorf("invalid token signature encoding")
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(payload))
	sigExpected := mac.Sum(nil)
	if !hmac.Equal(sigExpected, sigGiven) {
		return JWTClaims{}, fmt.Errorf("token signature mismatch")
	}
	rawClaims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return JWTClaims{}, fmt.Errorf("invalid token payload encoding")
	}
	var claims JWTClaims
	if err := json.Unmarshal(rawClaims, &claims); err != nil {
		return JWTClaims{}, fmt.Errorf("invalid token claims")
	}
	if claims.ExpMS <= time.Now().UnixMilli() {
		return JWTClaims{}, fmt.Errorf("token expired")
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return JWTClaims{}, fmt.Errorf("token missing user_id")
	}
	return claims, nil
}
