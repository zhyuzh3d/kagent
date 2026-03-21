package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"
)

var nonSlugPattern = regexp.MustCompile(`[^a-z0-9_]+`)

func (h *AdminHandler) requireLifecycle() (*supervisor.LifecycleManager, error) {
	if h.lifecycle == nil {
		return nil, fmt.Errorf("lifecycle manager is not configured")
	}
	return h.lifecycle, nil
}

func (h *AdminHandler) requireServiceID(reqArgs map[string]any) (string, error) {
	serviceID, _ := reqArgs["service_id"].(string)
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return "", fmt.Errorf("service_id is required")
	}
	return serviceID, nil
}

func (h *AdminHandler) handleServiceGetTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	info, ok := lifecycle.ManagedServiceInfo(serviceID)
	if !ok {
		return toolproto.CallResponse{}, fmt.Errorf("managed service not found: %s", serviceID)
	}
	startupManifest, startupManifestErr := lifecycle.ReadStartupManifest(serviceID)
	runtimeManifest, runtimeManifestPath, runtimeManifestErr := lifecycle.ReadServiceRuntimeManifest(serviceID)
	config, configPath, configErr := lifecycle.ReadConfigJSON(serviceID)
	configx, _, configxErr := lifecycle.ReadConfigXJSON(serviceID)
	stateResp, _, stateErr := h.toolHandler.ProbeServiceTool(ctx, serviceID, "service.lifecycle.state.get", map[string]any{}, 2500)
	audits := h.auditStore.List(200)
	filteredAudits := make([]any, 0, 32)
	for _, item := range audits {
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), serviceID) {
			filteredAudits = append(filteredAudits, item)
		}
	}
	stateResult := map[string]any{}
	if stateErr == nil && stateResp.Ok {
		if payload, ok := stateResp.Result.(map[string]any); ok {
			stateResult = payload
		}
	}
	allToolViews := h.hubPlatform.ListTools()
	serviceToolIDs := map[string]struct{}{}
	if info.RegisteredManifest != nil {
		for _, descriptor := range info.RegisteredManifest.Provides {
			if toolID := strings.TrimSpace(descriptor.ToolID); toolID != "" {
				serviceToolIDs[toolID] = struct{}{}
			}
		}
	}
	toolViews := make([]any, 0, len(serviceToolIDs))
	for _, item := range allToolViews {
		if _, ok := serviceToolIDs[strings.TrimSpace(item.ToolID)]; !ok {
			continue
		}
		// Skip self in candidates list for cleaner UI in service-specific view
		if len(item.Candidates) > 0 {
			filtered := make([]toolproto.ToolCandidate, 0, len(item.Candidates))
			for _, c := range item.Candidates {
				if c.ServiceID != serviceID {
					filtered = append(filtered, c)
				}
			}
			item.Candidates = filtered
		}
		toolViews = append(toolViews, item)
	}
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"service":             info,
			"registered_manifest": info.RegisteredManifest,
			"startup_manifest":    startupManifest,
			"startup_manifest_ok": startupManifestErr == nil,
			"startup_manifest_err": func() string {
				if startupManifestErr != nil {
					return startupManifestErr.Error()
				}
				return ""
			}(),
			"runtime_manifest":      runtimeManifest,
			"runtime_manifest_path": runtimeManifestPath,
			"runtime_manifest_ok":   runtimeManifestErr == nil,
			"runtime_manifest_err": func() string {
				if runtimeManifestErr != nil {
					return runtimeManifestErr.Error()
				}
				return ""
			}(),
			"config":    config,
			"config_ok": configErr == nil,
			"config_path": func() string {
				if configPath != "" {
					return configPath
				}
				return info.ConfigPath
			}(),
			"config_err": func() string {
				if configErr != nil {
					return configErr.Error()
				}
				return ""
			}(),
			"configx":    configx,
			"configx_ok": configxErr == nil,
			"configx_err": func() string {
				if configxErr != nil {
					return configxErr.Error()
				}
				return ""
			}(),
			"state":       stateResult,
			"state_ok":    stateErr == nil,
			"state_error": errString(stateErr),
			"instances":   h.registry.GetByService(serviceID),
			"audits":      filteredAudits,
			"tool_views":  toolViews,
		},
	}, nil
}

func (h *AdminHandler) handleServiceStartTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := lifecycle.StartService(startCtx, serviceID)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	return toolproto.CallResponse{Ok: true, Result: out}, nil
}

func (h *AdminHandler) handleServiceStopTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	if err := lifecycle.StopService(serviceID, 7*time.Second); err != nil {
		return toolproto.CallResponse{}, err
	}
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	return toolproto.CallResponse{Ok: true, Result: map[string]any{"service_id": serviceID, "stopped": true}}, nil
}

func (h *AdminHandler) handleServiceRestartTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out, err := lifecycle.RestartService(startCtx, serviceID, 7*time.Second)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	return toolproto.CallResponse{Ok: true, Result: out}, nil
}

func (h *AdminHandler) handleServiceDrainTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	if err := lifecycle.DrainService(serviceID, 2500*time.Millisecond); err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{"service_id": serviceID, "draining": true}}, nil
}

func (h *AdminHandler) handleServiceRebindTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id": serviceID,
		"bindings":   h.hubPlatform.ListBindings(),
	}}, nil
}

