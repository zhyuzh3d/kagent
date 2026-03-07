package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type VersionInfo struct {
	Format  string `json:"format"`
	Backend string `json:"backend"`
	WebUI   string `json:"webui"`
}

func LoadVersionInfo(path string) (*VersionInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read version file: %w", err)
	}
	var v VersionInfo
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse version file: %w", err)
	}
	v.Format = strings.TrimSpace(v.Format)
	v.Backend = strings.TrimSpace(v.Backend)
	v.WebUI = strings.TrimSpace(v.WebUI)
	if v.Backend == "" || v.WebUI == "" {
		return nil, errors.New("version file is missing backend/webui")
	}
	if v.Format == "" {
		v.Format = "calver-yymmddnn"
	}
	return &v, nil
}

