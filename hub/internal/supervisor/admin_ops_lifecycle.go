package supervisor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	app "kagent/hub/internal/app"
)

type ManagedServiceInfo struct {
	ServiceID           string                 `json:"service_id"`
	Description         string                 `json:"description,omitempty"`
	Dir                 string                 `json:"dir"`
	Enabled             bool                   `json:"enabled"`
	Reliability         string                 `json:"reliability,omitempty"`
	DirAbs              string                 `json:"dir_abs"`
	ExecPath            string                 `json:"exec_path"`
	StartupManifestPath string                 `json:"startup_manifest_path"`
	RuntimeManifestPath string                 `json:"runtime_manifest_path"`
	ConfigPath          string                 `json:"config_path"`
	HasSourceConfig     bool                   `json:"has_source_config"`
	HasStartupManifest  bool                   `json:"has_startup_manifest"`
	HasRuntimeManifest  bool                   `json:"has_runtime_manifest"`
	HasExec             bool                   `json:"has_exec"`
	HasGoMod            bool                   `json:"has_go_mod"`
	Registered          bool                   `json:"registered"`
	Active              bool                   `json:"active"`
	Healthy             bool                   `json:"healthy"`
	Status              string                 `json:"status,omitempty"`
	InstanceID          string                 `json:"instance_id,omitempty"`
	Endpoint            string                 `json:"endpoint,omitempty"`
	PID                 int                    `json:"pid,omitempty"`
	RegisteredManifest  *app.ServiceManifest   `json:"registered_manifest,omitempty"`
	Startup             *ManagedServiceStartup `json:"startup,omitempty"`
}

type ManagedServiceStartup struct {
	StartedAtMS   int64  `json:"started_at_ms,omitempty"`
	CompletedAtMS int64  `json:"completed_at_ms,omitempty"`
	Ready         bool   `json:"ready"`
	Registered    bool   `json:"registered,omitempty"`
	Status        string `json:"status,omitempty"`
	Attempts      int    `json:"attempts,omitempty"`
	PID           int    `json:"pid,omitempty"`
	InstanceID    string `json:"instance_id,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	ErrorText     string `json:"error,omitempty"`
}

func (m *LifecycleManager) ListManagedServices() []ManagedServiceInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	services := append([]managedService(nil), m.services...)
	m.mu.Unlock()
	out := make([]ManagedServiceInfo, 0, len(services))
	for _, svc := range services {
		out = append(out, m.describeManagedService(svc))
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
	svc, ok := m.managedServiceLocked(serviceID)
	m.mu.Unlock()
	if !ok {
		return ManagedServiceInfo{}, false
	}
	return m.describeManagedService(svc), true
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

func (m *LifecycleManager) SetServiceEnabled(serviceID string, enabled bool) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	sid := strings.TrimSpace(serviceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.services {
		if strings.TrimSpace(m.services[i].entry.ServiceID) != sid {
			continue
		}
		m.services[i].enabled = enabled
		m.services[i].entry.Enabled = enabled
		return m.persistConfigLocked()
	}
	return fmt.Errorf("managed service not found: %s", sid)
}

func (m *LifecycleManager) UpdateServiceGovernance(serviceID string, enabled bool, reliability string) error {
	if m == nil {
		return fmt.Errorf("lifecycle manager is nil")
	}
	sid := strings.TrimSpace(serviceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.services {
		if strings.TrimSpace(m.services[i].entry.ServiceID) != sid {
			continue
		}
		nextReliability := normalizeLifecycleReliability(reliability)
		m.services[i].enabled = enabled
		m.services[i].reliability = nextReliability
		m.services[i].entry.Enabled = enabled
		m.services[i].entry.Reliability = nextReliability
		if m.hubPlatform != nil {
			m.hubPlatform.SetServiceReliability(sid, nextReliability)
		}
		return m.persistConfigLocked()
	}
	return fmt.Errorf("managed service not found: %s", sid)
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
