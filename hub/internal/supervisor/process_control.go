package supervisor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	app "kagent/hub/internal/app"
	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

const serviceSelfShutdownGrace = 500 * time.Millisecond

// BuildServiceControlURL constructs an HTTP URL for controlling a service at the given endpoint.
// This is the canonical implementation — do NOT duplicate in other packages.
func BuildServiceControlURL(endpoint string, targetPath string) string {
	base := strings.TrimSpace(endpoint)
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	parsed.Path = strings.TrimSpace(targetPath)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// BroadcastServiceShutdown sends shutdown requests to all registered services concurrently.
func BroadcastServiceShutdown(hubPlatform *app.HubPlatform, timeout time.Duration) {
	if hubPlatform == nil {
		return
	}
	services := hubPlatform.ListRegisteredServices()
	if len(services) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, reg := range services {
		reg := reg
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := StopServiceRegistration(hubPlatform, reg, timeout); err != nil {
				app.Warnf("broadcast shutdown failed for service=%s instance=%s pid=%d endpoint=%s err=%v", reg.ServiceID, reg.InstanceID, reg.PID, reg.Endpoint, err)
				return
			}
			hubPlatform.UnregisterService(reg.ServiceID, reg.InstanceID)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	maxWait := timeout + 2500*time.Millisecond
	select {
	case <-done:
	case <-time.After(maxWait):
		app.Warnf("broadcast shutdown timeout after %v", maxWait)
	}
}

// StopServiceRegistration asks the service to stop itself, waits briefly, then force kills if needed.
func StopServiceRegistration(hubPlatform *app.HubPlatform, reg app.HubServiceRegistration, timeout time.Duration) error {
	if hubPlatform != nil {
		_, _, _ = callServiceLifecycleTool(hubPlatform, reg, "service.lifecycle.shutdown", map[string]any{
			"reason": "hub supervisor shutdown",
		}, serviceSelfShutdownGrace)
	}

	deadline := time.Now().Add(serviceSelfShutdownGrace)
	for time.Now().Before(deadline) {
		alive, pidAlive := ServiceRuntimeAlive(hubPlatform, reg)
		if !alive && !pidAlive {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if reg.PID > 1 && IsPIDAlive(reg.PID) {
		_ = syscall.Kill(reg.PID, syscall.SIGKILL)
		time.Sleep(100 * time.Millisecond)
	}
	alive, pidAlive := ServiceRuntimeAlive(hubPlatform, reg)
	if alive || pidAlive {
		return fmt.Errorf("service still alive after shutdown attempts")
	}
	return nil
}

// ServiceRuntimeAlive checks if a service is alive via lifecycle health tool, endpoint health check and PID.
func ServiceRuntimeAlive(hubPlatform *app.HubPlatform, reg app.HubServiceRegistration) (bool, bool) {
	epAlive := false
	if hubPlatform != nil {
		if resp, _, err := callServiceLifecycleTool(hubPlatform, reg, "service.lifecycle.health", map[string]any{}, 1200*time.Millisecond); err == nil {
			epAlive = resp.Ok
		}
	}
	if !epAlive && strings.TrimSpace(reg.ServiceID) != "account" {
		if healthzURL := BuildServiceControlURL(reg.Endpoint, "/healthz"); healthzURL != "" {
			epAlive = IsServiceEndpointAlive(healthzURL)
		}
	}
	pidAlive := reg.PID > 1 && IsPIDAlive(reg.PID)
	return epAlive, pidAlive
}

func callServiceLifecycleTool(hubPlatform *app.HubPlatform, reg app.HubServiceRegistration, toolID string, args map[string]any, timeout time.Duration) (toolproto.CallResponse, int, error) {
	if hubPlatform == nil {
		return toolproto.CallResponse{}, 0, fmt.Errorf("hub platform is nil")
	}
	auth, ok := hubPlatform.ServiceHubAuth(reg.ServiceID)
	if !ok || strings.TrimSpace(auth.H2SToken) == "" {
		return toolproto.CallResponse{}, 0, fmt.Errorf("missing hub auth for service %s", strings.TrimSpace(reg.ServiceID))
	}
	callReq := toolproto.CallRequest{
		ToolID: strings.TrimSpace(toolID),
		Args:   args,
		Context: &toolproto.Context{
			RequestID: "lifecycle-" + newStamp(),
			TraceID:   "tr-" + newStamp(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: strings.TrimSpace(reg.ServiceID),
			},
		},
	}
	raw, err := json.Marshal(callReq)
	if err != nil {
		return toolproto.CallResponse{}, 0, err
	}
	execURL := BuildServiceControlURL(reg.Endpoint, "/service/tool/exec")
	if execURL == "" {
		return toolproto.CallResponse{}, 0, fmt.Errorf("service endpoint is empty")
	}
	req, err := http.NewRequest(http.MethodPost, execURL, strings.NewReader(string(raw)))
	if err != nil {
		return toolproto.CallResponse{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyHubAuthHeaders(req.Header, strings.TrimSpace(auth.ServiceID), strings.TrimSpace(auth.InstanceID), strings.TrimSpace(auth.H2SToken))
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return toolproto.CallResponse{}, 0, err
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return toolproto.CallResponse{}, resp.StatusCode, err
	}
	var out toolproto.CallResponse
	if len(rawResp) > 0 {
		if err := json.Unmarshal(rawResp, &out); err != nil {
			return toolproto.CallResponse{}, resp.StatusCode, fmt.Errorf("decode service tool response: %w", err)
		}
	}
	return out, resp.StatusCode, nil
}

// IsServiceEndpointAlive checks if a health endpoint is responding.
func IsServiceEndpointAlive(healthzURL string) bool {
	client := &http.Client{Timeout: 900 * time.Millisecond}
	resp, err := client.Get(healthzURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

// IsPIDAlive checks whether a process with the given PID is still running.
func IsPIDAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
