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
	"sync"
	"syscall"
	"time"

	app "kagent/hub/internal/app"
	"kagent/pkg/hubsvc"
)

const (
	defaultMaxTimeoutMS      = 5000
	defaultMaxRestartTimes   = 10
	defaultGracePeriodMS     = 1000
	defaultRegisterTimeoutMS = 1000
	defaultRestartBackoffMS  = 300
	defaultRestartPolicy     = "never"
)

type LifecycleConfig struct {
	Service LifecycleServiceConfig `json:"service"`
}

type LifecycleServiceConfig struct {
	Global           LifecycleGlobalConfig   `json:"global"`
	LifecycleDefault LifecycleDefaultConfig  `json:"lifecycle_default"`
	Services         []LifecycleServiceEntry `json:"services"`
}

type LifecycleGlobalConfig struct {
	MaxTimeoutMS    int `json:"max_timeout_ms"`
	MaxRestartTimes int `json:"max_restart_times"`
	GracePeriodMS   int `json:"grace_period_ms"`
}

type LifecycleDefaultConfig struct {
	RegisterTimeoutMS int    `json:"register_timeout_ms"`
	RestartPolicy     string `json:"restart_policy"`
	RestartBackoffMS  int    `json:"restart_backoff_ms"`
}

type LifecycleServiceEntry struct {
	ServiceID   string `json:"service_id"`
	Dir         string `json:"dir"`
	Enabled     bool   `json:"enabled"`
	Reliability string `json:"reliability,omitempty"`
}

type StartupManifest struct {
	ServiceID   string                `json:"service_id"`
	Description string                `json:"description,omitempty"`
	Version     string                `json:"version,omitempty"`
	DependsOn   []string              `json:"depends_on,omitempty"`
	Entry       StartupManifestEntry  `json:"entry"`
	Lifecycle   StartupManifestPolicy `json:"lifecycle"`
}

type StartupManifestEntry struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env,omitempty"`
}

type StartupManifestPolicy struct {
	RegisterTimeoutMS int    `json:"register_timeout_ms,omitempty"`
	RestartPolicy     string `json:"restart_policy,omitempty"`
	RestartBackoffMS  int    `json:"restart_backoff_ms,omitempty"`
	RestartTimes      int    `json:"restart_times,omitempty"`
}

type StartupSnapshot struct {
	StartedAtMS   int64                   `json:"started_at_ms"`
	CompletedAtMS int64                   `json:"completed_at_ms"`
	Services      []StartupServiceOutcome `json:"services"`
}

