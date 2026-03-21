package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/observability"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/supervisor"
	"kagent/pkg/toolproto"
)

type serviceBindRequest struct {
	ToolID    string `json:"tool_id"`
	ServiceID string `json:"service_id"`
}

type adminServiceListItem struct {
	ServiceID           string                            `json:"service_id"`
	Description         string                            `json:"description,omitempty"`
	Dir                 string                            `json:"dir,omitempty"`
	Enabled             bool                              `json:"enabled"`
	Reliability         string                            `json:"reliability,omitempty"`
	DirAbs              string                            `json:"dir_abs,omitempty"`
	ExecPath            string                            `json:"exec_path,omitempty"`
	StartupManifestPath string                            `json:"startup_manifest_path,omitempty"`
	RuntimeManifestPath string                            `json:"runtime_manifest_path,omitempty"`
	ConfigPath          string                            `json:"config_path,omitempty"`
	HasSourceConfig     bool                              `json:"has_source_config"`
	HasStartupManifest  bool                              `json:"has_startup_manifest"`
	HasRuntimeManifest  bool                              `json:"has_runtime_manifest"`
	HasExec             bool                              `json:"has_exec"`
	HasGoMod            bool                              `json:"has_go_mod"`
	Registered          bool                              `json:"registered"`
	Active              bool                              `json:"active"`
	Healthy             bool                              `json:"healthy"`
	Status              string                            `json:"status,omitempty"`
	InstanceID          string                            `json:"instance_id,omitempty"`
	Endpoint            string                            `json:"endpoint,omitempty"`
	PID                 int                               `json:"pid,omitempty"`
	RegisteredManifest  *app.ServiceManifest              `json:"registered_manifest,omitempty"`
	Startup             *supervisor.ManagedServiceStartup `json:"startup,omitempty"`
}

// AdminHandler handles /api/admin/* endpoints.
type AdminHandler struct {
	authService   *app.AuthService
	hubPlatform   *app.HubPlatform
	registry      *supervisor.Registry
	routingEngine *routing.Engine
	auditStore    *observability.Store
	toolHandler   *ToolHandler
	lifecycle     *supervisor.LifecycleManager
	startupStore  *app.StartupSnapshotStore
	servicesPath  string
	appRoot       string
}

// NewAdminHandler creates a new AdminHandler with required dependencies.
func NewAdminHandler(
	authService *app.AuthService,
	hubPlatform *app.HubPlatform,
	registry *supervisor.Registry,
	routingEngine *routing.Engine,
	auditStore *observability.Store,
	toolHandler *ToolHandler,
	lifecycle *supervisor.LifecycleManager,
	startupStore *app.StartupSnapshotStore,
	servicesPath string,
	appRoot string,
) *AdminHandler {
	h := &AdminHandler{
		authService:   authService,
		hubPlatform:   hubPlatform,
		registry:      registry,
		routingEngine: routingEngine,
		auditStore:    auditStore,
		toolHandler:   toolHandler,
		lifecycle:     lifecycle,
		startupStore:  startupStore,
		servicesPath:  servicesPath,
		appRoot:       appRoot,
	}
	h.RegisterTools(toolHandler)
	return h
}

func (h *AdminHandler) RegisterTools(th *ToolHandler) {
	if th == nil {
		return
	}
	th.RegisterTool("hub.admin.services.list", h.handleServicesTool)
	th.RegisterTool("hub.admin.routes.get", h.handleRoutesTool)
	th.RegisterTool("hub.admin.routes.bind", h.handleBindTool)
	th.RegisterTool("hub.admin.audits.list", h.handleAuditsTool)
	th.RegisterTool("hub.admin.tool.probe", h.handleToolProbeTool)
	th.RegisterTool("hub.admin.service.get", h.handleServiceGetTool)
	th.RegisterTool("hub.admin.service.start", h.handleServiceStartTool)
	th.RegisterTool("hub.admin.service.stop", h.handleServiceStopTool)
	th.RegisterTool("hub.admin.service.restart", h.handleServiceRestartTool)
	th.RegisterTool("hub.admin.service.drain", h.handleServiceDrainTool)
	th.RegisterTool("hub.admin.service.rebind", h.handleServiceRebindTool)
	th.RegisterTool("hub.admin.service.disable", h.handleServiceDisableTool)
	th.RegisterTool("hub.admin.service.enable", h.handleServiceEnableTool)
	th.RegisterTool("hub.admin.service.governance.update", h.handleServiceGovernanceUpdateTool)
	th.RegisterTool("hub.admin.service.manifest.get", h.handleServiceManifestGetTool)
	th.RegisterTool("hub.admin.service.manifest.update", h.handleServiceManifestUpdateTool)
	th.RegisterTool("hub.admin.service.config.get", h.handleServiceConfigGetTool)
	th.RegisterTool("hub.admin.service.config.update", h.handleServiceConfigUpdateTool)
	th.RegisterTool("hub.admin.service.files.list", h.handleServiceFilesListTool)
	th.RegisterTool("hub.admin.service.files.read", h.handleServiceFilesReadTool)
	th.RegisterTool("hub.admin.service.files.write", h.handleServiceFilesWriteTool)
	th.RegisterTool("hub.admin.service.build", h.handleServiceBuildTool)
	th.RegisterTool("hub.admin.service.generate", h.handleServiceGenerateTool)
}

