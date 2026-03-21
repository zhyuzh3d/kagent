package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
