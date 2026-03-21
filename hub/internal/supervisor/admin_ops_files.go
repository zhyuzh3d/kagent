package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ManagedServiceFile struct {
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	SizeBytes   int64  `json:"size_bytes"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func (m *LifecycleManager) ListServiceFiles(serviceID string) ([]ManagedServiceFile, string, error) {
	if m == nil {
		return nil, "", fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	out := make([]ManagedServiceFile, 0, 16)
	err := filepath.WalkDir(svc.dirAbs, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(svc.dirAbs, current)
		if err != nil || rel == "." {
			return err
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out = append(out, ManagedServiceFile{
			Path:        filepath.ToSlash(rel),
			IsDir:       d.IsDir(),
			SizeBytes:   info.Size(),
			UpdatedAtMS: info.ModTime().UnixMilli(),
		})
		return nil
	})
	return out, svc.dirAbs, err
}

func (m *LifecycleManager) ReadServiceFile(serviceID string, relPath string) ([]byte, string, error) {
	target, err := m.resolveServiceFile(serviceID, relPath)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(target)
	return raw, target, err
}

func (m *LifecycleManager) WriteServiceFile(serviceID string, relPath string, data []byte) (string, error) {
	target, err := m.resolveServiceFile(serviceID, relPath)
	if err != nil {
		return "", err
	}
	if !serviceFileEditable(target) {
		return "", fmt.Errorf("file type is not editable")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	return target, os.WriteFile(target, data, 0o644)
}

func isFile(path string) bool {
	fi, err := os.Stat(strings.TrimSpace(path))
	return err == nil && !fi.IsDir()
}

func isDir(path string) bool {
	fi, err := os.Stat(strings.TrimSpace(path))
	return err == nil && fi.IsDir()
}

func serviceFileEditable(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".go", ".json", ".md", ".txt", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func (m *LifecycleManager) resolveServiceFile(serviceID string, relPath string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	clean := strings.TrimSpace(relPath)
	if clean == "" {
		return "", fmt.Errorf("path is required")
	}
	clean = filepath.Clean(clean)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid or unsafe service file path")
	}
	target := filepath.Join(svc.dirAbs, clean)
	rel, err := filepath.Rel(svc.dirAbs, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("resolved path escapes service root: %s", target)
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return target, fmt.Errorf("file not found: %s (service_id=%s)", relPath, serviceID)
	}
	return target, nil
}