func (h *AdminHandler) checkAuth(ctx context.Context) error {
	identity := app.IdentityFromContext(ctx)
	if identity.Type != app.IdentityUser {
		return fmt.Errorf("admin access restricted to users")
	}
	// TODO: Check if user has admin role if roles are implemented
	return nil
}

func (h *AdminHandler) handleServicesTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	managed := make([]supervisor.ManagedServiceInfo, 0, 16)
	if h.lifecycle != nil {
		managed = h.lifecycle.ListManagedServices()
	}
	registered := h.hubPlatform.ListServices()
	tools := h.hubPlatform.ListTools()

	// Unify managed and registered services into a single list
	managedIDs := make(map[string]struct{})
	for _, m := range managed {
		managedIDs[m.ServiceID] = struct{}{}
	}

	for _, r := range registered {
		if _, ok := managedIDs[r.ServiceID]; !ok {
			// Registered but not managed by this Hub's lifecycle
			managed = append(managed, supervisor.ManagedServiceInfo{
				ServiceID:          r.ServiceID,
				Registered:         true,
				Active:             strings.TrimSpace(r.Status) == app.ServiceStatusActive,
				Healthy:            r.Healthy,
				Status:             strings.TrimSpace(r.Status),
				InstanceID:         r.InstanceID,
				Endpoint:           r.Endpoint,
				PID:                r.PID,
				Reliability:        r.Reliability,
				RegisteredManifest: &r.Manifest,
			})
		}
	}

	startupByService := h.loadLatestStartupByService()
	for i := range managed {
		if startup, ok := startupByService[managed[i].ServiceID]; ok {
			snapshot := startup
			managed[i].Startup = &snapshot
		}
	}

	// 确保 Hub 自身也在列表中显示其声明的描述
	hubManifestPath := filepath.Join(h.appRoot, "hub", "run", "manifest.json")
	hubInfo := supervisor.ManagedServiceInfo{
		ServiceID:   "hub",
		Description: "Kagent 核心枢纽：服务编排、工具降临、路由治理。",
		Registered:  true,
		Active:      true,
		Healthy:     true,
		Status:      "active",
		Reliability: "trusted",
		InstanceID:  "builtin-hub",
		Endpoint:    "internal",
	}
	if data, err := os.ReadFile(hubManifestPath); err == nil {
		var rm supervisor.StartupManifest
		if json.Unmarshal(data, &rm) == nil {
			if rm.Description != "" {
				hubInfo.Description = rm.Description
			}
			hubInfo.RegisteredManifest = &app.ServiceManifest{
				ServiceID:   rm.ServiceID,
				ServiceName: "Kagent Hub",
				Version:     rm.Version,
			}
		}
	}
	// 将 Hub 置于列表头部或尾部，这里直接追加
	foundHub := false
	for i, m := range managed {
		if m.ServiceID == "hub" {
			managed[i].Description = hubInfo.Description
			managed[i].RegisteredManifest = hubInfo.RegisteredManifest
			foundHub = true
			break
		}
	}
	if !foundHub {
		managed = append(managed, hubInfo)
	}

	services := make([]adminServiceListItem, 0, len(managed))
	for _, item := range managed {
		services = append(services, flattenServiceListItem(item))
	}

	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"active_provider": "tool-routing-engine-v1",
			"services":        services,
			"tools":           tools,
			"app_root":        h.appRoot,
		},
	}, nil
}

