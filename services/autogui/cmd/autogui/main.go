package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-vgo/robotgo"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18087", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "hub register url")
	instanceID := flag.String("instance-id", "", "instance id")
	flag.Parse()

	serviceID := "autogui"
	projectRoot := filepath.Join("services", serviceID)
	if fi, err := os.Stat(projectRoot); err != nil || !fi.IsDir() {
		projectRoot = "."
	}
	if _, err := hubsvc.LoadProjectConfig(projectRoot); err != nil {
		fmt.Fprintf(os.Stderr, "load service config failed: %v\n", err)
		os.Exit(1)
	}
	secretPath := filepath.Join("services", "autogui", "run", ".service_secret")
	if _, err := os.Stat(secretPath); err != nil {
		secretPath = filepath.Join("run", ".service_secret")
	}
	processStorePath := filepath.Join("services", "autogui", "run", ".service_pid")
	if projectRoot == "." {
		processStorePath = filepath.Join("run", ".service_pid")
	}
	runtimeManifestPath := filepath.Join("services", "autogui", "run", "manifest_runtime.json")
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

	manifest := toolproto.NormalizeServiceManifest(toolproto.ServiceManifest{
		ServiceID:   serviceID,
		ServiceName: serviceID,
		Version:     "1.0.0",
		Visibility:  "public",
		Provides: []toolproto.ServiceTool{
			{
				ToolID: "autogui.mouse.move", Description: "移动鼠标到指定的绝对坐标 (x, y)", AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "integer"},
						"y": map[string]any{"type": "integer"},
					},
					"required": []string{"x", "y"},
				},
			},
			{
				ToolID: "autogui.mouse.click", Description: "模拟鼠标按键点击 (左键/右键/中键)", AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"button": map[string]any{"type": "string", "enum": []string{"left", "right", "center"}, "default": "left"},
						"double": map[string]any{"type": "boolean", "default": false},
					},
				},
			},
			{
				ToolID: "autogui.mouse.scroll", Description: "模拟鼠标滚轮滚动", AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"amount":    map[string]any{"type": "integer", "default": 1},
						"direction": map[string]any{"type": "string", "enum": []string{"up", "down"}, "default": "up"},
					},
				},
			},
			{
				ToolID: "autogui.keyboard.type", Description: "在当前焦点处模拟键盘输入字符串", AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string", "description": "要输入的文本内容"},
					},
					"required": []string{"text"},
				},
			},
			{
				ToolID: "autogui.keyboard.press", Description: "模拟单个按键及组合键的按下", AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"key":       map[string]any{"type": "string", "description": "主键名 (如: enter, space, a)"},
						"modifiers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "修饰键 (如: command, shift)"},
					},
					"required": []string{"key"},
				},
			},
			{
				ToolID: "autogui.screen.capture", Description: "全屏截图并返回 Base64 编码的 PNG 数据", AllowedCallerTypes: []string{"user", "service", "surface"},
				OutputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"png_base64": map[string]any{"type": "string"},
						"width":      map[string]any{"type": "integer"},
						"height":     map[string]any{"type": "integer"},
					},
				},
			},
			{
				ToolID:             "autogui.screen.capture_region",
				Description:        "指定区域截图",
				AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x":      map[string]any{"type": "integer"},
						"y":      map[string]any{"type": "integer"},
						"width":  map[string]any{"type": "integer"},
						"height": map[string]any{"type": "integer"},
					},
				},
			},
			{
				ToolID:             "autogui.text.insert",
				Description:        "优先通过剪贴板粘贴插入文本，必要时回退到逐字输入，并可选择清空或提交。",
				AllowedCallerTypes: []string{"user", "service", "surface"},
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text":         map[string]any{"type": "string"},
						"mode":         map[string]any{"type": "string", "enum": []string{"paste_preferred", "type_only"}, "default": "paste_preferred"},
						"clear_before": map[string]any{"type": "boolean", "default": false},
						"submit":       map[string]any{"type": "boolean", "default": false},
					},
					"required": []string{"text"},
				},
			},
			{ToolID: "service.lifecycle.health", Description: "服务健康状况探测", AllowedCallerTypes: []string{"service"}},
			{ToolID: "service.lifecycle.state.get", Description: "获取服务运行状态快照", AllowedCallerTypes: []string{"service"}},
			{ToolID: "service.lifecycle.shutdown", Description: "强制停止服务进程", AllowedCallerTypes: []string{"service"}},
		},
	})

	var shutdownOnce sync.Once
	var server *http.Server
	stop := func() {
		shutdownOnce.Do(func() {
			if server != nil {
				_ = server.Close()
			}
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		})
	}
	shutdownNow := func(reason string) {
		if reason != "" {
			fmt.Fprintf(os.Stderr, "autogui shutdown: %s\n", reason)
		}
		stop()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, toolproto.CallResponse{Ok: true, Result: map[string]any{"ok": true, "service_id": serviceID}})
	})
	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
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
		result, execErr := execute(req, serviceID, instance, *addr, stop)
		if execErr != nil {
			writeJSON(w, toolproto.CallResponse{Ok: false, Error: &toolproto.Error{Code: toolproto.ErrorCodeToolExecError, Message: execErr.Error()}})
			return
		}
		writeJSON(w, toolproto.CallResponse{Ok: true, Result: result})
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

