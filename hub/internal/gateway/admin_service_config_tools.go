package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"
)

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
