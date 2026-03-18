package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err == nil && len(decoded) >= 32 {
			return decoded[:32], nil
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create secret dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	return secret, nil
}
