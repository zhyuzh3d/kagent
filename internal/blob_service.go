package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BlobMetadata struct {
	BlobID      string `json:"blob_id"`
	UserID      string `json:"user_id"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	MIME        string `json:"mime"`
	CreatedAtMS int64  `json:"created_at_ms"`
	ExpiresAtMS int64  `json:"expires_at_ms"`
}

type blobDownloadClaims struct {
	BlobID string `json:"blob_id"`
	UserID string `json:"user_id"`
	ExpMS  int64  `json:"exp_ms"`
	Nonce  string `json:"nonce"`
}

type BlobService struct {
	rootDir string
	metaDir string
	dataDir string
	secret  []byte
}

func NewBlobService(dataRoot string) (*BlobService, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, fmt.Errorf("blob data root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve blob data root: %w", err)
	}
	blobRoot := filepath.Join(absRoot, "blobs")
	metaDir := filepath.Join(blobRoot, "meta")
	dataDir := filepath.Join(blobRoot, "data")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		return nil, fmt.Errorf("create blob meta dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create blob data dir: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate blob secret: %w", err)
	}
	return &BlobService{
		rootDir: blobRoot,
		metaDir: metaDir,
		dataDir: dataDir,
		secret:  secret,
	}, nil
}

func (s *BlobService) Put(userID string, mime string, data []byte, ttl time.Duration) (BlobMetadata, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return BlobMetadata{}, fmt.Errorf("user_id is empty")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	blobID := "blob-" + newRequestID()
	sum := sha256.Sum256(data)
	meta := BlobMetadata{
		BlobID:      blobID,
		UserID:      uid,
		Size:        int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		MIME:        firstNonEmpty(strings.TrimSpace(mime), "application/octet-stream"),
		CreatedAtMS: nowMS(),
		ExpiresAtMS: nowMS() + ttl.Milliseconds(),
	}
	dataPath := s.blobDataPath(blobID)
	metaPath := s.blobMetaPath(blobID)
	if err := os.WriteFile(dataPath, data, 0o644); err != nil {
		return BlobMetadata{}, fmt.Errorf("write blob data: %w", err)
	}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, append(raw, '\n'), 0o644); err != nil {
		_ = os.Remove(dataPath)
		return BlobMetadata{}, fmt.Errorf("write blob metadata: %w", err)
	}
	return meta, nil
}

func (s *BlobService) Get(userID string, blobID string) ([]byte, BlobMetadata, error) {
	meta, err := s.loadMeta(blobID)
	if err != nil {
		return nil, BlobMetadata{}, err
	}
	if strings.TrimSpace(meta.UserID) != strings.TrimSpace(userID) {
		return nil, BlobMetadata{}, fmt.Errorf("blob access denied")
	}
	if nowMS() > meta.ExpiresAtMS {
		return nil, BlobMetadata{}, fmt.Errorf("blob expired")
	}
	dataPath := s.blobDataPath(blobID)
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, BlobMetadata{}, fmt.Errorf("read blob data: %w", err)
	}
	return raw, meta, nil
}

func (s *BlobService) SignDownloadURL(userID string, blobID string, ttl time.Duration) (string, int64, error) {
	meta, err := s.loadMeta(blobID)
	if err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(meta.UserID) != strings.TrimSpace(userID) {
		return "", 0, fmt.Errorf("blob access denied")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > 2*time.Hour {
		ttl = 2 * time.Hour
	}
	expMS := nowMS() + ttl.Milliseconds()
	if expMS > meta.ExpiresAtMS {
		expMS = meta.ExpiresAtMS
	}
	claims := blobDownloadClaims{
		BlobID: blobID,
		UserID: strings.TrimSpace(userID),
		ExpMS:  expMS,
		Nonce:  newRequestID(),
	}
	token, err := s.signClaims(claims)
	if err != nil {
		return "", 0, err
	}
	return token, expMS, nil
}

func (s *BlobService) VerifyDownloadToken(blobID string, token string) (BlobMetadata, error) {
	claims, err := s.verifyClaims(token)
	if err != nil {
		return BlobMetadata{}, err
	}
	if strings.TrimSpace(claims.BlobID) != strings.TrimSpace(blobID) {
		return BlobMetadata{}, fmt.Errorf("blob_id mismatch")
	}
	meta, err := s.loadMeta(blobID)
	if err != nil {
		return BlobMetadata{}, err
	}
	if strings.TrimSpace(meta.UserID) != strings.TrimSpace(claims.UserID) {
		return BlobMetadata{}, fmt.Errorf("blob user mismatch")
	}
	if nowMS() > meta.ExpiresAtMS {
		return BlobMetadata{}, fmt.Errorf("blob expired")
	}
	return meta, nil
}

func (s *BlobService) ReadByID(blobID string) ([]byte, BlobMetadata, error) {
	meta, err := s.loadMeta(blobID)
	if err != nil {
		return nil, BlobMetadata{}, err
	}
	dataPath := s.blobDataPath(blobID)
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, BlobMetadata{}, fmt.Errorf("read blob data: %w", err)
	}
	return raw, meta, nil
}

func (s *BlobService) GC(now time.Time) (int, error) {
	entries, err := os.ReadDir(s.metaDir)
	if err != nil {
		return 0, fmt.Errorf("read blob meta dir: %w", err)
	}
	deleted := 0
	nowMSVal := now.UnixMilli()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		metaPath := filepath.Join(s.metaDir, entry.Name())
		b, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta BlobMetadata
		if err := json.Unmarshal(b, &meta); err != nil {
			continue
		}
		if meta.ExpiresAtMS > nowMSVal {
			continue
		}
		_ = os.Remove(metaPath)
		_ = os.Remove(s.blobDataPath(meta.BlobID))
		deleted++
	}
	return deleted, nil
}

func (s *BlobService) blobMetaPath(blobID string) string {
	return filepath.Join(s.metaDir, strings.TrimSpace(blobID)+".json")
}

func (s *BlobService) blobDataPath(blobID string) string {
	return filepath.Join(s.dataDir, strings.TrimSpace(blobID)+".bin")
}

func (s *BlobService) loadMeta(blobID string) (BlobMetadata, error) {
	cleanID := strings.TrimSpace(blobID)
	if cleanID == "" {
		return BlobMetadata{}, fmt.Errorf("blob_id is empty")
	}
	b, err := os.ReadFile(s.blobMetaPath(cleanID))
	if err != nil {
		return BlobMetadata{}, fmt.Errorf("read blob metadata: %w", err)
	}
	var meta BlobMetadata
	if err := json.Unmarshal(b, &meta); err != nil {
		return BlobMetadata{}, fmt.Errorf("decode blob metadata: %w", err)
	}
	return meta, nil
}

func (s *BlobService) signClaims(claims blobDownloadClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(raw))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return raw + "." + sig, nil
}

func (s *BlobService) verifyClaims(token string) (blobDownloadClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return blobDownloadClaims{}, fmt.Errorf("invalid token")
	}
	raw := parts[0]
	sig := parts[1]
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(raw))
	expect := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expect), []byte(sig)) != 1 {
		return blobDownloadClaims{}, fmt.Errorf("invalid token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return blobDownloadClaims{}, fmt.Errorf("decode token payload: %w", err)
	}
	var claims blobDownloadClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return blobDownloadClaims{}, fmt.Errorf("decode token claims: %w", err)
	}
	if claims.BlobID == "" || claims.UserID == "" {
		return blobDownloadClaims{}, fmt.Errorf("invalid token claims")
	}
	if nowMS() > claims.ExpMS {
		return blobDownloadClaims{}, fmt.Errorf("token expired")
	}
	return claims, nil
}
