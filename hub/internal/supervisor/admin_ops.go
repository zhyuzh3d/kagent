package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	app "kagent/hub/internal/app"
)

type ManagedServiceInfo struct {
	ServiceID           string `json:"service_id"`
	Dir                 string `json:"dir"`
	DirAbs              string `json:"dir_abs"`
	ExecPath            string `json:"exec_path"`
	RuntimeManifestPath string `json:"runtime_manifest_path"`
	ConfigPath          string `json:"config_path"`
	HasSourceConfig     bool   `json:"has_source_config"`
	HasRuntimeManifest  bool   `json:"has_runtime_manifest"`
	HasExec             bool   `json:"has_exec"`
	HasGoMod            bool   `json:"has_go_mod"`
	Registered          bool   `json:"registered"`
	Active              bool   `json:"active"`
	Healthy             bool   `json:"healthy"`
	Status              string `json:"status,omitempty"`
	InstanceID          string               `json:"instance_id,omitempty"`
	Endpoint            string               `json:"endpoint,omitempty"`
	PID                 int                  `json:"pid,omitempty"`
	Manifest            *app.ServiceManifest `json:"manifest,omitempty"`
}

type BuildResult struct {
	ServiceID string `json:"service_id"`
	Workdir   string `json:"workdir"`
	ExecPath  string `json:"exec_path"`
	TargetPkg string `json:"target_pkg"`
	Output    string `json:"output"`
}