type StartupServiceOutcome struct {
	ServiceID  string `json:"service_id"`
	Dir        string `json:"dir"`
	ExecPath   string `json:"exec_path"`
	Manifest   string `json:"manifest_path"`
	SecretPath string `json:"secret_path"`

	Ready       bool   `json:"ready"`
	Registered  bool   `json:"registered,omitempty"`
	Initialized bool   `json:"initialized,omitempty"`
	Status      string `json:"status,omitempty"`
	Attempts    int    `json:"attempts"`
	PID         int    `json:"pid,omitempty"`
	Instance    string `json:"instance_id,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	ErrorText   string `json:"error,omitempty"`
}

type managedService struct {
	entry           LifecycleServiceEntry
	dirAbs          string
	execPath        string
	startupManifest string
	runtimeManifest string
	secretPath      string
	restartMax      int
	restartWait     time.Duration
	policy          string
	timeout         time.Duration
	enabled         bool
	reliability     string
	dependsOn       []string
	startupConfig   StartupManifest
}

type managedProcess struct {
	serviceID   string
	cmd         *exec.Cmd
	done        chan error
	startedAtMS int64
}

type LifecycleManager struct {
	mu sync.Mutex

	appRoot     string
	configPath  string
	registerURL string
	global      LifecycleGlobalConfig
	defaults    LifecycleDefaultConfig

	hubPlatform *app.HubPlatform
	registry    *Registry

	services []managedService
	procs    map[string]*managedProcess
	stopping bool
}

type startupPlan struct {
	layers      [][]string
	missingDeps map[string][]string
	cyclic      map[string]struct{}
}

func LoadLifecycleConfig(path string) (LifecycleConfig, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return LifecycleConfig{}, err
	}
	var cfg LifecycleConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return LifecycleConfig{}, fmt.Errorf("decode lifecycle config: %w", err)
	}
	return cfg, nil
}

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

func buildStartupPlan(services map[string]*managedService) startupPlan {
	adjacency := map[string][]string{}
	indegree := map[string]int{}
	missingDeps := map[string][]string{}
	for sid := range services {
		indegree[sid] = 0
	}
	for sid, svc := range services {
		for _, dep := range svc.dependsOn {
			if _, ok := services[dep]; !ok {
				missingDeps[sid] = append(missingDeps[sid], dep)
				continue
			}
			adjacency[dep] = append(adjacency[dep], sid)
			indegree[sid]++
		}
	}
	ready := make([]string, 0, len(indegree))
	for sid, degree := range indegree {
		if degree == 0 {
			ready = append(ready, sid)
		}
	}
	slices.Sort(ready)

	layers := make([][]string, 0, len(ready))
	processed := map[string]struct{}{}
	for len(ready) > 0 {
		layer := append([]string(nil), ready...)
		layers = append(layers, layer)
		nextSet := map[string]struct{}{}
		for _, sid := range layer {
			processed[sid] = struct{}{}
			for _, dependent := range adjacency[sid] {
				indegree[dependent]--
				if indegree[dependent] == 0 {
					nextSet[dependent] = struct{}{}
				}
			}
		}
		next := make([]string, 0, len(nextSet))
		for sid := range nextSet {
			next = append(next, sid)
		}
		slices.Sort(next)
		ready = next
	}

	cyclic := map[string]struct{}{}
	for sid := range services {
		if _, ok := processed[sid]; ok {
			continue
		}
		cyclic[sid] = struct{}{}
	}
	for sid, deps := range missingDeps {
		slices.Sort(deps)
		missingDeps[sid] = deps
	}
	return startupPlan{
		layers:      layers,
		missingDeps: missingDeps,
		cyclic:      cyclic,
	}
}

func firstFailedDependency(outcomes []StartupServiceOutcome, indexByService map[string]int, deps []string) string {
	for _, dep := range deps {
		idx, ok := indexByService[dep]
		if !ok {
			return dep
		}
		if !outcomes[idx].Ready {
			return dep
		}
	}
	return ""
}

func normalizeDependsOn(serviceID string, values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	self := strings.TrimSpace(serviceID)
	for _, value := range values {
		clean := strings.TrimSpace(value)
		if clean == "" || clean == self {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m *LifecycleManager) startOnce(ctx context.Context, svc *managedService, startupManifest StartupManifest) (*managedProcess, app.HubServiceRegistration, error) {
	serviceID := strings.TrimSpace(svc.entry.ServiceID)
	args := append([]string(nil), startupManifest.Entry.Args...)
	args = ensureFlagValue(args, "-hub-register-url", m.registerURL)
	instanceID := flagValue(args, "-instance-id")
	if strings.TrimSpace(instanceID) == "" {
		instanceID = serviceID + "-" + newStamp()
		args = ensureFlagValue(args, "-instance-id", instanceID)
	}
	bootstrap, err := m.hubPlatform.PrepareServiceBootstrap(serviceID, instanceID, m.registerURL, 10*time.Minute)
	if err != nil {
		return nil, app.HubServiceRegistration{}, fmt.Errorf("prepare service bootstrap failed: %w", err)
	}
	if err := hubsvc.WriteBootstrapSecret(svc.secretPath, bootstrap); err != nil {
		return nil, app.HubServiceRegistration{}, fmt.Errorf("write bootstrap secret failed: %w", err)
	}

	cmd := exec.Command(svc.execPath, args...)
	cmd.Dir = m.appRoot
	cmd.Env = append(os.Environ(), flattenEnv(startupManifest.Entry.Env)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, app.HubServiceRegistration{}, fmt.Errorf("start process failed: %w", err)
	}
	startedAtMS := time.Now().UnixMilli()
	proc := &managedProcess{
		serviceID:   serviceID,
		cmd:         cmd,
		done:        make(chan error, 1),
		startedAtMS: startedAtMS,
	}
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()

	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(svc.timeout)
	defer timer.Stop()

	checkRegistered := func() (app.HubServiceRegistration, bool) {
		reg, ok := m.hubPlatform.GetService(serviceID)
		if !ok {
			return app.HubServiceRegistration{}, false
		}
		if !reg.Healthy || strings.TrimSpace(reg.Status) != app.ServiceStatusActive {
			return app.HubServiceRegistration{}, false
		}
		if strings.TrimSpace(reg.InstanceID) != strings.TrimSpace(instanceID) {
			return app.HubServiceRegistration{}, false
		}
		return reg, true
	}

	for {
		if reg, ok := checkRegistered(); ok {
			return proc, reg, nil
		}
		select {
		case <-ctx.Done():
			_ = m.stopProcess(proc, time.Duration(m.global.GracePeriodMS)*time.Millisecond)
			return nil, app.HubServiceRegistration{}, fmt.Errorf("startup canceled: %w", ctx.Err())
		case err := <-proc.done:
			if err == nil {
				err = fmt.Errorf("service exited unexpectedly")
			}
			m.hubPlatform.MarkServiceDown(serviceID, "process exited before register")
			return nil, app.HubServiceRegistration{}, fmt.Errorf("service exited before register: %w", err)
		case <-ticker.C:
		case <-timer.C:
			if reg, ok := checkRegistered(); ok {
				return proc, reg, nil
			}
			_ = m.stopProcess(proc, time.Duration(m.global.GracePeriodMS)*time.Millisecond)
			return nil, app.HubServiceRegistration{}, fmt.Errorf("register timeout after %v", svc.timeout)
		}
	}
}

func (m *LifecycleManager) trackProcess(proc *managedProcess) {
	if m == nil || proc == nil {
		return
	}
	m.mu.Lock()
	m.procs[proc.serviceID] = proc
	m.mu.Unlock()
	go m.watchProcess(proc)
}

func (m *LifecycleManager) watchProcess(proc *managedProcess) {
	if m == nil || proc == nil {
		return
	}
	waitErr, ok := <-proc.done
	if !ok {
		return
	}
	exitedCleanly := waitErr == nil
	m.mu.Lock()
	stopping := m.stopping
	current := m.procs[proc.serviceID]
	if current == proc {
		delete(m.procs, proc.serviceID)
	}
	m.mu.Unlock()
	if stopping || current != proc {
		return
	}
	if waitErr == nil {
		waitErr = fmt.Errorf("process exited")
	}
	m.hubPlatform.MarkServiceDown(proc.serviceID, "process exited: "+waitErr.Error())
	for _, instance := range m.registry.GetByService(proc.serviceID) {
		m.registry.MarkDead(proc.serviceID, instance.InstanceID)
	}
	svc, ok := m.serviceByID(proc.serviceID)
	if !ok || svc.policy == "never" || svc.restartMax <= 0 {
		return
	}
	if svc.policy == "on-failure" && exitedCleanly {
		return
	}
	for attempt := 1; attempt <= svc.restartMax; attempt++ {
		m.mu.Lock()
		stopNow := m.stopping
		m.mu.Unlock()
		if stopNow {
			return
		}
		time.Sleep(svc.restartWait)
		if err := m.prepareManagedService(&svc); err != nil {
			app.Warnf("service restart manifest load failed: service=%s attempt=%d err=%v", svc.entry.ServiceID, attempt, err)
			continue
		}
		newProc, reg, restartErr := m.startOnce(context.Background(), &svc, svc.startupConfig)
		if restartErr != nil {
			app.Warnf("service restart failed: service=%s attempt=%d err=%v", svc.entry.ServiceID, attempt, restartErr)
			continue
		}
		app.Warnf("service restarted: service=%s attempt=%d pid=%d", svc.entry.ServiceID, attempt, newProc.cmd.Process.Pid)
		if m.registry != nil {
			m.registry.MarkReady(svc.entry.ServiceID, reg.InstanceID)
		}
		m.trackProcess(newProc)
		return
	}
}

func (m *LifecycleManager) stopOne(serviceID string, timeout time.Duration) {
	if m == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return
	}
	m.mu.Lock()
	proc := m.procs[sid]
	if proc != nil {
		delete(m.procs, sid)
	}
	m.mu.Unlock()
	if proc == nil {
		return
	}
	_ = m.stopProcess(proc, timeout)
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

func (m *LifecycleManager) stopProcess(proc *managedProcess, timeout time.Duration) error {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = serviceSelfShutdownGrace
	}
	if reg, ok := m.hubPlatform.GetService(proc.serviceID); ok {
		_ = StopServiceRegistration(m.hubPlatform, reg, timeout)
		if m.registry != nil {
			m.registry.Unregister(reg.ServiceID, reg.InstanceID)
		}
	}
	select {
	case <-proc.done:
		return nil
	case <-time.After(timeout):
	}
	_ = proc.cmd.Process.Signal(syscall.SIGKILL)
	select {
	case <-proc.done:
	case <-time.After(200 * time.Millisecond):
	}
	return nil
}

func loadStartupManifest(path string, serviceID string) (StartupManifest, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return StartupManifest{}, fmt.Errorf("read startup manifest failed: %w", err)
	}
	var out StartupManifest
	if err := json.Unmarshal(raw, &out); err != nil {
		return StartupManifest{}, fmt.Errorf("decode startup manifest failed: %w", err)
	}
	if strings.TrimSpace(out.ServiceID) == "" {
		return StartupManifest{}, fmt.Errorf("startup manifest missing service_id")
	}
	if strings.TrimSpace(out.ServiceID) != strings.TrimSpace(serviceID) {
		return StartupManifest{}, fmt.Errorf("startup manifest service_id mismatch: expect=%s got=%s", serviceID, out.ServiceID)
	}
	if len(out.Entry.Args) == 0 {
		return StartupManifest{}, fmt.Errorf("startup manifest entry.args is required")
	}
	return out, nil
}

func normalizeGlobal(in LifecycleGlobalConfig) LifecycleGlobalConfig {
	out := in
	if out.MaxTimeoutMS <= 0 {
		out.MaxTimeoutMS = defaultMaxTimeoutMS
	}
	if out.MaxRestartTimes <= 0 {
		out.MaxRestartTimes = defaultMaxRestartTimes
	}
	if out.GracePeriodMS <= 0 {
		out.GracePeriodMS = defaultGracePeriodMS
	}
	return out
}

func normalizeDefaults(in LifecycleDefaultConfig) LifecycleDefaultConfig {
	out := in
	if out.RegisterTimeoutMS <= 0 {
		out.RegisterTimeoutMS = defaultRegisterTimeoutMS
	}
	if out.RestartBackoffMS <= 0 {
		out.RestartBackoffMS = defaultRestartBackoffMS
	}
	out.RestartPolicy = normalizeRestartPolicy(out.RestartPolicy)
	return out
}

func normalizeRestartPolicy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "always":
		return "always"
	case "on-failure":
		return "on-failure"
	default:
		return defaultRestartPolicy
	}
}

func ensureFlagValue(args []string, flagName string, value string) []string {
	flagName = strings.TrimSpace(flagName)
	if flagName == "" {
		return args
	}
	out := make([]string, 0, len(args)+2)
	replaced := false
	for i := 0; i < len(args); i++ {
		cur := strings.TrimSpace(args[i])
		if cur == flagName {
			out = append(out, flagName, strings.TrimSpace(value))
			replaced = true
			if i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	if !replaced {
		out = append(out, flagName, strings.TrimSpace(value))
	}
	return out
}

func flagValue(args []string, flagName string) string {
	target := strings.TrimSpace(flagName)
	if target == "" {
		return ""
	}
	for i := 0; i < len(args); i++ {
		if strings.TrimSpace(args[i]) != target {
			continue
		}
		if i+1 >= len(args) {
			return ""
		}
		return strings.TrimSpace(args[i+1])
	}
	return ""
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out = append(out, key+"="+v)
	}
	return out
}

func clampDurationMS(valueMS int, maxMS int) time.Duration {
	v := valueMS
	if v <= 0 {
		v = defaultMaxTimeoutMS
	}
	if maxMS > 0 && v > maxMS {
		v = maxMS
	}
	return time.Duration(v) * time.Millisecond
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		clean := strings.TrimSpace(value)
		if clean != "" {
			return clean
		}
	}
	return ""
}

func newStamp() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func buildManagedService(appRoot string, entry LifecycleServiceEntry) (managedService, error) {
	entry = normalizeLifecycleServiceEntry(entry)
	sid := strings.TrimSpace(entry.ServiceID)
	dir := strings.TrimSpace(entry.Dir)
	if sid == "" || dir == "" {
		return managedService{}, fmt.Errorf("service_id and dir are required")
	}
	dirAbs := dir
	if !filepath.IsAbs(dirAbs) {
		dirAbs = filepath.Join(strings.TrimSpace(appRoot), dirAbs)
	}
	execPath := filepath.Join(dirAbs, "run", sid+"-latest")
	if runtime.GOOS == "windows" {
		execPath += ".exe"
	}
	return managedService{
		entry:           entry,
		dirAbs:          dirAbs,
		execPath:        execPath,
		startupManifest: filepath.Join(dirAbs, "run", "manifest.json"),
		runtimeManifest: filepath.Join(dirAbs, "run", "manifest_runtime.json"),
		secretPath:      filepath.Join(dirAbs, "run", ".service_secret"),
		enabled:         entry.Enabled,
		reliability:     entry.Reliability,
	}, nil
}

func normalizeLifecycleReliability(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "trusted":
		return "trusted"
	case "verified":
		return "verified"
	case "unverified":
		return "unverified"
	case "risky":
		return "risky"
	case "high_risk":
		return "high_risk"
	default:
		return "unverified"
	}
}

func normalizeLifecycleServiceEntry(entry LifecycleServiceEntry) LifecycleServiceEntry {
	entry.ServiceID = strings.TrimSpace(entry.ServiceID)
	entry.Dir = strings.TrimSpace(entry.Dir)
	entry.Reliability = normalizeLifecycleReliability(entry.Reliability)
	return entry
}

func baseStartupOutcome(svc managedService) StartupServiceOutcome {
	return StartupServiceOutcome{
		ServiceID:  strings.TrimSpace(svc.entry.ServiceID),
		Dir:        strings.TrimSpace(svc.entry.Dir),
		ExecPath:   svc.execPath,
		Manifest:   svc.startupManifest,
		SecretPath: svc.secretPath,
		Status:     InstanceStatusStarting,
	}
}
