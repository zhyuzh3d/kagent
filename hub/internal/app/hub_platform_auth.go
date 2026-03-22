package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func (h *HubPlatform) PrepareServiceBootstrap(serviceID string, instanceID string, registerURL string, ttl time.Duration) (hubsvc.BootstrapSecret, error) {
	if h == nil {
		return hubsvc.BootstrapSecret{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	if sid == "" || iid == "" {
		return hubsvc.BootstrapSecret{}, fmt.Errorf("service_id and instance_id are required")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	issuedAtMS := nowMS()
	expiresAtMS := issuedAtMS + ttl.Milliseconds()
	s2hToken, err := randomAuthToken()
	if err != nil {
		return hubsvc.BootstrapSecret{}, err
	}
	h2sToken, err := randomAuthToken()
	if err != nil {
		return hubsvc.BootstrapSecret{}, err
	}
	for h2sToken == s2hToken {
		h2sToken, err = randomAuthToken()
		if err != nil {
			return hubsvc.BootstrapSecret{}, err
		}
	}
	auth := HubServiceAuth{
		ServiceID:   sid,
		InstanceID:  iid,
		S2HToken:    s2hToken,
		H2SToken:    h2sToken,
		IssuedAtMS:  issuedAtMS,
		ExpiresAtMS: expiresAtMS,
	}
	h.mu.Lock()
	h.serviceAuths[sid] = auth
	h.mu.Unlock()
	bootstrap := hubsvc.BootstrapSecret{
		ServiceID:      sid,
		InstanceID:     iid,
		HubRegisterURL: strings.TrimSpace(registerURL),
		S2HToken:       s2hToken,
		H2SToken:       h2sToken,
		IssuedAtMS:     issuedAtMS,
		ExpiresAtMS:    expiresAtMS,
	}
	if err := bootstrap.Validate(); err != nil {
		return hubsvc.BootstrapSecret{}, err
	}
	return bootstrap, nil
}

func (h *HubPlatform) ServiceHubAuth(serviceID string) (HubServiceAuth, bool) {
	if h == nil {
		return HubServiceAuth{}, false
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return HubServiceAuth{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	auth, ok := h.serviceAuths[sid]
	return auth, ok
}

func (h *HubPlatform) VerifyServiceAuth(serviceID string, instanceID string, token string) (HubServiceAuth, error) {
	if h == nil {
		return HubServiceAuth{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	expectedToken := strings.TrimSpace(token)
	if sid == "" || iid == "" || expectedToken == "" {
		return HubServiceAuth{}, fmt.Errorf("service auth headers are required")
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	auth, ok := h.serviceAuths[sid]
	if !ok {
		return HubServiceAuth{}, fmt.Errorf("service auth missing")
	}
	if strings.TrimSpace(auth.InstanceID) != iid {
		return HubServiceAuth{}, fmt.Errorf("service instance mismatch")
	}
	if strings.TrimSpace(auth.S2HToken) != expectedToken {
		return HubServiceAuth{}, fmt.Errorf("service auth token mismatch")
	}
	return auth, nil
}

func (h *HubPlatform) VerifyHubAuth(serviceID string, instanceID string, token string) (HubServiceAuth, error) {
	if h == nil {
		return HubServiceAuth{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	expectedToken := strings.TrimSpace(token)
	if sid == "" || expectedToken == "" {
		return HubServiceAuth{}, fmt.Errorf("hub auth headers are required")
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	auth, ok := h.serviceAuths[sid]
	if !ok {
		return HubServiceAuth{}, fmt.Errorf("service auth missing")
	}
	if iid != "" && strings.TrimSpace(auth.InstanceID) != iid {
		return HubServiceAuth{}, fmt.Errorf("service instance mismatch")
	}
	if strings.TrimSpace(auth.H2SToken) != expectedToken {
		return HubServiceAuth{}, fmt.Errorf("hub auth token mismatch")
	}
	return auth, nil
}

func randomAuthToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate auth token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (h *HubPlatform) IssueOriginCallerToken(origin toolproto.Caller, serviceID string, requestID string, traceID string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("hub platform is nil")
	}
	claims := hubsvc.OriginCallerTokenClaims{
		OriginCaller:       origin,
		IssuedAtMS:         time.Now().UnixMilli(),
		ExpiresAtMS:        time.Now().Add(originCallerTokenTTL).UnixMilli(),
		IssuedForServiceID: strings.TrimSpace(serviceID),
		RequestID:          strings.TrimSpace(requestID),
		TraceID:            strings.TrimSpace(traceID),
	}
	return hubsvc.SignOriginCallerToken(h.originSecret, claims)
}

func (h *HubPlatform) VerifyOriginCallerToken(token string, expectedServiceID string) (hubsvc.OriginCallerTokenClaims, error) {
	if h == nil {
		return hubsvc.OriginCallerTokenClaims{}, fmt.Errorf("hub platform is nil")
	}
	claims, err := hubsvc.VerifyOriginCallerToken(h.originSecret, token)
	if err != nil {
		return hubsvc.OriginCallerTokenClaims{}, err
	}
	expected := strings.TrimSpace(expectedServiceID)
	if expected != "" && strings.TrimSpace(claims.IssuedForServiceID) != "" && strings.TrimSpace(claims.IssuedForServiceID) != expected {
		return hubsvc.OriginCallerTokenClaims{}, fmt.Errorf("origin caller token target mismatch")
	}
	return claims, nil
}

func (h *HubPlatform) VerifySurfaceToken(token string) (hubsvc.SurfaceTokenClaims, error) {
	if h == nil {
		return hubsvc.SurfaceTokenClaims{}, fmt.Errorf("hub platform is nil")
	}
	return hubsvc.VerifySurfaceToken(h.surfaceSecret, token)
}