func (h *AdminHandler) handleServiceDisableTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	// 1. Stop the service first
	_ = lifecycle.StopService(serviceID, 7*time.Second)

	// 2. Set enabled = false in config
	if err := lifecycle.SetServiceEnabled(serviceID, false); err != nil {
		return toolproto.CallResponse{}, err
	}

	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	return toolproto.CallResponse{Ok: true, Result: map[string]any{"service_id": serviceID, "enabled": false}}, nil
}

func (h *AdminHandler) handleServiceEnableTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}

	if err := lifecycle.SetServiceEnabled(serviceID, true); err != nil {
		return toolproto.CallResponse{}, err
	}

	// Optionally start the service automatically?
	// The user's rule says "managed services ... and the status should reflect real-time session".
	// Let's just enable it, user can click "Start" if they want.

	return toolproto.CallResponse{Ok: true, Result: map[string]any{"service_id": serviceID, "enabled": true}}, nil
}

func (h *AdminHandler) handleServiceGovernanceUpdateTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	enabled, ok := req.Args["enabled"].(bool)
	if !ok {
		return toolproto.CallResponse{}, fmt.Errorf("enabled is required")
	}
	reliability, _ := req.Args["reliability"].(string)
	prevInfo, prevOK := lifecycle.ManagedServiceInfo(serviceID)
	wasEnabled := prevOK && prevInfo.Enabled
	if !enabled {
		_ = lifecycle.StopService(serviceID, 7*time.Second)
	}
	if err := lifecycle.UpdateServiceGovernance(serviceID, enabled, reliability); err != nil {
		return toolproto.CallResponse{}, err
	}
	if enabled && !wasEnabled {
		startCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		if _, err := lifecycle.StartService(startCtx, serviceID); err != nil {
			h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
			return toolproto.CallResponse{}, fmt.Errorf("service enabled but auto-start failed: %w", err)
		}
	}
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	info, ok := lifecycle.ManagedServiceInfo(serviceID)
	if !ok {
		return toolproto.CallResponse{}, fmt.Errorf("managed service not found: %s", serviceID)
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":  serviceID,
		"enabled":     info.Enabled,
		"reliability": info.Reliability,
	}}, nil
}

func (h *AdminHandler) handleServiceManifestGetTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	info, _ := lifecycle.ManagedServiceInfo(serviceID)
	manifest, err := lifecycle.ReadStartupManifest(serviceID)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":    serviceID,
		"manifest_path": info.RuntimeManifestPath,
		"manifest":      manifest,
	}}, nil
}

func (h *AdminHandler) handleServiceManifestUpdateTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	raw, ok := req.Args["manifest"]
	if !ok {
		return toolproto.CallResponse{}, fmt.Errorf("manifest is required")
	}
	var manifest supervisor.StartupManifest
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &manifest); err != nil {
		return toolproto.CallResponse{}, err
	}
	if err := lifecycle.WriteStartupManifest(serviceID, manifest); err != nil {
		return toolproto.CallResponse{}, err
	}
	return h.handleServiceManifestGetTool(ctx, toolproto.CallRequest{Args: map[string]any{"service_id": serviceID}})
}

func (h *AdminHandler) handleServiceConfigGetTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	config, path, err := lifecycle.ReadConfigJSON(serviceID)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":  serviceID,
		"config_path": path,
		"config_json": config,
	}}, nil
}

func (h *AdminHandler) handleServiceConfigUpdateTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	raw, ok := req.Args["config_json"]
	if !ok {
		return toolproto.CallResponse{}, fmt.Errorf("config_json is required")
	}
	payload := map[string]any{}
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &payload); err != nil {
		return toolproto.CallResponse{}, err
	}
	configType, _ := req.Args["type"].(string)
	fileName := "config.json"
	if configType == "configx" {
		fileName = "configx.json"
	} else if configType == "manifest" {
		// handle manifest via manifest tool? or here?
		// for now keep it simple as user asked for restore default mainly
	}

	path, err := lifecycle.WriteConfigJSON(serviceID, payload, fileName)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":  serviceID,
		"config_path": path,
		"config_json": payload,
	}}, nil
}

func (h *AdminHandler) handleServiceBuildTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	buildCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result, err := lifecycle.BuildService(buildCtx, serviceID)
	if err != nil {
		return toolproto.CallResponse{Ok: false, Result: result, Error: &toolproto.Error{
			Code:    toolproto.ErrorCodeToolExecError,
			Message: err.Error(),
		}}, nil
	}
	return toolproto.CallResponse{Ok: true, Result: result}, nil
}

func (h *AdminHandler) handleServiceFilesListTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	items, dir, err := lifecycle.ListServiceFiles(serviceID)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{"service_id": serviceID, "dir": dir, "items": items}}, nil
}

func (h *AdminHandler) handleServiceFilesReadTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	path, _ := req.Args["path"].(string)
	raw, resolved, err := lifecycle.ReadServiceFile(serviceID, path)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":  serviceID,
		"path":        path,
		"resolved":    resolved,
		"data_base64": base64.StdEncoding.EncodeToString(raw),
	}}, nil
}

func (h *AdminHandler) handleServiceFilesWriteTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	path, _ := req.Args["path"].(string)
	dataBase64, _ := req.Args["data_base64"].(string)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
	if err != nil {
		return toolproto.CallResponse{}, fmt.Errorf("invalid data_base64")
	}
	resolved, err := lifecycle.WriteServiceFile(serviceID, path, raw)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id": serviceID,
		"path":       path,
		"resolved":   resolved,
		"size_bytes": len(raw),
		"ok":         true,
	}}, nil
}

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

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
