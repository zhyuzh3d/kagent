package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	app "kagent/hub/internal/app"
)

func (m *LifecycleManager) managedServiceLocked(serviceID string) (managedService, bool) {
	sid := strings.TrimSpace(serviceID)
	for _, svc := range m.services {
		if strings.TrimSpace(svc.entry.ServiceID) == sid {
			return svc, true
		}
	}
	return managedService{}, false
}

func (m *LifecycleManager) describeManagedService(svc managedService) ManagedServiceInfo {
	info := ManagedServiceInfo{
		ServiceID:           strings.TrimSpace(svc.entry.ServiceID),
		Dir:                 strings.TrimSpace(svc.entry.Dir),
		Enabled:             svc.enabled,
		Reliability:         svc.reliability,
		DirAbs:              strings.TrimSpace(svc.dirAbs),
		ExecPath:            strings.TrimSpace(svc.execPath),
		StartupManifestPath: strings.TrimSpace(svc.startupManifest),
		RuntimeManifestPath: strings.TrimSpace(svc.runtimeManifest),
		ConfigPath:          filepath.Join(svc.dirAbs, "config", "config.json"),
		HasStartupManifest:  isFile(svc.startupManifest),
		HasRuntimeManifest:  isFile(svc.runtimeManifest),
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
		info.RegisteredManifest = &reg.Manifest
	}
	startupManifest, _ := loadStartupManifest(svc.startupManifest, info.ServiceID)
	info.Description = startupManifest.Description
	if info.Description == "" {
		info.Description = "Managed service (intent)"
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

func (m *LifecycleManager) syncGovernanceToHubPlatform() {
	if m == nil || m.hubPlatform == nil {
		return
	}
	for _, svc := range m.services {
		m.hubPlatform.SetServiceReliability(strings.TrimSpace(svc.entry.ServiceID), svc.reliability)
	}
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
		var manifest StartupManifest
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
