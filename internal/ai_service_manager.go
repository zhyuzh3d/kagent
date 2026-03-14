package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type AIServiceStatus struct {
	Mode             string         `json:"mode"`
	BaseURL          string         `json:"base_url"`
	AutoStart        bool           `json:"auto_start"`
	Running          bool           `json:"running"`
	PID              int            `json:"pid"`
	Healthy          bool           `json:"healthy"`
	LastCheckMS      int64          `json:"last_check_ms"`
	LastError        string         `json:"last_error,omitempty"`
	LastTransitionMS int64          `json:"last_transition_ms"`
	Info             *AIServiceInfo `json:"info,omitempty"`
}

type AIServiceManager struct {
	cfg AIServiceConfig

	mu     sync.RWMutex
	status AIServiceStatus
	cmd    *exec.Cmd

	httpClient *http.Client
}

func NewAIServiceManager(cfg AIServiceConfig) *AIServiceManager {
	c := cfg
	if c.HealthTimeoutMS <= 0 {
		c.HealthTimeoutMS = 1500
	}
	return &AIServiceManager{
		cfg: c,
		status: AIServiceStatus{
			Mode:             strings.ToLower(strings.TrimSpace(c.Mode)),
			BaseURL:          strings.TrimSpace(c.BaseURL),
			AutoStart:        c.AutoStart,
			LastTransitionMS: nowMS(),
		},
		httpClient: &http.Client{Timeout: time.Duration(c.HealthTimeoutMS) * time.Millisecond},
	}
}

func (m *AIServiceManager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(m.cfg.Mode))
	if mode != "service" {
		return nil
	}

	if m.cfg.AutoStart && len(m.cfg.StartCommand) > 0 {
		if err := m.startProcessLocked(); err != nil {
			m.setProbeFailure(fmt.Errorf("start process: %w", err))
			return err
		}
	}

	go m.healthLoop(ctx)
	return nil
}

func (m *AIServiceManager) WaitForHealthy(ctx context.Context, timeout time.Duration) bool {
	if m == nil {
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		if m.IsHealthy() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *AIServiceManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return
	}
	_ = m.cmd.Process.Kill()
	m.cmd = nil
	m.status.Running = false
	m.status.PID = 0
	m.status.LastTransitionMS = nowMS()
}

func (m *AIServiceManager) StartProcess() error {
	if m == nil {
		return fmt.Errorf("nil ai service manager")
	}
	return m.startProcessLocked()
}

func (m *AIServiceManager) Restart() error {
	if m == nil {
		return fmt.Errorf("nil ai service manager")
	}
	m.Stop()
	return m.startProcessLocked()
}

func (m *AIServiceManager) IsHealthy() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status.Healthy
}

func (m *AIServiceManager) Snapshot() AIServiceStatus {
	if m == nil {
		return AIServiceStatus{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := m.status
	if m.status.Info != nil {
		info := *m.status.Info
		cp.Info = &info
	}
	return cp
}

func (m *AIServiceManager) healthLoop(ctx context.Context) {
	interval := time.Duration(m.cfg.HealthIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 5 * time.Second
	}

	// Trigger an eager probe first to reduce fallback delay on startup.
	m.probeOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.probeOnce(ctx)
		}
	}
}

func (m *AIServiceManager) probeOnce(ctx context.Context) {
	base := strings.TrimRight(strings.TrimSpace(m.cfg.BaseURL), "/")
	if base == "" {
		m.setProbeFailure(fmt.Errorf("empty service baseUrl"))
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, m.httpClient.Timeout)
	defer cancel()

	healthURL := base + "/healthz"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		m.setProbeFailure(err)
		return
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		m.setProbeFailure(err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		m.setProbeFailure(fmt.Errorf("health status %d", resp.StatusCode))
		return
	}
	var h AIServiceHealth
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		m.setProbeFailure(fmt.Errorf("decode health response: %w", err))
		return
	}
	if !h.OK {
		m.setProbeFailure(fmt.Errorf("service health reports not ok"))
		return
	}

	info, err := m.fetchInfo(ctx, base)
	if err != nil {
		m.setProbeFailure(err)
		return
	}
	m.setProbeSuccess(info)
}

func (m *AIServiceManager) fetchInfo(ctx context.Context, base string) (*AIServiceInfo, error) {
	reqCtx, cancel := context.WithTimeout(ctx, m.httpClient.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/service/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("service info status %d", resp.StatusCode)
	}
	var info AIServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (m *AIServiceManager) startProcessLocked() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		return nil
	}

	if len(m.cfg.StartCommand) == 0 {
		return fmt.Errorf("startCommand is empty")
	}
	cmd := exec.Command(m.cfg.StartCommand[0], m.cfg.StartCommand[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	m.status.Running = true
	m.status.PID = cmd.Process.Pid
	m.status.LastTransitionMS = nowMS()

	go func(localCmd *exec.Cmd) {
		err := localCmd.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd == localCmd {
			m.cmd = nil
			m.status.Running = false
			m.status.PID = 0
			if err != nil {
				m.status.LastError = err.Error()
			}
			m.status.LastTransitionMS = nowMS()
		}
	}(cmd)
	return nil
}

func (m *AIServiceManager) setProbeSuccess(info *AIServiceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Healthy = true
	m.status.LastError = ""
	m.status.LastCheckMS = nowMS()
	m.status.Info = info
}

func (m *AIServiceManager) setProbeFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Healthy = false
	m.status.LastCheckMS = nowMS()
	if err != nil {
		m.status.LastError = err.Error()
	}
}
