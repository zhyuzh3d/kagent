package app

import (
	"crypto/ed25519"
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
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	JWTCookieName          = "kagent_token"
	AccountTokenCookieName = "svc.account.token"
	JWTMaxAgeSec           = 30 * 24 * 3600 // 30 days
	PasswordMinLen         = 6
	jwtSecretLen           = 32
	jwtSecretFile          = ".jwt_secret"
	passwordHashV2         = "bcrypt$"
)

type JWTClaims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	SID      string `json:"sid,omitempty"`
	KID      string `json:"kid,omitempty"`
	IatMS    int64  `json:"iat_ms,omitempty"`
	ExpMS    int64  `json:"exp_ms"`
}

type AccountPublicKey struct {
	KID       string `json:"kid"`
	Alg       string `json:"alg"`
	PublicKey string `json:"public_key"`
}

// AuthService provides JWT token management and password hashing.
type AuthService struct {
	legacySecret []byte

	mu             sync.RWMutex
	accountKeyring map[string]ed25519.PublicKey
	activeSID      map[string]string
}

// NewAuthService creates an AuthService. It loads or generates a persistent
// legacy JWT signing secret stored at <dataRoot>/.jwt_secret.
func NewAuthService(dataRoot string) (*AuthService, error) {
	secretPath := filepath.Join(strings.TrimSpace(dataRoot), jwtSecretFile)
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, fmt.Errorf("auth secret init: %w", err)
	}
	return &AuthService{
		legacySecret:   secret,
		accountKeyring: map[string]ed25519.PublicKey{},
		activeSID:      map[string]string{},
	}, nil
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

// HashPassword returns a bcrypt password hash with a version prefix.
func HashPassword(password string) (string, error) {
	if len(password) < PasswordMinLen {
		return "", fmt.Errorf("password must be at least %d characters", PasswordMinLen)
	}
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return passwordHashV2 + string(hashBytes), nil
}

func isBcryptHash(stored string) bool {
	return strings.HasPrefix(strings.TrimSpace(stored), passwordHashV2)
}

// VerifyPassword checks both new bcrypt and legacy salted SHA-256 hash strings.
func VerifyPassword(password string, stored string) bool {
	clean := strings.TrimSpace(stored)
	if clean == "" {
		return false
	}
	if isBcryptHash(clean) {
		hash := strings.TrimPrefix(clean, passwordHashV2)
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	// Legacy fallback: "salt:hash"
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	saltHex := parts[0]
	hash := sha256.Sum256([]byte(saltHex + ":" + password))
	return hex.EncodeToString(hash[:]) == parts[1]
}

func NeedsPasswordRehash(stored string) bool {
	return !isBcryptHash(stored)
}

// IssueJWT creates a legacy signed JWT token string valid for JWTMaxAgeSec.
func (a *AuthService) IssueJWT(userID string, username string) (string, error) {
	if a == nil || len(a.legacySecret) == 0 {
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
	mac := hmac.New(sha256.New, a.legacySecret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// ParseJWT verifies and decodes a legacy JWT token string into claims.
func (a *AuthService) ParseJWT(token string) (JWTClaims, error) {
	if a == nil || len(a.legacySecret) == 0 {
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
	mac := hmac.New(sha256.New, a.legacySecret)
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

func (a *AuthService) SetAccountPublicKeys(keys []AccountPublicKey) {
	if a == nil {
		return
	}
	next := map[string]ed25519.PublicKey{}
	for _, key := range keys {
		kid := strings.TrimSpace(key.KID)
		if kid == "" {
			continue
		}
		decoded, err := decodePublicKey(key.PublicKey)
		if err != nil {
			continue
		}
		next[kid] = decoded
	}
	a.mu.Lock()
	a.accountKeyring = next
	a.mu.Unlock()
}

func (a *AuthService) ReplaceActiveSessions(next map[string]string) {
	if a == nil {
		return
	}
	clean := map[string]string{}
	for userID, sid := range next {
		uid := strings.TrimSpace(userID)
		sessionID := strings.TrimSpace(sid)
		if uid == "" || sessionID == "" {
			continue
		}
		clean[uid] = sessionID
	}
	a.mu.Lock()
	a.activeSID = clean
	a.mu.Unlock()
}

func (a *AuthService) SetActiveSession(userID string, sid string) {
	if a == nil {
		return
	}
	uid := strings.TrimSpace(userID)
	sessionID := strings.TrimSpace(sid)
	if uid == "" {
		return
	}
	a.mu.Lock()
	if sessionID == "" {
		delete(a.activeSID, uid)
	} else {
		a.activeSID[uid] = sessionID
	}
	a.mu.Unlock()
}

func (a *AuthService) ParseAccountToken(token string) (JWTClaims, error) {
	if a == nil {
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
	payload := strings.TrimSpace(parts[0])
	if payload == "" {
		return JWTClaims{}, fmt.Errorf("invalid token payload")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return JWTClaims{}, fmt.Errorf("invalid token signature encoding")
	}
	rawClaims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return JWTClaims{}, fmt.Errorf("invalid token payload encoding")
	}
	var claims JWTClaims
	if err := json.Unmarshal(rawClaims, &claims); err != nil {
		return JWTClaims{}, fmt.Errorf("invalid token claims")
	}
	claims.UserID = strings.TrimSpace(claims.UserID)
	claims.Username = strings.TrimSpace(claims.Username)
	claims.SID = strings.TrimSpace(claims.SID)
	claims.KID = strings.TrimSpace(claims.KID)
	if claims.ExpMS <= time.Now().UnixMilli() {
		return JWTClaims{}, fmt.Errorf("token expired")
	}
	if claims.UserID == "" {
		return JWTClaims{}, fmt.Errorf("token missing user_id")
	}
	if claims.SID == "" {
		return JWTClaims{}, fmt.Errorf("token missing sid")
	}
	if claims.KID == "" {
		return JWTClaims{}, fmt.Errorf("token missing kid")
	}
	a.mu.RLock()
	publicKey, keyOK := a.accountKeyring[claims.KID]
	activeSID, sidOK := a.activeSID[claims.UserID]
	a.mu.RUnlock()
	if !keyOK || len(publicKey) != ed25519.PublicKeySize {
		return JWTClaims{}, fmt.Errorf("token key not found")
	}
	if !ed25519.Verify(publicKey, []byte(payload), sig) {
		return JWTClaims{}, fmt.Errorf("token signature mismatch")
	}
	if !sidOK || strings.TrimSpace(activeSID) == "" {
		return JWTClaims{}, fmt.Errorf("token session not found")
	}
	if strings.TrimSpace(activeSID) != claims.SID {
		return JWTClaims{}, fmt.Errorf("token session mismatch")
	}
	if claims.Username == "" {
		claims.Username = claims.UserID
	}
	return claims, nil
}

func decodePublicKey(raw string) (ed25519.PublicKey, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return nil, fmt.Errorf("empty public key")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(clean)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, err
		}
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length")
	}
	return ed25519.PublicKey(decoded), nil
}
