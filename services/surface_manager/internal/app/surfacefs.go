package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	SurfaceTokenKindSession    = "surface_session"
	SurfaceTokenKindCapability = "surface_capability"

	SurfaceScopeRead   = "fs.read"
	SurfaceScopeWrite  = "fs.write"
	SurfaceScopeList   = "fs.list"
	SurfaceScopeDelete = "fs.delete"
	SurfaceScopeStatic = "fs.static"
	SurfaceScopeAll    = "fs.*"

	surfaceSecretFile = ".surface_secret"
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

type SurfaceFSService struct {
	dataRoot string
	secret   []byte

	mu      sync.Mutex
	runtime map[string]*surfaceTokenRuntime
}

type surfaceTokenRuntime struct {
	LastUserID            string `json:"last_user_id,omitempty"`
	SessionIssued         int64  `json:"session_issued"`
	CapabilityIssued      int64  `json:"capability_issued"`
	LastSessionIssueMS    int64  `json:"last_session_issue_ms"`
	LastCapabilityIssueMS int64  `json:"last_capability_issue_ms"`
	LastCapabilityScope   string `json:"last_capability_scope,omitempty"`
	LastCapabilityPath    string `json:"last_capability_path_prefix,omitempty"`
	activeSessionExpMS    []int64
	activeCapabilityExpMS []int64
}

type SurfaceRuntimeStatus struct {
	SurfaceID                  string `json:"surface_id"`
	LastUserID                 string `json:"last_user_id,omitempty"`
	SessionIssued              int64  `json:"session_issued"`
	CapabilityIssued           int64  `json:"capability_issued"`
	ActiveSessionTokenCount    int    `json:"active_session_token_count"`
	ActiveCapabilityTokenCount int    `json:"active_capability_token_count"`
	LastSessionIssueMS         int64  `json:"last_session_issue_ms"`
	LastCapabilityIssueMS      int64  `json:"last_capability_issue_ms"`
	LastCapabilityScope        string `json:"last_capability_scope,omitempty"`
	LastCapabilityPathPrefix   string `json:"last_capability_path_prefix,omitempty"`
}

func NewSurfaceFSService(dataRoot string) (*SurfaceFSService, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, fmt.Errorf("surfacefs data root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve surfacefs data root: %w", err)
	}
	secretPath := filepath.Join(absRoot, surfaceSecretFile)
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, fmt.Errorf("init surfacefs secret: %w", err)
	}
	return &SurfaceFSService{
		dataRoot: absRoot,
		secret:   secret,
		runtime:  map[string]*surfaceTokenRuntime{},
	}, nil
}

func (s *SurfaceFSService) IssueSurfaceSessionToken(userID string, surfaceID string, ttl time.Duration) (string, int64, error) {
	uid := strings.TrimSpace(userID)
	sid := strings.TrimSpace(surfaceID)
	if uid == "" || sid == "" {
		return "", 0, fmt.Errorf("surface session token requires user_id and surface_id")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	claims := SurfaceTokenClaims{
		Kind:      SurfaceTokenKindSession,
		UserID:    uid,
		SurfaceID: sid,
		ExpMS:     nowMS() + ttl.Milliseconds(),
		Nonce:     newRequestID(),
	}
	token, err := s.signClaims(claims)
	if err != nil {
		return "", 0, err
	}
	s.noteSessionIssued(claims)
	return token, claims.ExpMS, nil
}

func (s *SurfaceFSService) IssueCapabilityTokenFromSession(sessionToken string, scope string, pathPrefix string, ttl time.Duration) (string, int64, error) {
	claims, err := s.verifyClaims(sessionToken)
	if err != nil {
		return "", 0, err
	}
	if claims.Kind != SurfaceTokenKindSession {
		return "", 0, fmt.Errorf("token kind is not surface_session")
	}
	cleanScope := normalizeCapabilityScope(scope)
	if cleanScope == "" {
		return "", 0, fmt.Errorf("invalid capability scope")
	}
	cleanPrefix, err := normalizeRelativePath(pathPrefix)
	if err != nil {
		return "", 0, fmt.Errorf("invalid path_prefix: %w", err)
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	exp := nowMS() + ttl.Milliseconds()
	if exp > claims.ExpMS {
		exp = claims.ExpMS
	}
	capClaims := SurfaceTokenClaims{
		Kind:       SurfaceTokenKindCapability,
		UserID:     claims.UserID,
		SurfaceID:  claims.SurfaceID,
		Scope:      cleanScope,
		PathPrefix: cleanPrefix,
		ExpMS:      exp,
		Nonce:      newRequestID(),
	}
	token, err := s.signClaims(capClaims)
	if err != nil {
		return "", 0, err
	}
	s.noteCapabilityIssued(capClaims)
	return token, capClaims.ExpMS, nil
}

func (s *SurfaceFSService) noteSessionIssued(claims SurfaceTokenClaims) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := s.ensureRuntimeLocked(claims.SurfaceID)
	rt.LastUserID = claims.UserID
	rt.SessionIssued++
	rt.LastSessionIssueMS = nowMS()
	rt.activeSessionExpMS = append(rt.activeSessionExpMS, claims.ExpMS)
	s.pruneRuntimeLocked(rt, nowMS())
}

func (s *SurfaceFSService) noteCapabilityIssued(claims SurfaceTokenClaims) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := s.ensureRuntimeLocked(claims.SurfaceID)
	rt.LastUserID = claims.UserID
	rt.CapabilityIssued++
	rt.LastCapabilityIssueMS = nowMS()
	rt.LastCapabilityScope = claims.Scope
	rt.LastCapabilityPath = claims.PathPrefix
	rt.activeCapabilityExpMS = append(rt.activeCapabilityExpMS, claims.ExpMS)
	s.pruneRuntimeLocked(rt, nowMS())
}

