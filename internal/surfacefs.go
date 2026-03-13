package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

type SurfaceFSListEntry struct {
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	SizeBytes   int64  `json:"size_bytes"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

type SurfaceFSService struct {
	dataRoot string
	secret   []byte
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
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate surfacefs secret: %w", err)
	}
	return &SurfaceFSService{
		dataRoot: absRoot,
		secret:   secret,
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
	return token, capClaims.ExpMS, nil
}

func (s *SurfaceFSService) ReadFile(capabilityToken string, surfaceID string, relPath string) ([]byte, error) {
	claims, cleanPath, err := s.verifyCapability(capabilityToken, SurfaceScopeRead, surfaceID, relPath)
	if err != nil {
		return nil, err
	}
	_, targetPath, err := s.resolveFilePath(claims.UserID, claims.SurfaceID, cleanPath, false)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(targetPath)
	if err != nil {
		return nil, fmt.Errorf("surfacefs read stat: %w", err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("surfacefs read path is directory")
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("surfacefs read failed: %w", err)
	}
	return raw, nil
}

func (s *SurfaceFSService) WriteFile(capabilityToken string, surfaceID string, relPath string, data []byte) (int64, error) {
	claims, cleanPath, err := s.verifyCapability(capabilityToken, SurfaceScopeWrite, surfaceID, relPath)
	if err != nil {
		return 0, err
	}
	_, targetPath, err := s.resolveFilePath(claims.UserID, claims.SurfaceID, cleanPath, true)
	if err != nil {
		return 0, err
	}
	parent := filepath.Dir(targetPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return 0, fmt.Errorf("surfacefs ensure parent: %w", err)
	}
	if err := s.rejectSymlink(parent); err != nil {
		return 0, err
	}
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return 0, fmt.Errorf("surfacefs write failed: %w", err)
	}
	return int64(len(data)), nil
}

func (s *SurfaceFSService) ListDir(capabilityToken string, surfaceID string, relPath string) ([]SurfaceFSListEntry, error) {
	claims, cleanPath, err := s.verifyCapability(capabilityToken, SurfaceScopeList, surfaceID, relPath)
	if err != nil {
		return nil, err
	}
	_, targetPath, err := s.resolveFilePath(claims.UserID, claims.SurfaceID, cleanPath, true)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return nil, fmt.Errorf("surfacefs list ensure dir: %w", err)
	}
	entries, err := os.ReadDir(targetPath)
	if err != nil {
		return nil, fmt.Errorf("surfacefs list failed: %w", err)
	}
	out := make([]SurfaceFSListEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, SurfaceFSListEntry{
			Name:        entry.Name(),
			IsDir:       entry.IsDir(),
			SizeBytes:   info.Size(),
			UpdatedAtMS: info.ModTime().UnixMilli(),
		})
	}
	return out, nil
}

func (s *SurfaceFSService) DeletePath(capabilityToken string, surfaceID string, relPath string, recursive bool) error {
	claims, cleanPath, err := s.verifyCapability(capabilityToken, SurfaceScopeDelete, surfaceID, relPath)
	if err != nil {
		return err
	}
	if cleanPath == "." {
		return fmt.Errorf("surfacefs delete root is forbidden")
	}
	_, targetPath, err := s.resolveFilePath(claims.UserID, claims.SurfaceID, cleanPath, false)
	if err != nil {
		return err
	}
	fi, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("surfacefs delete stat: %w", err)
	}
	if fi.IsDir() {
		if !recursive {
			return fmt.Errorf("surfacefs delete dir requires recursive=true")
		}
		return os.RemoveAll(targetPath)
	}
	return os.Remove(targetPath)
}

func (s *SurfaceFSService) ResolveStaticFile(capabilityToken string, surfaceID string, relPath string) (string, error) {
	claims, cleanPath, err := s.verifyCapability(capabilityToken, SurfaceScopeStatic, surfaceID, relPath)
	if err != nil {
		return "", err
	}
	_, targetPath, err := s.resolveFilePath(claims.UserID, claims.SurfaceID, cleanPath, false)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(targetPath)
	if err != nil {
		return "", fmt.Errorf("surfacefs static stat: %w", err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("surfacefs static path is directory")
	}
	return targetPath, nil
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

func (s *SurfaceFSService) resolveFilePath(userID string, surfaceID string, relPath string, createRoot bool) (string, string, error) {
	uid, err := sanitizePathSegment(userID)
	if err != nil {
		return "", "", fmt.Errorf("invalid user_id: %w", err)
	}
	sid, err := sanitizePathSegment(surfaceID)
	if err != nil {
		return "", "", fmt.Errorf("invalid surface_id: %w", err)
	}
	if uid == "" || sid == "" {
		return "", "", fmt.Errorf("surfacefs path missing user_id or surface_id")
	}
	cleanRel, err := normalizeRelativePath(relPath)
	if err != nil {
		return "", "", err
	}
	baseDir := filepath.Join(s.dataRoot, "users", uid, "surface_data", sid)
	if createRoot {
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			return "", "", fmt.Errorf("surfacefs ensure root: %w", err)
		}
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", "", fmt.Errorf("surfacefs resolve base: %w", err)
	}
	target := baseAbs
	if cleanRel != "." {
		target = filepath.Join(baseAbs, cleanRel)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("surfacefs resolve target: %w", err)
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", "", fmt.Errorf("surfacefs resolve rel: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("surfacefs path escapes root")
	}
	if err := s.rejectSymlink(baseAbs); err != nil {
		return "", "", err
	}
	return baseAbs, targetAbs, nil
}

func sanitizePathSegment(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", fmt.Errorf("segment is empty")
	}
	if clean == "." || clean == ".." {
		return "", fmt.Errorf("segment is invalid")
	}
	if strings.ContainsAny(clean, `/\`) {
		return "", fmt.Errorf("segment contains path separator")
	}
	return clean, nil
}

func (s *SurfaceFSService) rejectSymlink(pathToCheck string) error {
	fi, err := os.Lstat(pathToCheck)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("surfacefs lstat failed: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("surfacefs symlink path is forbidden")
	}
	return nil
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
