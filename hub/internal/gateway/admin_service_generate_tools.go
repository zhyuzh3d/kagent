package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"
)

var nonSlugPattern = regexp.MustCompile(`[^a-z0-9_]+`)

func (h *AdminHandler) handleServiceGenerateTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	identity := app.IdentityFromContext(ctx)
	userID := strings.TrimSpace(identity.ID)
	if userID == "" {
		return toolproto.CallResponse{}, fmt.Errorf("missing user identity")
	}
	rawPrompt, _ := req.Args["prompt"].(string)
	rawName, _ := req.Args["service_name"].(string)
	slug := normalizeServiceSlug(firstNonEmpty(rawName, rawPrompt, "custom_service"))
	if slug == "" {
		slug = "custom_service"
	}
	serviceID := slug
	serviceDir := filepath.Join(h.appRoot, "data", "user", userID, "service", "custom", serviceID)
	port := supervisor.NextSuggestedPort(lifecycle.ListManagedServices())
	startupManifest := supervisor.StartupManifest{
		ServiceID: serviceID,
		Version:   "1.0.0",
		Entry: supervisor.StartupManifestEntry{
			Args: []string{"-addr", fmt.Sprintf("127.0.0.1:%d", port)},
			Env:  map[string]string{},
		},
		Lifecycle: supervisor.StartupManifestPolicy{
			RegisterTimeoutMS: 1500,
			RestartPolicy:     "never",
			RestartBackoffMS:  300,
			RestartTimes:      2,
		},
	}
	files := map[string]string{
		filepath.Join(serviceDir, "go.mod"):                         renderGeneratedServiceGoMod(h.appRoot, serviceID),
		filepath.Join(serviceDir, "manifest.json"):                  mustJSONText(startupManifest),
		filepath.Join(serviceDir, "run", "manifest.json"):           mustJSONText(startupManifest),
		filepath.Join(serviceDir, "config", "config.json"):          "{\n  \"service\": {\n    \"welcome\": \"hello from generated service\"\n  }\n}\n",
		filepath.Join(serviceDir, "config", "configx.json"):         "{}\n",
		filepath.Join(serviceDir, "config", "configx.json.example"): "{\n  \"secrets\": {}\n}\n",
		filepath.Join(serviceDir, "README.md"):                      renderGeneratedServiceReadme(serviceID, rawPrompt, port),
		filepath.Join(serviceDir, "cmd", serviceID, "main.go"):      renderGeneratedServiceMain(serviceID),
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return toolproto.CallResponse{}, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return toolproto.CallResponse{}, err
		}
	}
	if text, ok := h.runServiceScaffoldGeneration(ctx, serviceID, rawPrompt); ok {
		if generatedFiles, parseErr := parseGeneratedFilesMap(text); parseErr == nil && len(generatedFiles) > 0 {
			for relPath, content := range generatedFiles {
				target := filepath.Clean(filepath.Join(serviceDir, relPath))
				if !strings.HasPrefix(target, filepath.Clean(serviceDir)+string(filepath.Separator)) && target != filepath.Clean(serviceDir) {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					continue
				}
				_ = os.WriteFile(target, []byte(content), 0o644)
			}
		}
	}
	if err := lifecycle.UpsertManagedService(supervisor.LifecycleServiceEntry{
		ServiceID: serviceID,
		Dir:       serviceDir,
	}); err != nil {
		return toolproto.CallResponse{}, err
	}
	info, _ := lifecycle.ManagedServiceInfo(serviceID)
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":       serviceID,
		"dir":              serviceDir,
		"port":             port,
		"startup_manifest": startupManifest,
		"managed_service":  info,
		"prompt_summary":   strings.TrimSpace(rawPrompt),
	}}, nil
}

func (h *AdminHandler) runServiceScaffoldGeneration(ctx context.Context, serviceID string, prompt string) (string, bool) {
	if h.toolHandler == nil || strings.TrimSpace(prompt) == "" {
		return "", false
	}
	systemPrompt := `你是一个 Go service scaffold 生成器。你必须只输出 JSON，格式为 {"files":{"相对路径":"文件内容"}}。
要求：
1. 只返回和 service scaffold 直接相关的文本文件。
2. 相对路径只允许：go.mod、README.md、manifest.json、run/manifest.json、config/config.json、config/configx.json、config/configx.json.example、cmd/` + serviceID + `/main.go。
3. main.go 必须实现一个可注册到 Hub 的最小 service，并至少包含 ` + serviceID + `.echo、service.lifecycle.health、service.lifecycle.state.get、service.lifecycle.shutdown。
4. 不要输出 markdown 代码块，不要输出解释。`
	result, _, err := h.toolHandler.ProbeServiceTool(ctx, "ai_doubao", "ai.llm.generate", map[string]any{
		"system_prompt": systemPrompt,
		"input":         fmt.Sprintf("service_id=%s\n用户需求：%s", serviceID, strings.TrimSpace(prompt)),
	}, 65000)
	if err != nil || !result.Ok {
		return "", false
	}
	payload, _ := result.Result.(map[string]any)
	text, _ := payload["text"].(string)
	text = strings.TrimSpace(text)
	return text, text != ""
}

