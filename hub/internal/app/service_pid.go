package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type ServicePidRecord struct {
	ServiceID   string `json:"service_id"`
	PID         int    `json:"pid"`
	ProcessName string `json:"process_name,omitempty"`
	ExecPath    string `json:"exec_path"`
	StartedAtMS int64  `json:"started_at_ms"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

type ServicePidSnapshot struct {
	Version     int                         `json:"version"`
	UpdatedAtMS int64                       `json:"updated_at_ms"`
	Records     map[string]ServicePidRecord `json:"records"`
}

type ServicePidStore struct {
	mu   sync.Mutex
	path string
	data ServicePidSnapshot
}

func NewServicePidStore(path string) *ServicePidStore {
	store := &ServicePidStore{
		path: strings.TrimSpace(path),
		data: ServicePidSnapshot{
			Version: 1,
			Records: map[string]ServicePidRecord{},
		},
	}
	store.load()
	return store
}

func (s *ServicePidStore) Get(serviceID string) (ServicePidRecord, bool) {
	if s == nil {
		return ServicePidRecord{}, false
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return ServicePidRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.data.Records[sid]
	return rec, ok
}

func (s *ServicePidStore) Upsert(record ServicePidRecord) error {
	if s == nil {
		return nil
	}
	sid := strings.TrimSpace(record.ServiceID)
	if sid == "" {
		return fmt.Errorf("service_id is empty")
	}
	if record.PID <= 1 {
		return fmt.Errorf("pid is invalid")
	}
	execPath := normalizeExecutablePath(record.ExecPath)
	if execPath == "" {
		return fmt.Errorf("exec_path is empty")
	}
	startedAtMS := record.StartedAtMS
	if startedAtMS <= 0 {
		startedAtMS = time.Now().UnixMilli()
	}
	processName := strings.TrimSpace(record.ProcessName)
	if processName == "" {
		processName = filepath.Base(execPath)
	}
	nowMS := time.Now().UnixMilli()
	record.ServiceID = sid
	record.ExecPath = execPath
	record.ProcessName = processName
	record.StartedAtMS = startedAtMS
	record.UpdatedAtMS = nowMS

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Records == nil {
		s.data.Records = map[string]ServicePidRecord{}
	}
	s.data.Version = 1
	s.data.UpdatedAtMS = nowMS
	s.data.Records[sid] = record
	if s.path == "" {
		return nil
	}
	return writeJSONAtomic(s.path, s.data)
}

func (s *ServicePidStore) load() {
	if s == nil || s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var snap ServicePidSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return
	}
	if snap.Version <= 0 {
		snap.Version = 1
	}
	if snap.Records == nil {
		snap.Records = map[string]ServicePidRecord{}
	}
	s.mu.Lock()
	s.data = snap
	s.mu.Unlock()
}

// CleanRecordedProcess only kills the process if it is alive and both the
// executable path and, when available, the recorded start time match.
func CleanRecordedProcess(pid int, expectedExecPath string, expectedStartedAtMS int64) (bool, error) {
	if pid <= 1 {
		return false, nil
	}
	if pid == os.Getpid() {
		return false, nil
	}
	if !isProcessAlive(pid) {
		return false, nil
	}
	expected := normalizeExecutablePath(expectedExecPath)
	if expected == "" {
		return false, nil
	}
	actualExecPath, err := processExecutablePath(pid)
	if err != nil {
		return false, err
	}
	if normalizeExecutablePath(actualExecPath) != expected {
		return false, nil
	}
	if expectedStartedAtMS > 0 {
		if actualStartedAtMS, err := processStartedAtMS(pid); err == nil && actualStartedAtMS > 0 {
			if absInt64(actualStartedAtMS-expectedStartedAtMS) > int64(2*time.Second/time.Millisecond) {
				return false, nil
			}
		}
	}
	if err := terminateProcess(pid, 1500*time.Millisecond); err != nil {
		return false, err
	}
	return true, nil
}

func normalizeExecutablePath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	return filepath.Clean(cleaned)
}

func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func processExecutablePath(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("invalid pid")
	}
	if runtime.GOOS == "windows" {
		cmd := exec.Command("wmic", "process", "where", fmt.Sprintf("processid=%d", pid), "get", "ExecutablePath", "/value")
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "executablepath=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "ExecutablePath=")), nil
			}
		}
		return "", fmt.Errorf("executable path not found")
	}
	cmd := exec.Command("lsof", "-p", strconv.Itoa(pid), "-a", "-d", "txt", "-Fn")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "n") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "n"))
			if path != "" {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("executable path not found")
}

func processName(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("invalid pid")
	}
	if runtime.GOOS == "windows" {
		cmd := exec.Command("wmic", "process", "where", fmt.Sprintf("processid=%d", pid), "get", "Name", "/value")
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "name=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "Name=")), nil
			}
		}
		return "", fmt.Errorf("process name not found")
	}
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("process name not found")
	}
	return filepath.Base(name), nil
}

func CleanHubProcessByPID(pid int, expectedExecPath string) (bool, error) {
	if pid <= 1 || pid == os.Getpid() {
		return false, nil
	}
	if !isProcessAlive(pid) {
		return false, nil
	}
	expectedBase := filepath.Base(normalizeExecutablePath(expectedExecPath))
	if expectedBase == "" {
		expectedBase = "kagent"
	}
	if cleaned, err := CleanRecordedProcess(pid, expectedExecPath, 0); cleaned {
		return true, nil
	} else if err != nil {
		// Fall through to looser matching and, if needed, last-resort termination.
	}
	actualExecPath, execErr := processExecutablePath(pid)
	if execErr == nil && filepath.Base(normalizeExecutablePath(actualExecPath)) == expectedBase {
		if err := terminateProcess(pid, 1500*time.Millisecond); err != nil {
			return false, err
		}
		return true, nil
	}
	name, nameErr := processName(pid)
	if nameErr == nil && filepath.Base(strings.TrimSpace(name)) == expectedBase {
		if err := terminateProcess(pid, 1500*time.Millisecond); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := terminateProcess(pid, 1500*time.Millisecond); err != nil {
		return false, err
	}
	return true, nil
}

func processStartedAtMS(pid int) (int64, error) {
	if pid <= 1 {
		return 0, fmt.Errorf("invalid pid")
	}
	if runtime.GOOS == "windows" {
		return 0, fmt.Errorf("not implemented on windows")
	}
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return 0, fmt.Errorf("empty process start time")
	}
	normalized := strings.Join(strings.Fields(text), " ")
	startedAt, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", normalized, time.Local)
	if err != nil {
		return 0, err
	}
	return startedAt.UnixMilli(), nil
}

func terminateProcess(pid int, grace time.Duration) error {
	if pid <= 1 {
		return nil
	}
	if runtime.GOOS == "windows" {
		return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
	}
	if grace <= 0 {
		grace = 1500 * time.Millisecond
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return killProcess(pid)
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
