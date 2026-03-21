package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (h *HubPlatform) IssueServiceSessionToken(serviceID string, instanceID string, ttl time.Duration) (string, int64, error) {
	if h == nil {
		return "", 0, fmt.Errorf("hub is nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.issueServiceSessionTokenLocked(serviceID, instanceID, ttl)
}

func (h *HubPlatform) issueServiceSessionTokenLocked(serviceID string, instanceID string, ttl time.Duration) (string, int64, error) {
	if ttl <= 0 {
		ttl = h.sessionTTL
	}
	claims := HubServiceSessionClaims{
		ServiceID:  strings.TrimSpace(serviceID),
		InstanceID: strings.TrimSpace(instanceID),
		ExpMS:      nowMS() + ttl.Milliseconds(),
		Nonce:      newRequestID(),
	}
	if claims.ServiceID == "" || claims.InstanceID == "" {
		return "", 0, fmt.Errorf("service token requires service_id and instance_id")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", 0, err
	}
	raw := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.serviceSecret)
	_, _ = mac.Write([]byte(raw))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return raw + "." + sig, claims.ExpMS, nil
}

func (h *HubPlatform) VerifyServiceSessionToken(token string) (HubServiceSessionClaims, error) {
	if h == nil {
		return HubServiceSessionClaims{}, fmt.Errorf("hub is nil")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return HubServiceSessionClaims{}, fmt.Errorf("invalid service token")
	}
	raw := parts[0]
	sig := parts[1]
	mac := hmac.New(sha256.New, h.serviceSecret)
	_, _ = mac.Write([]byte(raw))
	expect := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expect), []byte(sig)) {
		return HubServiceSessionClaims{}, fmt.Errorf("invalid service token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return HubServiceSessionClaims{}, fmt.Errorf("decode service token payload: %w", err)
	}
	var claims HubServiceSessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return HubServiceSessionClaims{}, fmt.Errorf("decode service token claims: %w", err)
	}
	if claims.ExpMS <= nowMS() {
		return HubServiceSessionClaims{}, fmt.Errorf("service token expired")
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	reg, ok := h.services[claims.ServiceID]
	if !ok {
		return HubServiceSessionClaims{}, fmt.Errorf("service not registered")
	}
	if reg.InstanceID != claims.InstanceID {
		return HubServiceSessionClaims{}, fmt.Errorf("service instance mismatch")
	}
	if reg.Status != ServiceStatusActive {
		return HubServiceSessionClaims{}, fmt.Errorf("service is not active")
	}
	return claims, nil
}
