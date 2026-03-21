package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"
)

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
			"service":               info,
			"registered_manifest":   info.RegisteredManifest,
			"startup_manifest":      startupManifest,
			"startup_manifest_ok":   startupManifestErr == nil,
			"startup_manifest_err":  errString(startupManifestErr),
			"runtime_manifest":      runtimeManifest,
			"runtime_manifest_path": runtimeManifestPath,
			"runtime_manifest_ok":   runtimeManifestErr == nil,
			"runtime_manifest_err":  errString(runtimeManifestErr),
			"config":                config,
			"config_ok":             configErr == nil,
			"config_path": func() string {
				if configPath != "" {
					return configPath
				}
				return info.ConfigPath
			}(),
			"config_err":  errString(configErr),
			"configx":     configx,
			"configx_ok":  configxErr == nil,
			"configx_err": errString(configxErr),
			"state":       stateResult,
			"state_ok":    stateErr == nil,
			"state_error": errString(stateErr),
			"instances":   h.registry.GetByService(serviceID),
			"audits":      filteredAudits,
			"tool_views":  toolViews,
		},
	}, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