type ManagedServiceFile struct {
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	SizeBytes   int64  `json:"size_bytes"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func (m *LifecycleManager) ListManagedServices() []ManagedServiceInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ManagedServiceInfo, 0, len(m.services))
	for _, svc := range m.services {
		out = append(out, m.describeManagedServiceLocked(svc))
	}
	slices.SortFunc(out, func(a ManagedServiceInfo, b ManagedServiceInfo) int {
		return strings.Compare(a.ServiceID, b.ServiceID)
	})
	return out
}

func (m *LifecycleManager) ManagedServiceInfo(serviceID string) (ManagedServiceInfo, bool) {
	if m == nil {
		return ManagedServiceInfo{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.managedServiceLocked(serviceID)
	if !ok {
		return ManagedServiceInfo{}, false
	}
	return m.describeManagedServiceLocked(svc), true
}

func (m *LifecycleManager) StartService(ctx context.Context, serviceID string) (StartupServiceOutcome, error) {
	if m == nil {
		return StartupServiceOutcome{}, fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return StartupServiceOutcome{}, fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	out := m.startService(ctx, &svc)
	if !out.Ready {
		return out, fmt.Errorf("%s", firstNonEmptyValue(strings.TrimSpace(out.ErrorText), "start service failed"))
	}
	return out, nil
}

func (m *LifecycleManager) StopService(serviceID string, timeout time.Duration) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	m.stopOne(serviceID, timeout)
	if reg, ok := m.hubPlatform.GetService(strings.TrimSpace(serviceID)); ok {
		if err := StopServiceRegistration(m.hubPlatform, reg, timeout); err != nil {
			return err
		}
		m.hubPlatform.UnregisterService(reg.ServiceID, reg.InstanceID)
		if m.registry != nil {
			m.registry.Unregister(reg.ServiceID, reg.InstanceID)
		}
	}
	return nil
}

func (m *LifecycleManager) RestartService(ctx context.Context, serviceID string, timeout time.Duration) (StartupServiceOutcome, error) {
	if err := m.StopService(serviceID, timeout); err != nil {
		return StartupServiceOutcome{}, err
	}
	return m.StartService(ctx, serviceID)
}

func (m *LifecycleManager) DrainService(serviceID string, timeout time.Duration) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return fmt.Errorf("service_id is required")
	}
	if reg, ok := m.hubPlatform.GetService(sid); ok {
		_, _, err := callServiceLifecycleTool(m.hubPlatform, reg, "service.lifecycle.drain", map[string]any{
			"reason": "hub admin drain",
		}, timeout)
		if err != nil {
			m.registry.MarkDraining(reg.ServiceID, reg.InstanceID)
			return nil
		}
		m.registry.MarkDraining(reg.ServiceID, reg.InstanceID)
		return nil
	}
	return nil
}

func (m *LifecycleManager) ReadRuntimeManifest(serviceID string) (RuntimeManifest, error) {
	if m == nil {
		return RuntimeManifest{}, fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return RuntimeManifest{}, fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	return loadRuntimeManifest(svc.manifest, strings.TrimSpace(serviceID))
}

func (m *LifecycleManager) WriteRuntimeManifest(serviceID string, manifest RuntimeManifest) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	if strings.TrimSpace(manifest.ServiceID) == "" {
		manifest.ServiceID = strings.TrimSpace(serviceID)
	}
	if strings.TrimSpace(manifest.ServiceID) != strings.TrimSpace(serviceID) {
		return fmt.Errorf("runtime manifest service_id mismatch")
	}
	if len(manifest.Entry.Args) == 0 {
		return fmt.Errorf("runtime manifest entry.args is required")
	}
	return writeJSONFileAtomic(svc.manifest, manifest)
}

func (m *LifecycleManager) ReadConfigJSON(serviceID string) (map[string]any, string, error) {
	if m == nil {
		return nil, "", fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	configPath := filepath.Join(svc.dirAbs, "config", "config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, configPath, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, configPath, err
	}
	return out, configPath, nil
}

func (m *LifecycleManager) WriteConfigJSON(serviceID string, value map[string]any) (string, error) {
	if m == nil {
		return "", fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	configPath := filepath.Join(svc.dirAbs, "config", "config.json")
	if value == nil {
		value = map[string]any{}
	}
	return configPath, writeJSONFileAtomic(configPath, value)
}

func (m *LifecycleManager) UpsertManagedService(entry LifecycleServiceEntry) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	clean := LifecycleServiceEntry{
		ServiceID: strings.TrimSpace(entry.ServiceID),
		Dir:       strings.TrimSpace(entry.Dir),
	}
	if clean.ServiceID == "" || clean.Dir == "" {
		return fmt.Errorf("service_id and dir are required")
	}
	managed, err := buildManagedService(m.appRoot, clean)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	replaced := false
	for i := range m.services {
		if strings.TrimSpace(m.services[i].entry.ServiceID) != clean.ServiceID {
			continue
		}
		m.services[i] = managed
		replaced = true
		break
	}
	if !replaced {
		m.services = append(m.services, managed)
	}
	return m.persistConfigLocked()
}

func (m *LifecycleManager) BuildService(ctx context.Context, serviceID string) (BuildResult, error) {
	if m == nil {
		return BuildResult{}, fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return BuildResult{}, fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	buildArgs, workdir, targetPkg, err := m.resolveBuildCommand(svc)
	if err != nil {
		return BuildResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(svc.execPath), 0o755); err != nil {
		return BuildResult{}, err
	}
	cmd := exec.CommandContext(ctx, buildArgs[0], buildArgs[1:]...)
	cmd.Dir = workdir
	raw, err := cmd.CombinedOutput()
	result := BuildResult{
		ServiceID: strings.TrimSpace(serviceID),
		Workdir:   workdir,
		ExecPath:  svc.execPath,
		TargetPkg: targetPkg,
		Output:    string(raw),
	}
	if err != nil {
		return result, fmt.Errorf("go build failed: %w", err)
	}
	srcManifest := filepath.Join(svc.dirAbs, "manifest.json")
	if rawManifest, readErr := os.ReadFile(srcManifest); readErr == nil {
		_ = os.MkdirAll(filepath.Dir(svc.manifest), 0o755)
		_ = os.WriteFile(svc.manifest, rawManifest, 0o644)
	}
	return result, nil
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

func (m *LifecycleManager) resolveBuildCommand(svc managedService) ([]string, string, string, error) {
	cmdDir := filepath.Join(svc.dirAbs, "cmd", strings.TrimSpace(svc.entry.ServiceID))
	if !isDir(cmdDir) {
		return nil, "", "", fmt.Errorf("service cmd dir not found: %s", cmdDir)
	}
	if isFile(filepath.Join(svc.dirAbs, "go.mod")) {
		return []string{"go", "build", "-buildvcs=false", "-o", svc.execPath, "./cmd/" + strings.TrimSpace(svc.entry.ServiceID)}, svc.dirAbs, "./cmd/" + strings.TrimSpace(svc.entry.ServiceID), nil
	}
	rel, err := filepath.Rel(m.appRoot, cmdDir)
	if err != nil {
		return nil, "", "", err
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") || rel == ".." {
		return nil, "", "", fmt.Errorf("custom service outside app root requires its own go.mod")
	}
	return []string{"go", "build", "-buildvcs=false", "-o", svc.execPath, "./" + rel}, m.appRoot, "./" + rel, nil
}

func (m *LifecycleManager) managedServiceLocked(serviceID string) (managedService, bool) {
	sid := strings.TrimSpace(serviceID)
	for _, svc := range m.services {
		if strings.TrimSpace(svc.entry.ServiceID) == sid {
			return svc, true
		}
	}
	return managedService{}, false
}

func (m *LifecycleManager) describeManagedServiceLocked(svc managedService) ManagedServiceInfo {
	info := ManagedServiceInfo{
		ServiceID:           strings.TrimSpace(svc.entry.ServiceID),
		Dir:                 strings.TrimSpace(svc.entry.Dir),
		DirAbs:              strings.TrimSpace(svc.dirAbs),
		ExecPath:            strings.TrimSpace(svc.execPath),
		RuntimeManifestPath: strings.TrimSpace(svc.manifest),
		ConfigPath:          filepath.Join(svc.dirAbs, "config", "config.json"),
		HasRuntimeManifest:  isFile(svc.manifest),
		HasExec:             isFile(svc.execPath),
		HasSourceConfig:     isFile(filepath.Join(svc.dirAbs, "config", "config.json")),
		HasGoMod:            isFile(filepath.Join(svc.dirAbs, "go.mod")),
	}
	if reg, ok := m.hubPlatform.GetService(info.ServiceID); ok {
		info.Registered = true
		info.Active = strings.TrimSpace(reg.Status) == app.ServiceStatusActive
		info.Healthy = reg.Healthy
		info.Status = strings.TrimSpace(reg.Status)
		info.InstanceID = strings.TrimSpace(reg.InstanceID)
		info.Endpoint = strings.TrimSpace(reg.Endpoint)
		info.PID = reg.PID
		info.Manifest = &reg.Manifest
	}
	return info
}

func (m *LifecycleManager) persistConfigLocked() error {
	if strings.TrimSpace(m.configPath) == "" {
		return nil
	}
	cfg := LifecycleConfig{
		Service: LifecycleServiceConfig{
			Global:           m.global,
			LifecycleDefault: m.defaults,
			Services:         make([]LifecycleServiceEntry, 0, len(m.services)),
		},
	}
	for _, svc := range m.services {
		cfg.Service.Services = append(cfg.Service.Services, svc.entry)
	}
	return writeJSONFileAtomic(m.configPath, cfg)
}

func writeJSONFileAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(strings.TrimSpace(path)), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(strings.TrimSpace(path)), "cfg-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, strings.TrimSpace(path))
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
	
	// Verify it is still within dirAbs
	rel, err := filepath.Rel(svc.dirAbs, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("resolved path escapes service root: %s", target)
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		return target, fmt.Errorf("file not found: %s (service_id=%s)", relPath, serviceID)
	}
	return target, nil
}

func NextSuggestedPort(existing []ManagedServiceInfo) int {
	used := map[int]struct{}{}
	for _, item := range existing {
		mf := strings.TrimSpace(item.RuntimeManifestPath)
		if !isFile(mf) {
			continue
		}
		raw, err := os.ReadFile(mf)
		if err != nil {
			continue
		}
		var manifest RuntimeManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			continue
		}
		for i := 0; i < len(manifest.Entry.Args)-1; i++ {
			if strings.TrimSpace(manifest.Entry.Args[i]) != "-addr" {
				continue
			}
			addr := strings.TrimSpace(manifest.Entry.Args[i+1])
			if idx := strings.LastIndex(addr, ":"); idx >= 0 && idx < len(addr)-1 {
				var port int
				_, _ = fmt.Sscanf(addr[idx+1:], "%d", &port)
				if port > 0 {
					used[port] = struct{}{}
				}
			}
		}
	}
	for port := 18110; port < 18999; port++ {
		if _, ok := used[port]; !ok {
			return port
		}
	}
	if runtime.GOOS == "windows" {
		return 19110
	}
	return 18110 + int(time.Now().Unix()%700)
}
