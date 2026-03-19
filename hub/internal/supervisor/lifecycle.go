package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/transport"
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
	ServiceID string `json:"service_id"`
	Dir       string `json:"dir"`
}

type RuntimeManifest struct {
	ServiceID string                `json:"service_id"`
	Version   string                `json:"version,omitempty"`
	Entry     RuntimeManifestEntry  `json:"entry"`
	Lifecycle RuntimeManifestPolicy `json:"lifecycle"`
}

type RuntimeManifestEntry struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env,omitempty"`
}

type RuntimeManifestPolicy struct {
	RegisterTimeoutMS int    `json:"register_timeout_ms,omitempty"`
	RestartPolicy     string `json:"restart_policy,omitempty"`
	RestartBackoffMS  int    `json:"restart_backoff_ms,omitempty"`
	RestartTimes      int    `json:"restart_times,omitempty"`
	KillOld           bool   `json:"kill_old,omitempty"`
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

	Ready     bool   `json:"ready"`
	Attempts  int    `json:"attempts"`
	PID       int    `json:"pid,omitempty"`
	Instance  string `json:"instance_id,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	ErrorText string `json:"error,omitempty"`
}

type managedService struct {
	entry       LifecycleServiceEntry
	dirAbs      string
	execPath    string
	manifest    string
	secretPath  string
	restartMax  int
	restartWait time.Duration
	policy      string
	timeout     time.Duration
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
	registerURL string
	global      LifecycleGlobalConfig
	defaults    LifecycleDefaultConfig

	hubPlatform *app.HubPlatform
	pidStore    *app.ServicePidStore
	registry    *Registry
	transport   interface {
		Call(ctx context.Context, endpoint transport.Endpoint, method string, path string, headers http.Header, body []byte, timeout time.Duration) (transport.Response, error)
	}

	services []managedService
	procs    map[string]*managedProcess
	stopping bool
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

func NewLifecycleManager(appRoot string, registerURL string, cfg LifecycleConfig, hubPlatform *app.HubPlatform, registry *Registry, tp interface {
	Call(ctx context.Context, endpoint transport.Endpoint, method string, path string, headers http.Header, body []byte, timeout time.Duration) (transport.Response, error)
}) (*LifecycleManager, error) {
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
		sid := strings.TrimSpace(item.ServiceID)
		dir := strings.TrimSpace(item.Dir)
		if sid == "" || dir == "" {
			continue
		}
		dirAbs := filepath.Join(root, dir)
		execPath := filepath.Join(dirAbs, "run", sid+"-latest")
		if runtime.GOOS == "windows" {
			execPath += ".exe"
		}
		services = append(services, managedService{
			entry:      item,
			dirAbs:     dirAbs,
			execPath:   execPath,
			manifest:   filepath.Join(dirAbs, "run", "manifest.json"),
			secretPath: filepath.Join(dirAbs, "run", ".service_secret"),
		})
	}
	return &LifecycleManager{
		appRoot:     root,
		registerURL: strings.TrimSpace(registerURL),
		global:      global,
		defaults:    defaults,
		hubPlatform: hubPlatform,
		pidStore:    app.NewServicePidStore(filepath.Join(root, "hub", "run", ".service_pid")),
		registry:    registry,
		transport:   tp,
		services:    services,
		procs:       map[string]*managedProcess{},
	}, nil
}

func (m *LifecycleManager) StartAll(ctx context.Context) StartupSnapshot {
	out := StartupSnapshot{
		StartedAtMS: time.Now().UnixMilli(),
		Services:    make([]StartupServiceOutcome, 0, len(m.services)),
	}
	for i := range m.services {
		out.Services = append(out.Services, m.startService(ctx, &m.services[i]))
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

	for i := len(serviceIDs) - 1; i >= 0; i-- {
		m.stopOne(serviceIDs[i], timeout)
	}
}

func (m *LifecycleManager) startService(ctx context.Context, svc *managedService) StartupServiceOutcome {
	serviceID := strings.TrimSpace(svc.entry.ServiceID)
	out := StartupServiceOutcome{
		ServiceID:  serviceID,
		Dir:        strings.TrimSpace(svc.entry.Dir),
		ExecPath:   svc.execPath,
		Manifest:   svc.manifest,
		SecretPath: svc.secretPath,
	}
	if _, err := os.Stat(svc.execPath); err != nil {
		out.ErrorText = "missing service executable: " + err.Error()
		return out
	}
	runtimeManifest, err := loadRuntimeManifest(svc.manifest, serviceID)
	if err != nil {
		out.ErrorText = err.Error()
		return out
	}
	if runtimeManifest.Lifecycle.KillOld {
		if err := m.cleanupRecordedServiceProcess(svc); err != nil {
			out.ErrorText = err.Error()
			return out
		}
	}
	svc.policy = normalizeRestartPolicy(firstNonEmptyValue(strings.TrimSpace(runtimeManifest.Lifecycle.RestartPolicy), m.defaults.RestartPolicy))
	svc.timeout = clampDurationMS(firstPositive(runtimeManifest.Lifecycle.RegisterTimeoutMS, m.defaults.RegisterTimeoutMS), m.global.MaxTimeoutMS)
	svc.restartWait = clampDurationMS(firstPositive(runtimeManifest.Lifecycle.RestartBackoffMS, m.defaults.RestartBackoffMS), m.global.MaxTimeoutMS)
	maxRestart := firstPositive(runtimeManifest.Lifecycle.RestartTimes, m.global.MaxRestartTimes)
	if maxRestart > m.global.MaxRestartTimes {
		maxRestart = m.global.MaxRestartTimes
	}
	if maxRestart < 0 {
		maxRestart = 0
	}
	svc.restartMax = maxRestart

	attemptLimit := 1 + svc.restartMax
	if svc.policy == "never" {
		attemptLimit = 1
	}
	for attempt := 1; attempt <= attemptLimit; attempt++ {
		out.Attempts = attempt
		proc, reg, readyErr := m.startOnce(ctx, svc, runtimeManifest)
		if readyErr == nil {
			out.Ready = true
			out.PID = proc.cmd.Process.Pid
			out.Instance = strings.TrimSpace(reg.InstanceID)
			out.Endpoint = strings.TrimSpace(reg.Endpoint)
			m.recordServiceStart(serviceID, proc.cmd.Process.Pid, svc.execPath, proc.startedAtMS)
			m.trackProcess(proc)
			return out
		}
		out.ErrorText = readyErr.Error()
		if attempt >= attemptLimit {
			break
		}
		time.Sleep(svc.restartWait)
	}
	return out
}

func (m *LifecycleManager) startOnce(ctx context.Context, svc *managedService, runtimeManifest RuntimeManifest) (*managedProcess, app.HubServiceRegistration, error) {
	serviceID := strings.TrimSpace(svc.entry.ServiceID)
	args := append([]string(nil), runtimeManifest.Entry.Args...)
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
	cmd.Env = append(os.Environ(), flattenEnv(runtimeManifest.Entry.Env)...)
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

	startedAt := time.Now()
	deadline := startedAt.Add(svc.timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			_ = m.stopProcess(proc, time.Duration(m.global.GracePeriodMS)*time.Millisecond)
			return nil, app.HubServiceRegistration{}, fmt.Errorf("startup canceled: %w", ctx.Err())
		case err := <-proc.done:
			if err == nil {
				err = fmt.Errorf("service exited unexpectedly")
			}
			m.hubPlatform.MarkServiceDown(serviceID, "process exited before ready")
			return nil, app.HubServiceRegistration{}, fmt.Errorf("service exited before ready: %w", err)
		default:
		}
		reg, ok := m.hubPlatform.GetService(serviceID)
		if ok && reg.Healthy && strings.TrimSpace(reg.Status) == app.ServiceStatusActive {
			instances := m.registry.GetByService(serviceID)
			if len(instances) == 0 {
				return proc, reg, nil
			}
			for _, ins := range instances {
				if strings.TrimSpace(ins.InstanceID) != strings.TrimSpace(reg.InstanceID) {
					continue
				}
				if ins.Healthy && strings.TrimSpace(ins.Status) == InstanceStatusReady {
					return proc, reg, nil
				}
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	_ = m.stopProcess(proc, time.Duration(m.global.GracePeriodMS)*time.Millisecond)
	return nil, app.HubServiceRegistration{}, fmt.Errorf("register timeout after %v", svc.timeout)
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
		runtimeManifest, mfErr := loadRuntimeManifest(svc.manifest, strings.TrimSpace(svc.entry.ServiceID))
		if mfErr != nil {
			app.Warnf("service restart manifest load failed: service=%s attempt=%d err=%v", svc.entry.ServiceID, attempt, mfErr)
			continue
		}
		newProc, _, restartErr := m.startOnce(context.Background(), &svc, runtimeManifest)
		if restartErr != nil {
			app.Warnf("service restart failed: service=%s attempt=%d err=%v", svc.entry.ServiceID, attempt, restartErr)
			continue
		}
		app.Warnf("service restarted: service=%s attempt=%d pid=%d", svc.entry.ServiceID, attempt, newProc.cmd.Process.Pid)
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
		timeout = 1500 * time.Millisecond
	}
	if reg, ok := m.hubPlatform.GetService(proc.serviceID); ok {
		_ = StopServiceRegistration(m.hubPlatform, reg, timeout)
	}
	select {
	case <-proc.done:
		return nil
	case <-time.After(timeout):
	}
	_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-proc.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	_ = proc.cmd.Process.Signal(syscall.SIGKILL)
	select {
	case <-proc.done:
	case <-time.After(800 * time.Millisecond):
	}
	return nil
}

func (m *LifecycleManager) cleanupRecordedServiceProcess(svc *managedService) error {
	if m == nil || m.pidStore == nil {
		return nil
	}
	serviceID := strings.TrimSpace(svc.entry.ServiceID)
	if serviceID == "" {
		return nil
	}
	record, ok := m.pidStore.Get(serviceID)
	if !ok {
		return nil
	}
	expectedExecPath := strings.TrimSpace(record.ExecPath)
	if expectedExecPath == "" {
		expectedExecPath = strings.TrimSpace(svc.execPath)
	}
	if expectedExecPath == "" {
		return nil
	}
	cleaned, err := app.CleanRecordedProcess(record.PID, expectedExecPath, record.StartedAtMS)
	if err != nil {
		return fmt.Errorf("cleanup recorded service process failed: service=%s pid=%d path=%s err=%w", serviceID, record.PID, expectedExecPath, err)
	}
	if cleaned {
		app.Infof("service old process cleaned: service=%s pid=%d path=%s started_at_ms=%d", serviceID, record.PID, expectedExecPath, record.StartedAtMS)
		return nil
	}
	app.Infof("service old process not cleaned: service=%s pid=%d path=%s started_at_ms=%d", serviceID, record.PID, expectedExecPath, record.StartedAtMS)
	return nil
}

func (m *LifecycleManager) recordServiceStart(serviceID string, pid int, execPath string, startedAtMS int64) {
	if m == nil || m.pidStore == nil {
		return
	}
	if err := m.pidStore.Upsert(app.ServicePidRecord{
		ServiceID:   serviceID,
		PID:         pid,
		ExecPath:    execPath,
		StartedAtMS: startedAtMS,
	}); err != nil {
		app.Warnf("service pid record update failed: service=%s pid=%d path=%s err=%v", serviceID, pid, execPath, err)
	}
}

func loadRuntimeManifest(path string, serviceID string) (RuntimeManifest, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return RuntimeManifest{}, fmt.Errorf("read runtime manifest failed: %w", err)
	}
	var out RuntimeManifest
	if err := json.Unmarshal(raw, &out); err != nil {
		return RuntimeManifest{}, fmt.Errorf("decode runtime manifest failed: %w", err)
	}
	if strings.TrimSpace(out.ServiceID) == "" {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest missing service_id")
	}
	if strings.TrimSpace(out.ServiceID) != strings.TrimSpace(serviceID) {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest service_id mismatch: expect=%s got=%s", serviceID, out.ServiceID)
	}
	if len(out.Entry.Args) == 0 {
		return RuntimeManifest{}, fmt.Errorf("runtime manifest entry.args is required")
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

func hasFlag(args []string, flagName string) bool {
	target := strings.TrimSpace(flagName)
	for i := 0; i < len(args); i++ {
		if strings.TrimSpace(args[i]) == target {
			return true
		}
	}
	return false
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
