package hubsvc

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

type ServiceProcessRecord struct {
	ServiceID   string `json:"service_id"`
	PID         int    `json:"pid"`
	ExecPath    string `json:"exec_path"`
	StartedAtMS int64  `json:"started_at_ms"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

type serviceProcessSnapshot struct {
	Version     int                             `json:"version"`
	UpdatedAtMS int64                           `json:"updated_at_ms"`
	Records     map[string]ServiceProcessRecord `json:"records"`
}

type ServiceProcessStore struct {
	mu   sync.Mutex
	path string
	data serviceProcessSnapshot
}

func NewServiceProcessStore(path string) *ServiceProcessStore {
	store := &ServiceProcessStore{
		path: strings.TrimSpace(path),
		data: serviceProcessSnapshot{
			Version: 1,
			Records: map[string]ServiceProcessRecord{},
		},
	}
	store.load()
	return store
}

func (s *ServiceProcessStore) Get(serviceID string) (ServiceProcessRecord, bool) {
	if s == nil {
		return ServiceProcessRecord{}, false
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return ServiceProcessRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.data.Records[sid]
	return record, ok
}

func (s *ServiceProcessStore) Upsert(record ServiceProcessRecord) error {
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
	record.ServiceID = sid
	record.ExecPath = normalizeExecutablePath(record.ExecPath)
	if record.ExecPath == "" {
		return fmt.Errorf("exec_path is empty")
	}
	if record.StartedAtMS <= 0 {
		record.StartedAtMS = time.Now().UnixMilli()
	}
	record.UpdatedAtMS = time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Records == nil {
		s.data.Records = map[string]ServiceProcessRecord{}
	}
	s.data.Version = 1
	s.data.UpdatedAtMS = record.UpdatedAtMS
	s.data.Records[sid] = record
	if s.path == "" {
		return nil
	}
	return writeProcessJSONAtomic(s.path, s.data)
}

func CleanupPreviousServiceProcess(storePath string, serviceID string) error {
	store := NewServiceProcessStore(storePath)
	record, ok := store.Get(serviceID)
	if !ok {
		return nil
	}
	_, err := CleanupRecordedServiceProcess(record.PID, record.ExecPath, record.StartedAtMS)
	return err
}

func RecordCurrentServiceProcess(storePath string, serviceID string, startedAtMS int64) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	store := NewServiceProcessStore(storePath)
	return store.Upsert(ServiceProcessRecord{
		ServiceID:   strings.TrimSpace(serviceID),
		PID:         os.Getpid(),
		ExecPath:    execPath,
		StartedAtMS: startedAtMS,
	})
}

func CleanupRecordedServiceProcess(pid int, expectedExecPath string, expectedStartedAtMS int64) (bool, error) {
	if pid <= 1 || pid == os.Getpid() {
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
		if !isProcessAlive(pid) {
			return true, nil
		}
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
		if !isProcessAlive(pid) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

func (s *ServiceProcessStore) load() {
	if s == nil || s.path == "" {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var snap serviceProcessSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return
	}
	if snap.Version <= 0 {
		snap.Version = 1
	}
	if snap.Records == nil {
		snap.Records = map[string]ServiceProcessRecord{}
	}
	s.mu.Lock()
	s.data = snap
	s.mu.Unlock()
}

func writeProcessJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "proc-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func normalizeExecutablePath(path string) string {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return ""
	}
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return filepath.Clean(clean)
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

func processStartedAtMS(pid int) (int64, error) {
	if pid <= 1 {
		return 0, fmt.Errorf("invalid pid")
	}
	if runtime.GOOS == "windows" {
		return 0, fmt.Errorf("process start time not supported on windows")
	}
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return 0, fmt.Errorf("process start time not found")
	}
	parsed, err := time.Parse("Mon Jan 2 15:04:05 2006", text)
	if err != nil {
		return 0, err
	}
	return parsed.UnixMilli(), nil
}

func terminateProcess(pid int, timeout time.Duration) error {
	if pid <= 1 {
		return nil
	}
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return nil
		}
		time.Sleep(60 * time.Millisecond)
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	for i := 0; i < 4; i++ {
		if !isProcessAlive(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if isProcessAlive(pid) {
		return fmt.Errorf("process still alive after kill: pid=%d", pid)
	}
	return nil
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
