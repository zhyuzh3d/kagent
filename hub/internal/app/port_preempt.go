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
	"strconv"
	"strings"
	"time"
)

// EnsurePortReady ensures the given address is ready for listening.
// It follows a "Request-then-Kill" strategy to clear the port.
func EnsurePortReady(addr string) error {
	// 1. Check if already listening
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		ln.Close()
		return nil
	}

	// 2. Port is occupied, try graceful shutdown if it's a Hub instance
	// We use a short timeout and ignore errors (might not be a Hub)
	adminURL := fmt.Sprintf("http://%s/api/tool/call", addr)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{
		"tool_id": "hub.system.shutdown",
		"args":    map[string]any{},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, adminURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}

	// 3. Short wait for graceful exit
	time.Sleep(800 * time.Millisecond)

	// 4. Check again
	ln, err = net.Listen("tcp", addr)
	if err == nil {
		ln.Close()
		return nil
	}

	// 5. Still occupied, proceed with "Force Kill"
	_, portStr, _ := net.SplitHostPort(addr)
	pid := findPIDByPort(portStr)
	if pid > 0 && pid != os.Getpid() {
		if err := killProcess(pid); err == nil {
			Infof("System:Internal:Startup:PortPreempted: %s", portStr)
			// Small settle time
			time.Sleep(200 * time.Millisecond)
		}
	}

	return nil
}

func findPIDByPort(port string) int {
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
		return 0
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0
	}

	// On Windows, the PID is the last column
	if runtime.GOOS == "windows" {
		fields := strings.Fields(lines[0])
		if len(fields) > 0 {
			pid, _ := strconv.Atoi(fields[len(fields)-1])
			return pid
		}
		return 0
	}

	// On Unix, lsof -t returns just the PID
	pid, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	return pid
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
