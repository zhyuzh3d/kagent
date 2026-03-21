package supervisor

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	app "kagent/hub/internal/app"
)

func NewLifecycleManager(appRoot string, configPath string, registerURL string, cfg LifecycleConfig, hubPlatform *app.HubPlatform, registry *Registry) (*LifecycleManager, error) {
	root := strings.TrimSpace(appRoot)
	if root == "" {
		return nil, fmt.Errorf("app root is empty")
	}
	if hubPlatform == nil {
		return nil, fmt.Errorf("hub platform is nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	global := normalizeGlobal(cfg.Service.Global)
	defaults := normalizeDefaults(cfg.Service.LifecycleDefault)
	services := make([]managedService, 0, len(cfg.Service.Services))
	for _, item := range cfg.Service.Services {
		item = normalizeLifecycleServiceEntry(item)
		sid := strings.TrimSpace(item.ServiceID)
		dir := strings.TrimSpace(item.Dir)
		if sid == "" || dir == "" {
			continue
		}
		managed, err := buildManagedService(root, item)
		if err != nil {
			return nil, err
		}
		services = append(services, managed)
	}
	manager := &LifecycleManager{
		appRoot:     root,
		configPath:  strings.TrimSpace(configPath),
		registerURL: strings.TrimSpace(registerURL),
		global:      global,
		defaults:    defaults,
		hubPlatform: hubPlatform,
		registry:    registry,
		services:    services,
		procs:       map[string]*managedProcess{},
	}
	manager.syncGovernanceToHubPlatform()
	return manager, nil
}

func (m *LifecycleManager) StartAll(ctx context.Context) StartupSnapshot {
	out := StartupSnapshot{
		StartedAtMS: time.Now().UnixMilli(),
		Services:    make([]StartupServiceOutcome, len(m.services)),
	}
	indexByService := map[string]int{}
	startable := map[string]*managedService{}
	for i := range m.services {
		svc := &m.services[i]
		sid := strings.TrimSpace(svc.entry.ServiceID)
		indexByService[sid] = i
		out.Services[i] = baseStartupOutcome(*svc)
		if !svc.enabled {
			out.Services[i].Status = "disabled"
			continue
		}
		if _, err := os.Stat(svc.execPath); err != nil {
			out.Services[i].Status = InstanceStatusFailed
			out.Services[i].ErrorText = "missing service executable: " + err.Error()
			continue
		}
		if err := m.prepareManagedService(svc); err != nil {
			out.Services[i].Status = InstanceStatusFailed
			out.Services[i].ErrorText = err.Error()
			continue
		}
		startable[sid] = svc
	}

	plan := buildStartupPlan(startable)
	for sid := range plan.cyclic {
		if idx, ok := indexByService[sid]; ok {
			out.Services[idx].Status = InstanceStatusSkipped
			out.Services[idx].ErrorText = "cyclic dependency detected"
		}
	}

	for _, layer := range plan.layers {
		layerServices := make([]*managedService, 0, len(layer))
		for _, sid := range layer {
			svc := startable[sid]
			if svc == nil {
				continue
			}
			idx := indexByService[sid]
			current := &out.Services[idx]
			if current.Status == InstanceStatusSkipped || current.Status == InstanceStatusFailed {
				continue
			}
			if missing := plan.missingDeps[sid]; len(missing) > 0 {
				current.Status = InstanceStatusSkipped
				current.ErrorText = "missing dependencies: " + strings.Join(missing, ", ")
				continue
			}
			if dep := firstFailedDependency(out.Services, indexByService, svc.dependsOn); dep != "" {
				current.Status = InstanceStatusSkipped
				current.ErrorText = "dependency not ready or failed: " + dep
				continue
			}
			layerServices = append(layerServices, svc)
		}
		if len(layerServices) == 0 {
			continue
		}
		layerOut := make([]StartupServiceOutcome, len(layerServices))
		var wg sync.WaitGroup
		for i := range layerServices {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				layerOut[i] = m.startService(ctx, layerServices[i])
			}()
		}
		wg.Wait()
		for _, svcOut := range layerOut {
			if idx, ok := indexByService[strings.TrimSpace(svcOut.ServiceID)]; ok {
				out.Services[idx] = svcOut
			}
		}
	}

	out.CompletedAtMS = time.Now().UnixMilli()
	return out
}

func (m *LifecycleManager) StopAll(timeout time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.stopping = true
	if timeout <= 0 {
		timeout = time.Duration(m.global.GracePeriodMS) * time.Millisecond
	}
	serviceIDs := make([]string, 0, len(m.procs))
	for sid := range m.procs {
		serviceIDs = append(serviceIDs, sid)
	}
	m.mu.Unlock()

	slices.Sort(serviceIDs)
	for i := len(serviceIDs) - 1; i >= 0; i-- {
		m.stopOne(serviceIDs[i], timeout)
	}
}

func (m *LifecycleManager) startService(ctx context.Context, svc *managedService) StartupServiceOutcome {
	out := baseStartupOutcome(*svc)
	if _, err := os.Stat(svc.execPath); err != nil {
		out.Status = InstanceStatusFailed
		out.ErrorText = "missing service executable: " + err.Error()
		return out
	}
	if err := m.prepareManagedService(svc); err != nil {
		out.Status = InstanceStatusFailed
		out.ErrorText = err.Error()
		return out
	}

	attemptLimit := 1 + svc.restartMax
	if svc.policy == "never" {
		attemptLimit = 1
	}
	for attempt := 1; attempt <= attemptLimit; attempt++ {
		out.Attempts = attempt
		proc, reg, readyErr := m.startOnce(ctx, svc, svc.startupConfig)
		if readyErr == nil {
			out.Registered = true
			out.Ready = true
			out.Status = InstanceStatusReady
			out.PID = proc.cmd.Process.Pid
			out.Instance = strings.TrimSpace(reg.InstanceID)
			out.Endpoint = strings.TrimSpace(reg.Endpoint)
			if m.registry != nil {
				m.registry.MarkReady(out.ServiceID, out.Instance)
			}
			m.trackProcess(proc)
			return out
		}
		out.Status = InstanceStatusFailed
		out.ErrorText = readyErr.Error()
		if attempt >= attemptLimit {
			break
		}
		time.Sleep(svc.restartWait)
	}
	return out
}

func (m *LifecycleManager) prepareManagedService(svc *managedService) error {
	if svc == nil {
		return fmt.Errorf("managed service is nil")
	}
	startupManifest, err := loadStartupManifest(svc.startupManifest, strings.TrimSpace(svc.entry.ServiceID))
	if err != nil {
		return err
	}
	svc.startupConfig = startupManifest
	svc.policy = normalizeRestartPolicy(firstNonEmptyValue(strings.TrimSpace(startupManifest.Lifecycle.RestartPolicy), m.defaults.RestartPolicy))
	svc.timeout = clampDurationMS(firstPositive(startupManifest.Lifecycle.RegisterTimeoutMS, m.defaults.RegisterTimeoutMS), m.global.MaxTimeoutMS)
	svc.restartWait = clampDurationMS(firstPositive(startupManifest.Lifecycle.RestartBackoffMS, m.defaults.RestartBackoffMS), m.global.MaxTimeoutMS)
	maxRestart := firstPositive(startupManifest.Lifecycle.RestartTimes, m.global.MaxRestartTimes)
	if maxRestart > m.global.MaxRestartTimes {
		maxRestart = m.global.MaxRestartTimes
	}
	if maxRestart < 0 {
		maxRestart = 0
	}
	svc.restartMax = maxRestart
	svc.dependsOn = normalizeDependsOn(strings.TrimSpace(startupManifest.ServiceID), startupManifest.DependsOn)
	return nil
}

func (m *LifecycleManager) serviceByID(serviceID string) (managedService, bool) {
	sid := strings.TrimSpace(serviceID)
	for _, svc := range m.services {
		if strings.TrimSpace(svc.entry.ServiceID) == sid {
			return svc, true
		}
	}
	return managedService{}, false
}
