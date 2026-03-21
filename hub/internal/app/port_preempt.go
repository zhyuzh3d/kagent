package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EnsurePortReady ensures the given address is ready for listening.
// It follows a "Request-then-Kill" strategy to clear the port.
func EnsurePortReady(addr string) error {
	expectedExecPath, _ := os.Executable()
	expectedExecPath = normalizeExecutablePath(expectedExecPath)
	_, portStr, _ := net.SplitHostPort(addr)

	// 1. If no other process is listening on this port, it is ready.
	pids := findPIDsByPort(portStr)
	if len(pids) == 0 {
		return nil
	}

	// 2. Port is occupied, try graceful shutdown if it's a Hub instance
	adminURL := fmt.Sprintf("http://%s/api/tool/call", addr)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{
		"tool_id": "hub.system.shutdown",
		"args":    map[string]any{},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, adminURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}

	// 3. Robust wait for old listeners to exit, then force kill remaining Hub listeners.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pids = findPIDsByPort(portStr)
		if len(pids) == 0 {
			return nil
		}
		for _, pid := range pids {
			if pid <= 0 || pid == os.Getpid() {
				continue
			}
			cleaned, _ := CleanHubProcessByPID(pid, expectedExecPath)
			if cleaned {
				Infof("System:Internal:Startup:PortPreempted: %s pid=%d", portStr, pid)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	if remaining := findPIDsByPort(portStr); len(remaining) > 0 {
		return fmt.Errorf("timeout waiting for port %s to be released; remaining_pids=%v", addr, remaining)
	}
	return nil
}

func findPIDsByPort(port string) []int {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// netstat -ano | findstr :PORT
		cmd = exec.Command("cmd", "/c", fmt.Sprintf("netstat -ano | findstr :%s", port))
	} else {
		// lsof -t -i:PORT
		cmd = exec.Command("lsof", "-t", "-i:"+port)
	}

	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return nil
	}
	out := make([]int, 0, len(lines))
	seen := map[int]struct{}{}

	// On Windows, the PID is the last column
	if runtime.GOOS == "windows" {
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			pid, _ := strconv.Atoi(fields[len(fields)-1])
			if pid <= 0 {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			out = append(out, pid)
		}
		slices.Sort(out)
		return out
	}

	// On Unix, lsof -t returns just the PID
	for _, line := range lines {
		pid, _ := strconv.Atoi(strings.TrimSpace(line))
		if pid <= 0 {
			continue
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		out = append(out, pid)
	}
	slices.Sort(out)
	return out
}

func killProcess(pid int) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
	} else {
		cmd = exec.Command("kill", "-9", strconv.Itoa(pid))
	}
	return cmd.Run()
}