func normalizeServiceSlug(raw string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	text = strings.ReplaceAll(text, "-", "_")
	text = nonSlugPattern.ReplaceAllString(text, "_")
	text = strings.Trim(text, "_")
	for strings.Contains(text, "__") {
		text = strings.ReplaceAll(text, "__", "_")
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mustJSONText(value any) string {
	raw, _ := json.MarshalIndent(value, "", "  ")
	return string(raw) + "\n"
}

func parseGeneratedFilesMap(raw string) (map[string]string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, fmt.Errorf("generated text is empty")
	}
	var payload struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err == nil && len(payload.Files) > 0 {
		return payload.Files, nil
	}
	if start := strings.Index(text, "{"); start >= 0 {
		text = text[start:]
		if err := json.Unmarshal([]byte(text), &payload); err == nil && len(payload.Files) > 0 {
			return payload.Files, nil
		}
	}
	return nil, fmt.Errorf("invalid generated files payload")
}

func renderGeneratedServiceGoMod(appRoot string, serviceID string) string {
	return fmt.Sprintf("module generated/%s\n\ngo 1.25.0\n\nrequire kagent v0.0.0\n\nreplace kagent => %s\n", serviceID, filepath.Clean(appRoot))
}

func renderGeneratedServiceReadme(serviceID string, prompt string, port int) string {
	return fmt.Sprintf("# %s\n\nGenerated scaffold for prompt:\n\n%s\n\nDefault listen port: `%d`\n", serviceID, firstNonEmpty(prompt, "(empty)"), port)
}

func renderGeneratedServiceMain(serviceID string) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18110", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "hub register endpoint")
	instanceID := flag.String("instance-id", "", "service instance id")
	flag.Parse()

	serviceID := %q
	instance := strings.TrimSpace(*instanceID)
	if instance == "" {
		instance = serviceID + "-" + fmt.Sprintf("%%d", time.Now().UnixNano())
	}
	runSecretPath := filepath.Join("run", ".service_secret")
	bootstrap, err := hubsvc.LoadBootstrapSecret(runSecretPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load bootstrap secret failed: %%v\n", err)
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(bootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	manifest := toolproto.NormalizeServiceManifest(toolproto.ServiceManifest{
		ServiceID:   serviceID,
		ServiceName: serviceID,
		Version:     "1.0.0",
		Visibility:  "public",
		Provides: []toolproto.ServiceTool{
			{ToolID: serviceID + ".echo", Description: "Echo generated payload", AllowedCallerTypes: []string{"user", "service"}},
			{ToolID: "service.lifecycle.health", Description: "service health probe", AllowedCallerTypes: []string{"service"}},
			{ToolID: "service.lifecycle.state.get", Description: "service lifecycle state snapshot", AllowedCallerTypes: []string{"service"}},
			{ToolID: "service.lifecycle.shutdown", Description: "service shutdown", AllowedCallerTypes: []string{"service"}},
		},
	})

	if registerURL != "" {
		req := toolproto.CallRequest{
			ToolID: "hub.governance.service.register",
			Args: map[string]any{
				"service_id":  manifest.ServiceID,
				"instance_id": instance,
				"version":     manifest.Version,
				"transport":   "tcp",
				"endpoint": map[string]any{
					"tcp_url": "http://" + strings.TrimSpace(*addr),
				},
				"tools":   manifest.Provides,
				"healthy": true,
			},
			Context: &toolproto.Context{
				RequestID: "reg-" + fmt.Sprintf("%%d", time.Now().UnixNano()),
				TraceID:   "tr-" + fmt.Sprintf("%%d", time.Now().UnixNano()),
				Caller: toolproto.Caller{
					Type:      "service",
					ServiceID: manifest.ServiceID,
				},
			},
		}
		if err := postHubRegister(registerURL, bootstrap, req); err != nil {
			fmt.Fprintf(os.Stderr, "register failed: %%v\n", err)
			os.Exit(1)
		}
		_ = hubsvc.DeleteBootstrapSecret(runSecretPath)
	}

	mux := http.NewServeMux()
	var server *http.Server
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
		switch req.ToolID {
		case serviceID + ".echo":
			writeJSON(w, toolproto.CallResponse{Ok: true, Result: map[string]any{
				"service_id": serviceID,
				"echo":       req.Args,
				"message":    "generated service is alive",
			}})
		case "service.lifecycle.health", "service.lifecycle.state.get":
			writeJSON(w, toolproto.CallResponse{Ok: true, Result: map[string]any{
				"service_id":  serviceID,
				"instance_id": instance,
				"endpoint":    "http://" + strings.TrimSpace(*addr),
				"pid":         os.Getpid(),
				"healthy":     true,
				"status":      "ready",
			}})
		case "service.lifecycle.shutdown":
			go func() {
				time.Sleep(100 * time.Millisecond)
				if server != nil {
					_ = server.Close()
				}
				os.Exit(0)
			}()
			writeJSON(w, toolproto.CallResponse{Ok: true, Result: map[string]any{"message": "shutting down"}})
		default:
			writeJSON(w, toolproto.CallResponse{Ok: false, Error: &toolproto.Error{Code: toolproto.ErrorCodeToolNotFound, Message: "tool not found"}})
		}
	})
	server = &http.Server{Addr: *addr, Handler: mux}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "listen failed: %%v\n", err)
		os.Exit(1)
	}
}

func postHubRegister(registerURL string, bootstrap hubsvc.BootstrapSecret, req toolproto.CallRequest) error {
	raw, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, registerURL, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(httpReq.Header, bootstrap)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %%d", resp.StatusCode)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
`, serviceID)
}
