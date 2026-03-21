package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/observability"
	"kagent/hub/internal/routing"
	"kagent/hub/internal/supervisor"
	"kagent/hub/internal/transport"
	"kagent/pkg/toolproto"

	"github.com/gorilla/websocket"
)

const (
	defaultToolTimeout = 30 * time.Second
	maxToolTimeout     = 120 * time.Second
)

type InternalToolFunc func(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error)
type InternalWSToolFunc func(ctx context.Context, conn *websocket.Conn, req toolproto.CallRequest) error

type ToolHandler struct {
	authService *app.AuthService
	hubPlatform *app.HubPlatform
	router      *routing.Engine
	supervisor  *supervisor.Registry
	transport   *transport.Client
	audit       *observability.Store
	endpoints   map[string]transport.Endpoint
	registry    map[string]InternalToolFunc
	wsRegistry  map[string]InternalWSToolFunc
}

func NewToolHandler(authService *app.AuthService, hubPlatform *app.HubPlatform, router *routing.Engine, supervisorRegistry *supervisor.Registry, transportClient *transport.Client, auditStore *observability.Store, defaultEndpoints map[string]transport.Endpoint) *ToolHandler {
	endpoints := map[string]transport.Endpoint{}
	for serviceID, endpoint := range defaultEndpoints {
		sid := strings.TrimSpace(serviceID)
		if sid == "" {
			continue
		}
		normalized := endpoint
		if strings.TrimSpace(normalized.Transport) == "" {
			normalized.Transport = inferTransport(normalized)
		}
		endpoints[sid] = normalized
	}
	return &ToolHandler{
		authService: authService,
		hubPlatform: hubPlatform,
		router:      router,
		supervisor:  supervisorRegistry,
		transport:   transportClient,
		audit:       auditStore,
		endpoints:   endpoints,
		registry:    map[string]InternalToolFunc{},
		wsRegistry:  map[string]InternalWSToolFunc{},
	}
}

func (h *ToolHandler) RegisterTool(toolID string, fn func(context.Context, toolproto.CallRequest) (toolproto.CallResponse, error)) {
	h.registry[toolID] = fn
}

func (h *ToolHandler) RegisterWSTool(toolID string, fn func(context.Context, *websocket.Conn, toolproto.CallRequest) error) {
	h.wsRegistry[toolID] = fn
}

func (h *ToolHandler) selectTool(toolID string) (routing.Selection, bool) {
	services := h.hubPlatform.ListRegisteredServices()
	instances := h.supervisor.List()
	h.router.SyncServices(services)
	return h.router.Select(toolID, services, instances)
}

func (h *ToolHandler) resolveEndpoint(selection routing.Selection) transport.Endpoint {
	instance := selection.Instance
	resolved := transport.Endpoint{
		Transport: strings.TrimSpace(instance.Transport),
	}
	if strings.EqualFold(resolved.Transport, "uds") {
		resolved.UDSPath = strings.TrimSpace(instance.Endpoint)
	} else {
		resolved.TCPURL = strings.TrimSpace(instance.Endpoint)
	}
	defaultEndpoint := h.endpoints[strings.TrimSpace(selection.Service.ServiceID)]
	if strings.TrimSpace(resolved.Transport) == "" {
		resolved.Transport = inferTransport(defaultEndpoint)
	}
	if strings.TrimSpace(resolved.UDSPath) == "" {
		resolved.UDSPath = strings.TrimSpace(defaultEndpoint.UDSPath)
	}
	if strings.TrimSpace(resolved.TCPURL) == "" {
		resolved.TCPURL = strings.TrimSpace(defaultEndpoint.TCPURL)
	}
	if strings.EqualFold(resolved.Transport, "uds") && strings.TrimSpace(resolved.UDSPath) == "" {
		resolved.Transport = "tcp"
	}
	if strings.TrimSpace(resolved.Transport) == "" {
		resolved.Transport = "tcp"
	}
	return resolved
}

func resolveTimeout(timeoutMS int) time.Duration {
	timeout := defaultToolTimeout
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
		if timeout > maxToolTimeout {
			timeout = maxToolTimeout
		}
	}
	return timeout
}

func inferTransport(endpoint transport.Endpoint) string {
	if strings.TrimSpace(endpoint.Transport) != "" {
		return strings.TrimSpace(endpoint.Transport)
	}
	if strings.TrimSpace(endpoint.UDSPath) != "" {
		return "uds"
	}
	return "tcp"
}

func inferTransportFromURL(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "unix://") || strings.HasPrefix(value, "uds://") {
		return "uds"
	}
	if strings.HasPrefix(value, "/") {
		return "uds"
	}
	return "tcp"
}

func mapStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

func pickReadyHealthyInstance(instances []supervisor.Instance) (supervisor.Instance, bool) {
	for _, instance := range instances {
		if strings.TrimSpace(instance.Status) != supervisor.InstanceStatusReady {
			continue
		}
		if !instance.Healthy {
			continue
		}
		return instance, true
	}
	return supervisor.Instance{}, false
}

func callerIdentity(caller toolproto.Caller) string {
	switch strings.ToLower(strings.TrimSpace(caller.Type)) {
	case "service":
		return strings.TrimSpace(caller.ServiceID)
	case "surface":
		if sid := strings.TrimSpace(caller.SurfaceID); sid != "" {
			return sid
		}
		return strings.TrimSpace(caller.UserID)
	default:
		return strings.TrimSpace(caller.UserID)
	}
}

func writeToolError(w http.ResponseWriter, statusCode int, code string, message string, requestID string, traceID string, serviceID string, instanceID string) {
	resp := toolproto.CallResponse{
		Ok:     false,
		Result: nil,
		Error: &toolproto.Error{
			Code:    strings.TrimSpace(code),
			Message: strings.TrimSpace(message),
		},
		Meta: toolproto.Meta{
			RequestID:  strings.TrimSpace(requestID),
			TraceID:    strings.TrimSpace(traceID),
			ServiceID:  strings.TrimSpace(serviceID),
			InstanceID: strings.TrimSpace(instanceID),
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}
