package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	app "kagent/hub/internal/app"
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
