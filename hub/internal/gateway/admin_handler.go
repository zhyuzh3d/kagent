package gateway

import (
	"context"
	"fmt"

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

// AdminHandler handles /api/admin/* endpoints.
type AdminHandler struct {
	authService   *app.AuthService
	hubPlatform   *app.HubPlatform
	registry      *supervisor.Registry
	routingEngine *routing.Engine
	auditStore    *observability.Store
	toolHandler   *ToolHandler
}

// NewAdminHandler creates a new AdminHandler with required dependencies.
func NewAdminHandler(
	authService *app.AuthService,
	hubPlatform *app.HubPlatform,
	registry *supervisor.Registry,
	routingEngine *routing.Engine,
	auditStore *observability.Store,
	toolHandler *ToolHandler,
) *AdminHandler {
	h := &AdminHandler{
		authService:   authService,
		hubPlatform:   hubPlatform,
		registry:      registry,
		routingEngine: routingEngine,
		auditStore:    auditStore,
		toolHandler:   toolHandler,
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
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"active_provider": "tool-routing-engine-v1",
			"services":        h.hubPlatform.ListServices(),
		},
	}, nil
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

