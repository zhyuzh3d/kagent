package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	app "kagent/hub/internal/app"
	"kagent/pkg/hubsvc"
)

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