func flattenServiceListItem(info supervisor.ManagedServiceInfo) adminServiceListItem {
	return adminServiceListItem{
		ServiceID:           strings.TrimSpace(info.ServiceID),
		Description:         strings.TrimSpace(info.Description),
		Dir:                 strings.TrimSpace(info.Dir),
		Enabled:             info.Enabled,
		Reliability:         strings.TrimSpace(info.Reliability),
		DirAbs:              strings.TrimSpace(info.DirAbs),
		ExecPath:            strings.TrimSpace(info.ExecPath),
		StartupManifestPath: strings.TrimSpace(info.StartupManifestPath),
		RuntimeManifestPath: strings.TrimSpace(info.RuntimeManifestPath),
		ConfigPath:          strings.TrimSpace(info.ConfigPath),
		HasSourceConfig:     info.HasSourceConfig,
		HasStartupManifest:  info.HasStartupManifest,
		HasRuntimeManifest:  info.HasRuntimeManifest,
		HasExec:             info.HasExec,
		HasGoMod:            info.HasGoMod,
		Registered:          info.Registered,
		Active:              info.Active,
		Healthy:             info.Healthy,
		Status:              strings.TrimSpace(info.Status),
		InstanceID:          strings.TrimSpace(info.InstanceID),
		Endpoint:            strings.TrimSpace(info.Endpoint),
		PID:                 info.PID,
		RegisteredManifest:  info.RegisteredManifest,
		Startup:             info.Startup,
	}
}

func (h *AdminHandler) loadLatestStartupByService() map[string]supervisor.ManagedServiceStartup {
	if h == nil || h.startupStore == nil {
		return nil
	}
	record, ok, err := h.startupStore.LoadLatest()
	if err != nil || !ok || len(record.PayloadJSON) == 0 {
		return nil
	}
	var snapshot supervisor.StartupSnapshot
	if err := json.Unmarshal(record.PayloadJSON, &snapshot); err != nil {
		return nil
	}
	result := make(map[string]supervisor.ManagedServiceStartup, len(snapshot.Services))
	for _, item := range snapshot.Services {
		sid := strings.TrimSpace(item.ServiceID)
		if sid == "" {
			continue
		}
		result[sid] = supervisor.ManagedServiceStartup{
			StartedAtMS:   snapshot.StartedAtMS,
			CompletedAtMS: snapshot.CompletedAtMS,
			Ready:         item.Ready,
			Registered:    item.Registered,
			Status:        strings.TrimSpace(item.Status),
			Attempts:      item.Attempts,
			PID:           item.PID,
			InstanceID:    strings.TrimSpace(item.Instance),
			Endpoint:      strings.TrimSpace(item.Endpoint),
			ErrorText:     strings.TrimSpace(item.ErrorText),
		}
	}
	return result
}

func (h *AdminHandler) handleRoutesTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	auditLimit := 200
	if raw, ok := req.Args["audit_limit"].(float64); ok && raw > 0 {
		auditLimit = int(raw)
	} else if raw, ok := req.Args["audit_limit"].(int); ok && raw > 0 {
		auditLimit = raw
	}
	services := h.hubPlatform.ListRegisteredServices()
	instances := h.registry.List()
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"bindings":       h.hubPlatform.ListBindings(),
			"routing_schema": h.routingEngine.BuildMetadataSchema(services, instances, auditLimit),
		},
	}, nil
}

func (h *AdminHandler) handleBindTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	toolID, _ := req.Args["tool_id"].(string)
	serviceID, _ := req.Args["service_id"].(string)
	if toolID == "" || serviceID == "" {
		return toolproto.CallResponse{}, fmt.Errorf("tool_id and service_id are required")
	}
	if err := h.hubPlatform.SetManualBinding(toolID, serviceID); err != nil {
		return toolproto.CallResponse{}, err
	}
	h.routingEngine.SetManualBinding(toolID, serviceID)
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	h.auditStore.Add("routing", "manual_bind", "ok", map[string]any{
		"tool_id":    toolID,
		"service_id": serviceID,
	})
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"ok":       true,
			"bindings": h.hubPlatform.ListBindings(),
		},
	}, nil
}

func (h *AdminHandler) handleAuditsTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	limit := 100
	if raw, ok := req.Args["limit"].(float64); ok && raw > 0 {
		limit = int(raw)
	} else if raw, ok := req.Args["limit"].(int); ok && raw > 0 {
		limit = raw
	}
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"tool_call_audits": h.routingEngine.ListAudits(limit),
			"events":           h.auditStore.List(limit),
		},
	}, nil
}

func (h *AdminHandler) handleToolProbeTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, _ := req.Args["service_id"].(string)
	toolID, _ := req.Args["tool_id"].(string)
	args, _ := req.Args["args"].(map[string]any)
	timeoutMS := 0
	if raw, ok := req.Args["timeout_ms"].(float64); ok {
		timeoutMS = int(raw)
	} else if raw, ok := req.Args["timeout_ms"].(int); ok {
		timeoutMS = raw
	}

	if serviceID == "" || toolID == "" {
		return toolproto.CallResponse{}, fmt.Errorf("service_id and tool_id are required")
	}

	callResp, _, err := h.toolHandler.ProbeServiceTool(ctx, serviceID, toolID, args, timeoutMS)
	return callResp, err
}
func (h *AdminHandler) UpdateLifecycleManager(lm *supervisor.LifecycleManager) {
	h.lifecycle = lm
}