func execute(req toolproto.CallRequest, serviceID string, instance string, addr string, stop func()) (map[string]any, error) {
	switch req.ToolID {
	case "service.lifecycle.health", "service.lifecycle.state.get":
		return map[string]any{
			"service_id":  serviceID,
			"instance_id": instance,
			"endpoint":    "http://" + strings.TrimSpace(addr),
			"pid":         os.Getpid(),
			"healthy":     true,
			"status":      "ready",
			"platform":    runtimePlatform(),
		}, nil
	case "service.lifecycle.shutdown":
		go func() {
			time.Sleep(100 * time.Millisecond)
			stop()
		}()
		return map[string]any{"message": "shutting down"}, nil
	case "autogui.mouse.move":
		x := asInt(req.Args["x"], 0)
		y := asInt(req.Args["y"], 0)
		robotgo.Move(x, y)
		return map[string]any{"x": x, "y": y}, nil
	case "autogui.mouse.click":
		button := firstNonEmpty(asString(req.Args["button"]), "left")
		doubleClick := asBool(req.Args["double"], false)
		robotgo.Click(button, doubleClick)
		return map[string]any{"button": button, "double": doubleClick}, nil
	case "autogui.mouse.scroll":
		amount := asInt(req.Args["amount"], 1)
		direction := firstNonEmpty(asString(req.Args["direction"]), "up")
		robotgo.ScrollDir(amount, direction)
		return map[string]any{"amount": amount, "direction": direction}, nil
	case "autogui.keyboard.type":
		text := asString(req.Args["text"])
		robotgo.TypeStr(text)
		return map[string]any{"text": text}, nil
	case "autogui.keyboard.press":
		key := asString(req.Args["key"])
		if key == "" {
			return nil, fmt.Errorf("key is required")
		}
		mods := asStringSlice(req.Args["modifiers"])
		args := make([]any, 0, len(mods))
		for _, item := range mods {
			args = append(args, item)
		}
		robotgo.KeyTap(key, args...)
		return map[string]any{"key": key, "modifiers": mods}, nil
	case "autogui.text.insert":
		text := asString(req.Args["text"])
		if text == "" {
			return nil, fmt.Errorf("text is required")
		}
		mode := firstNonEmpty(asString(req.Args["mode"]), "paste_preferred")
		clearBefore := asBool(req.Args["clear_before"], false)
		submit := asBool(req.Args["submit"], false)
		insertMode := "type_only"
		if clearBefore {
			clearModifier := "ctrl"
			if runtime.GOOS == "darwin" {
				clearModifier = "command"
			}
			robotgo.KeyTap("a", clearModifier)
			time.Sleep(40 * time.Millisecond)
			robotgo.KeyTap("backspace")
			time.Sleep(40 * time.Millisecond)
		}
		if mode != "type_only" {
			if err := robotgo.WriteAll(text); err == nil {
				pasteModifier := "ctrl"
				if runtime.GOOS == "darwin" {
					pasteModifier = "command"
				}
				robotgo.KeyTap("v", pasteModifier)
				insertMode = "paste"
			} else {
				insertMode = "type_fallback"
				robotgo.TypeStr(text)
			}
		} else {
			robotgo.TypeStr(text)
		}
		if submit {
			time.Sleep(40 * time.Millisecond)
			robotgo.KeyTap("enter")
		}
		return map[string]any{
			"text":         text,
			"mode":         mode,
			"insert_mode":  insertMode,
			"clear_before": clearBefore,
			"submit":       submit,
		}, nil
	case "autogui.screen.capture":
		return captureRegion(0, 0, 0, 0)
	case "autogui.screen.capture_region":
		return captureRegion(
			asInt(req.Args["x"], 0),
			asInt(req.Args["y"], 0),
			asInt(req.Args["width"], 0),
			asInt(req.Args["height"], 0),
		)
	default:
		return nil, fmt.Errorf("tool not found")
	}
}

func captureRegion(x, y, width, height int) (map[string]any, error) {
	bit := robotgo.CaptureScreen(x, y, width, height)
	if bit == nil {
		return nil, fmt.Errorf("capture screen failed")
	}
	defer robotgo.FreeBitmap(bit)
	img := robotgo.ToImage(bit)
	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, img); err != nil {
		return nil, err
	}
	return map[string]any{
		"x":           x,
		"y":           y,
		"width":       width,
		"height":      height,
		"png_base64":  base64.StdEncoding.EncodeToString(buf.Bytes()),
		"size_bytes":  buf.Len(),
		"captured_at": time.Now().Format(time.RFC3339),
	}, nil
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

func asString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func asInt(v any, fallback int) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return n
		}
	}
	return fallback
}

func asBool(v any, fallback bool) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return fallback
	}
}

func asStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runtimePlatform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
