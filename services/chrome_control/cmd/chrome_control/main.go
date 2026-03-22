package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/chrome_control/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18088", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "hub register url")
	instanceID := flag.String("instance-id", "", "instance id")
	flag.Parse()

	serviceID := "chrome_control"
	projectRoot := filepath.Join("services", serviceID)
	if fi, err := os.Stat(projectRoot); err != nil || !fi.IsDir() {
		projectRoot = "."
	}
	if _, err := hubsvc.LoadProjectConfig(projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "load service config failed: %v\n", err)
		os.Exit(1)
	}
	configPath := filepath.Join("services", serviceID, "config", "config.json")
	if projectRoot == "." {
		configPath = filepath.Join("config", "config.json")
	}
	runtimeRoot := filepath.Join("services", serviceID, "run")
	if projectRoot == "." {
		runtimeRoot = "run"
	}
	cfg, err := app.LoadConfig(configPath, ".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "load chrome_control config failed: %v\n", err)
		os.Exit(1)
	}

	secretPath := filepath.Join("services", serviceID, "run", ".service_secret")
	if _, err := os.Stat(secretPath); err != nil {
		secretPath = filepath.Join("run", ".service_secret")
	}
	processStorePath := filepath.Join("services", serviceID, "run", ".service_pid")
	if projectRoot == "." {
		processStorePath = filepath.Join("run", ".service_pid")
	}
	runtimeManifestPath := filepath.Join("services", serviceID, "run", "manifest_runtime.json")
	if projectRoot == "." {
		runtimeManifestPath = filepath.Join("run", "manifest_runtime.json")
	}
	bootstrap, err := hubsvc.LoadBootstrapSecret(secretPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load bootstrap secret failed: %v\n", err)
		os.Exit(1)
	}
	if err := hubsvc.CleanupPreviousServiceProcess(processStorePath, serviceID); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup previous process failed: %v\n", err)
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(bootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	hubToolCallURL := hubsvc.BuildHubToolCallURL(registerURL)
	instance := strings.TrimSpace(*instanceID)
	if instance == "" {
		instance = strings.TrimSpace(bootstrap.InstanceID)
	}
	if instance == "" {
		instance = serviceID + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			rootCancel()
			if server != nil {
				_ = server.Close()
			}
			time.Sleep(80 * time.Millisecond)
			os.Exit(0)
		})
	}

	svc := app.NewService(rootCtx, ".", runtimeRoot, cfg, instance, *addr, shutdownNow)
	if err := svc.StartupCleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "startup cleanup failed: %v\n", err)
		os.Exit(1)
	}
	manifest := app.ChromeControlManifest()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, toolproto.CallResponse{Ok: true, Result: map[string]any{"ok": true, "service_id": serviceID}})
	})
	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, serviceID, instance, bootstrap.H2SToken); err != nil {
			writeJSON(w, toolproto.CallResponse{Ok: false, Error: &toolproto.Error{Code: toolproto.ErrorCodeForbidden, Message: "invalid hub auth"}})
			return
		}
		var req toolproto.CallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, toolproto.CallResponse{Ok: false, Error: &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: "invalid request"}})
			return
		}
		req, err := toolproto.NormalizeRequest(req)
		if err != nil {
			writeJSON(w, toolproto.CallResponse{Ok: false, Error: &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: err.Error()}})
			return
		}
		result, execErr := svc.Execute(req)
		if execErr != nil {
			writeJSON(w, toolproto.CallResponse{Ok: false, Error: &toolproto.Error{Code: toolproto.ErrorCodeToolExecError, Message: execErr.Error()}})
			return
		}
		writeJSON(w, toolproto.CallResponse{Ok: true, Result: result})
	})
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux.HandleFunc("/service/tool/ws", func(w http.ResponseWriter, r *http.Request) {
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, serviceID, instance, bootstrap.H2SToken); err != nil {
			http.Error(w, "invalid hub auth", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		toolID := strings.TrimSpace(r.URL.Query().Get("tool_id"))
		if toolID == "" {
			toolID = strings.TrimSpace(r.URL.Query().Get("name"))
		}
		if toolID == "" {
			_ = conn.WriteJSON(map[string]any{"type": "error", "error": "tool_id is required"})
			return
		}
		var firstPayload []byte
		if mt, payload, err := conn.ReadMessage(); err == nil && mt == websocket.TextMessage {
			firstPayload = payload
		}
		err = svc.HandleWS(rootCtx, toolID, firstPayload, func(payload []byte) error {
			return conn.WriteMessage(websocket.TextMessage, payload)
		})
		if err != nil {
			_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		}
	})

	server = &http.Server{Addr: *addr, Handler: mux}
	ln, err := hubsvc.Listen(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen failed: %v\n", err)
		os.Exit(1)
	}
	startedAtMS := time.Now().UnixMilli()
	if err := hubsvc.RecordCurrentServiceProcess(processStorePath, serviceID, startedAtMS); err != nil {
		fmt.Fprintf(os.Stderr, "record current process failed: %v\n", err)
		os.Exit(1)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(ln)
	}()
	if registerURL != "" {
		registerResp, err := register(registerURL, bootstrap, manifest, instance, *addr)
		if err != nil {
			shutdownNow("register failed: " + err.Error())
			return
		}
		if err := hubsvc.WriteServiceRuntimeManifest(runtimeManifestPath, registerResp); err != nil {
			shutdownNow("write runtime manifest failed: " + err.Error())
			return
		}
		_ = hubsvc.DeleteBootstrapSecret(secretPath)
	}
	if hubToolCallURL != "" {
		startHubToolHeartbeatGuard(hubToolCallURL, serviceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), bootstrap, shutdownNow)
	}
	if err := <-serveErrCh; err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		os.Exit(1)
	}
}

