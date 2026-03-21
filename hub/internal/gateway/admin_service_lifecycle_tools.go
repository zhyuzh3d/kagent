package gateway

import (
	"context"
	"fmt"
	"time"

	"kagent/pkg/toolproto"
)

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
	_ = lifecycle.StopService(serviceID, 7*time.Second)
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