func (s *SurfaceFSService) ensureRuntimeLocked(surfaceID string) *surfaceTokenRuntime {
	key := strings.TrimSpace(surfaceID)
	if key == "" {
		key = "unknown"
	}
	rt, ok := s.runtime[key]
	if !ok {
		rt = &surfaceTokenRuntime{}
		s.runtime[key] = rt
	}
	return rt
}

func (s *SurfaceFSService) pruneRuntimeLocked(rt *surfaceTokenRuntime, now int64) {
	keepSessions := rt.activeSessionExpMS[:0]
	for _, exp := range rt.activeSessionExpMS {
		if exp > now {
			keepSessions = append(keepSessions, exp)
		}
	}
	rt.activeSessionExpMS = keepSessions
	keepCaps := rt.activeCapabilityExpMS[:0]
	for _, exp := range rt.activeCapabilityExpMS {
		if exp > now {
			keepCaps = append(keepCaps, exp)
		}
	}
	rt.activeCapabilityExpMS = keepCaps
}

func (s *SurfaceFSService) RuntimeStatus(surfaceID string) SurfaceRuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(surfaceID)
	status := SurfaceRuntimeStatus{
		SurfaceID: key,
	}
	rt, ok := s.runtime[key]
	if !ok {
		return status
	}
	s.pruneRuntimeLocked(rt, nowMS())
	status.LastUserID = rt.LastUserID
	status.SessionIssued = rt.SessionIssued
	status.CapabilityIssued = rt.CapabilityIssued
	status.ActiveSessionTokenCount = len(rt.activeSessionExpMS)
	status.ActiveCapabilityTokenCount = len(rt.activeCapabilityExpMS)
	status.LastSessionIssueMS = rt.LastSessionIssueMS
	status.LastCapabilityIssueMS = rt.LastCapabilityIssueMS
	status.LastCapabilityScope = rt.LastCapabilityScope
	status.LastCapabilityPathPrefix = rt.LastCapabilityPath
	return status
}

func (s *SurfaceFSService) ParseAnySurfaceToken(token string) (SurfaceTokenClaims, error) {
	return s.verifyClaims(token)
}

func (s *SurfaceFSService) ValidateCapabilityToken(capabilityToken string, requiredScope string, surfaceID string, relPath string) (SurfaceTokenClaims, error) {
	claims, _, err := s.verifyCapability(capabilityToken, requiredScope, surfaceID, relPath)
	if err != nil {
		return SurfaceTokenClaims{}, err
	}
	return claims, nil
}

func (s *SurfaceFSService) ValidateCapabilityRequest(capabilityToken string, requiredScope string, surfaceID string, relPath string) (SurfaceTokenClaims, string, error) {
	claims, cleanPath, err := s.verifyCapability(capabilityToken, requiredScope, surfaceID, relPath)
	if err != nil {
		return SurfaceTokenClaims{}, "", err
	}
	return claims, cleanPath, nil
}