func register(registerURL string, bootstrap hubsvc.BootstrapSecret, manifest toolproto.ServiceManifest, instance string, addr string) (toolproto.SupervisorRegisterResult, error) {
	healthy := true
	req := toolproto.CallRequest{
		ToolID: "hub.governance.service.register",
		Args: map[string]any{
			"service_id":  manifest.ServiceID,
			"instance_id": instance,
			"pid":         os.Getpid(),
			"version":     manifest.Version,
			"transport":   "tcp",
			"endpoint": map[string]any{
				"tcp_url": "http://" + strings.TrimSpace(addr),
			},
			"tools":   manifest.Provides,
			"healthy": &healthy,
		},
		Context: &toolproto.Context{
			RequestID: "reg-" + fmt.Sprintf("%d", time.Now().UnixNano()),
			TraceID:   "tr-" + fmt.Sprintf("%d", time.Now().UnixNano()),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: manifest.ServiceID,
			},
		},
	}
	raw, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, registerURL, bytes.NewReader(raw))
	if err != nil {
		return toolproto.SupervisorRegisterResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(httpReq.Header, bootstrap)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return toolproto.SupervisorRegisterResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return toolproto.SupervisorRegisterResult{}, err
	}
	return hubsvc.DecodeSupervisorRegisterResult(body)
}

func startHubToolHeartbeatGuard(hubToolCallURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			nowID := strconv.FormatInt(time.Now().UnixNano(), 10)
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  strings.TrimSpace(serviceID),
					"instance_id": strings.TrimSpace(instanceID),
					"status":      "ready",
					"healthy":     true,
					"pid":         pid,
					"endpoint":    strings.TrimSpace(endpoint),
				},
				Context: &toolproto.Context{
					RequestID: "hb-" + nowID,
					TraceID:   "tr-" + nowID,
					Caller: toolproto.Caller{
						Type:      "service",
						ServiceID: strings.TrimSpace(serviceID),
					},
				},
			}
			rawResp, statusCode, err := hubsvc.PostHubToolCall(&http.Client{Timeout: 3 * time.Second}, hubToolCallURL, serviceAuth, callReq)
			if err != nil {
				return err
			}
			if statusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			}
			var resp toolproto.CallResponse
			if err := json.Unmarshal(rawResp, &resp); err != nil {
				return fmt.Errorf("decode heartbeat response: %w", err)
			}
			if !resp.Ok {
				if resp.Error != nil && strings.TrimSpace(resp.Error.Message) != "" {
					return fmt.Errorf("heartbeat rejected: %s", strings.TrimSpace(resp.Error.Message))
				}
				return fmt.Errorf("heartbeat rejected")
			}
			return nil
		}

		if err := send(); err != nil {
			onFailure("hub heartbeat failed: " + err.Error())
			return
		}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := send(); err != nil {
				onFailure("hub heartbeat failed: " + err.Error())
				return
			}
		}
	}()
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
