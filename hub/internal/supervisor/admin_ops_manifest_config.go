package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func (m *LifecycleManager) ReadRuntimeManifest(serviceID string) (StartupManifest, error) {
	return m.ReadStartupManifest(serviceID)
}

func (m *LifecycleManager) ReadStartupManifest(serviceID string) (StartupManifest, error) {
	if m == nil {
		return StartupManifest{}, fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return StartupManifest{}, fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	return loadStartupManifest(svc.startupManifest, strings.TrimSpace(serviceID))
}

func (m *LifecycleManager) WriteRuntimeManifest(serviceID string, manifest StartupManifest) error {
	return m.WriteStartupManifest(serviceID, manifest)
}

func (m *LifecycleManager) WriteStartupManifest(serviceID string, manifest StartupManifest) error {
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
		return fmt.Errorf("startup manifest service_id mismatch")
	}
	if len(manifest.Entry.Args) == 0 {
		return fmt.Errorf("startup manifest entry.args is required")
	}
	return writeJSONFileAtomic(svc.startupManifest, manifest)
}

func (m *LifecycleManager) ReadServiceRuntimeManifest(serviceID string) (toolproto.ServiceRuntimeManifest, string, error) {
	if m == nil {
		return toolproto.ServiceRuntimeManifest{}, "", fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return toolproto.ServiceRuntimeManifest{}, "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	raw, err := os.ReadFile(svc.runtimeManifest)
	if err != nil {
		return toolproto.ServiceRuntimeManifest{}, svc.runtimeManifest, err
	}
	var out toolproto.ServiceRuntimeManifest
	if err := json.Unmarshal(raw, &out); err != nil {
		return toolproto.ServiceRuntimeManifest{}, svc.runtimeManifest, fmt.Errorf("decode runtime manifest failed: %w", err)
	}
	return out, svc.runtimeManifest, nil
}

func (m *LifecycleManager) ReadConfigXJSON(serviceID string) (map[string]any, string, error) {
	if m == nil {
		return nil, "", fmt.Errorf("lifecycle manager is nil")
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	configPath := filepath.Join(svc.dirAbs, "config", "configx.json")
	if !isFile(configPath) {
		configPath = filepath.Join(svc.dirAbs, "run", "config", "configx.json")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, configPath, err
	}
	out, err := hubsvc.DecodeJSONMapAllowEmpty(raw)
	if err != nil {
		return nil, configPath, err
	}
	return out, configPath, nil
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
	out, err := hubsvc.DecodeJSONMapAllowEmpty(raw)
	if err != nil {
		return nil, configPath, err
	}
	return out, configPath, nil
}

func (m *LifecycleManager) WriteConfigJSON(serviceID string, value map[string]any, configFileName ...string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("lifecycle manager is nil")
	}
	fileName := "config.json"
	if len(configFileName) > 0 && configFileName[0] != "" {
		fileName = configFileName[0]
	}
	m.mu.Lock()
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("managed service not found: %s", strings.TrimSpace(serviceID))
	}
	configPath := filepath.Join(svc.dirAbs, "config", fileName)
	if value == nil {
		value = map[string]any{}
	}
	runConfigPath := filepath.Join(svc.dirAbs, "run", "config", fileName)
	if err := writeJSONFileAtomic(configPath, value); err != nil {
		return configPath, err
	}
	if err := writeJSONFileAtomic(runConfigPath, value); err != nil {
		return configPath, err
	}
	return configPath, nil
}

func (m *LifecycleManager) UpsertManagedService(entry LifecycleServiceEntry) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	clean := LifecycleServiceEntry{
		ServiceID:   strings.TrimSpace(entry.ServiceID),
		Dir:         strings.TrimSpace(entry.Dir),
		Enabled:     entry.Enabled,
		Reliability: normalizeLifecycleReliability(entry.Reliability),
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
	if m.hubPlatform != nil {
		m.hubPlatform.SetServiceReliability(clean.ServiceID, clean.Reliability)
	}
	return m.persistConfigLocked()
}