func (s *SurfaceFSService) verifyCapability(capabilityToken string, requiredScope string, surfaceID string, relPath string) (SurfaceTokenClaims, string, error) {
	claims, err := s.verifyClaims(capabilityToken)
	if err != nil {
		return SurfaceTokenClaims{}, "", err
	}
	if claims.Kind != SurfaceTokenKindCapability {
		return SurfaceTokenClaims{}, "", fmt.Errorf("token kind is not surface_capability")
	}
	if strings.TrimSpace(surfaceID) != "" && strings.TrimSpace(surfaceID) != strings.TrimSpace(claims.SurfaceID) {
		return SurfaceTokenClaims{}, "", fmt.Errorf("surface_id mismatch")
	}
	if !capabilityScopeAllows(claims.Scope, requiredScope) {
		return SurfaceTokenClaims{}, "", fmt.Errorf("capability scope denied: required=%s", requiredScope)
	}
	cleanPath, err := normalizeRelativePath(relPath)
	if err != nil {
		return SurfaceTokenClaims{}, "", err
	}
	if !pathPrefixAllows(claims.PathPrefix, cleanPath) {
		return SurfaceTokenClaims{}, "", fmt.Errorf("path out of capability prefix")
	}
	return claims, cleanPath, nil
}

func normalizeCapabilityScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case SurfaceScopeRead:
		return SurfaceScopeRead
	case SurfaceScopeWrite:
		return SurfaceScopeWrite
	case SurfaceScopeList:
		return SurfaceScopeList
	case SurfaceScopeDelete:
		return SurfaceScopeDelete
	case SurfaceScopeStatic:
		return SurfaceScopeStatic
	case SurfaceScopeAll:
		return SurfaceScopeAll
	default:
		return ""
	}
}

func capabilityScopeAllows(granted string, required string) bool {
	grant := normalizeCapabilityScope(granted)
	need := normalizeCapabilityScope(required)
	if grant == "" || need == "" {
		return false
	}
	if grant == SurfaceScopeAll {
		return true
	}
	return grant == need
}

func pathPrefixAllows(prefix string, relPath string) bool {
	cleanPrefix, err := normalizeRelativePath(prefix)
	if err != nil {
		return false
	}
	cleanRel, err := normalizeRelativePath(relPath)
	if err != nil {
		return false
	}
	if cleanPrefix == "." {
		return true
	}
	relSlash := filepath.ToSlash(cleanRel)
	prefixSlash := filepath.ToSlash(cleanPrefix)
	return relSlash == prefixSlash || strings.HasPrefix(relSlash, prefixSlash+"/")
}

func normalizeRelativePath(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" || clean == "." || clean == "/" {
		return ".", nil
	}
	clean = filepath.Clean(clean)
	if clean == "." {
		return ".", nil
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute path is forbidden")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal is forbidden")
	}
	if strings.Contains(clean, "\x00") {
		return "", fmt.Errorf("invalid path")
	}
	return clean, nil
}

func (s *SurfaceFSService) signClaims(claims SurfaceTokenClaims) (string, error) {
	if s == nil || len(s.secret) == 0 {
		return "", fmt.Errorf("surfacefs secret is not ready")
	}
	if claims.ExpMS <= nowMS() {
		return "", fmt.Errorf("token expiration must be in the future")
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal token claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func (s *SurfaceFSService) verifyClaims(token string) (SurfaceTokenClaims, error) {
	clean := strings.TrimSpace(token)
	if clean == "" {
		return SurfaceTokenClaims{}, fmt.Errorf("token is empty")
	}
	parts := strings.Split(clean, ".")
	if len(parts) != 2 {
		return SurfaceTokenClaims{}, fmt.Errorf("token format is invalid")
	}
	payload := parts[0]
	sigGivenRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SurfaceTokenClaims{}, fmt.Errorf("token signature is invalid")
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	sigExpected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(sigExpected, sigGivenRaw) != 1 {
		return SurfaceTokenClaims{}, fmt.Errorf("token signature mismatch")
	}
	rawClaims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return SurfaceTokenClaims{}, fmt.Errorf("token payload is invalid")
	}
	var claims SurfaceTokenClaims
	if err := json.Unmarshal(rawClaims, &claims); err != nil {
		return SurfaceTokenClaims{}, fmt.Errorf("token claims parse failed")
	}
	if claims.ExpMS <= nowMS() {
		return SurfaceTokenClaims{}, fmt.Errorf("token is expired")
	}
	if strings.TrimSpace(claims.UserID) == "" || strings.TrimSpace(claims.SurfaceID) == "" {
		return SurfaceTokenClaims{}, fmt.Errorf("token claims missing user_id or surface_id")
	}
	return claims, nil
}
