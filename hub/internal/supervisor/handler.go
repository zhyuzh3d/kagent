package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/observability"
	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

// ServiceSyncer defines the interface for syncing services with a routing engine.
type ServiceSyncer interface {
	SyncServices(services []app.HubServiceRegistration)
}

// SupervisorHandler handles /api/service/* endpoints.
type SupervisorHandler struct {
	hubPlatform    *app.HubPlatform
	registry       *Registry
	routingEngine  ServiceSyncer
	auditStore     *observability.Store
	onServiceReady func(serviceID string)
}

// NewSupervisorHandler creates a new SupervisorHandler with required dependencies.
func NewSupervisorHandler(
	hubPlatform *app.HubPlatform,
	registry *Registry,
	routingEngine ServiceSyncer,
	auditStore *observability.Store,
) *SupervisorHandler {
	return &SupervisorHandler{
		hubPlatform:   hubPlatform,
		registry:      registry,
		routingEngine: routingEngine,
		auditStore:    auditStore,
	}
}

func (h *SupervisorHandler) RegisterTools(th interface {
	RegisterTool(toolID string, fn func(context.Context, toolproto.CallRequest) (toolproto.CallResponse, error))
}) {
	if th == nil {
		return
	}
	th.RegisterTool("hub.governance.service.register", h.handleRegisterTool)
	th.RegisterTool("hub.governance.service.heartbeat", h.handleHeartbeatTool)
	th.RegisterTool("hub.governance.service.drain", h.handleDrainTool)
}

func (h *SupervisorHandler) handleRegisterTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	regReq := parseRegisterRequest(req.Args)
	if regReq.ServiceID == "" {
		return toolproto.CallResponse{}, fmt.Errorf("invalid register args: service_id required")
	}

	appReq, transportName, err := decodeInternalRegister(regReq)
	if err != nil {
		return toolproto.CallResponse{}, err
	}

	identity := app.IdentityFromContext(ctx)
	if identity.Type != app.IdentityService && identity.Type != app.IdentityAnonymous {
		return toolproto.CallResponse{}, fmt.Errorf("governance restricted to services")
	}

	remoteAddr, _ := ctx.Value(app.RemoteAddrContextKey).(string)
	if !app.IsLoopbackRemoteAddr(remoteAddr) {
		return toolproto.CallResponse{}, fmt.Errorf("governance restricted to loopback")
	}

	// Internal security check: loopback only for registration via hub internal tool if it's from localhost
	// But wait, if it's via tool call, it's already reached the Hub.
	// We should still verify service auth if possible.
	if identity.Type == app.IdentityService {
		if strings.TrimSpace(identity.ID) != strings.TrimSpace(appReq.Manifest.ServiceID) {
			return toolproto.CallResponse{}, fmt.Errorf("service_id mismatch in auth")
		}
	}

	if sid := strings.TrimSpace(appReq.Manifest.ServiceID); sid != "" {
		prev, _, err := h.ensureServiceStoppedForRegister(sid, appReq.InstanceID, 7*time.Second)
		if err != nil {
			return toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeConflict,
					Message: "pre-registration cleanup failed: " + err.Error(),
				},
				Meta: toolproto.Meta{
					ServiceID:  sid,
					InstanceID: prev.InstanceID,
				},
			}, nil
		}
	}

	res, err := h.hubPlatform.RegisterService(appReq)
	if err != nil {
		h.auditStore.Add("supervisor", "register", "error", map[string]any{
			"service_id":  strings.TrimSpace(appReq.Manifest.ServiceID),
			"instance_id": strings.TrimSpace(appReq.InstanceID),
			"error":       err.Error(),
		})
		return toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeConflict,
				Message: err.Error(),
			},
		}, nil
	}

	h.registry.UpsertFromServiceRegistration(res.Service, transportName, instanceStatusFromHealth(res.Service.Healthy))
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	app.Infof("System:Internal:RegistrationSuccess:service=%s,endpoint=%s", res.Service.ServiceID, res.Service.Endpoint)
	h.auditStore.Add("supervisor", "register", "ok", map[string]any{
		"service_id":  res.Service.ServiceID,
		"instance_id": res.Service.InstanceID,
		"endpoint":    res.Service.Endpoint,
	})
	if h.onServiceReady != nil {
		go h.onServiceReady(strings.TrimSpace(res.Service.ServiceID))
	}
	return toolproto.CallResponse{
		Ok: true,
		Result: toolproto.SupervisorRegisterResult{
			HeartbeatIntervalSec:           3,
			InverseHeartbeatIntervalSec:    3,
			InverseHeartbeatFailuresToExit: 2,
			DrainGracePeriodSec:            30,
		},
		Meta: toolproto.Meta{
			ServiceID:  res.Service.ServiceID,
			InstanceID: res.Service.InstanceID,
		},
	}, nil
}

func (h *SupervisorHandler) handleHeartbeatTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	identity := app.IdentityFromContext(ctx)
	if identity.Type != app.IdentityService {
		return toolproto.CallResponse{}, fmt.Errorf("heartbeat restricted to services")
	}

	remoteAddr, _ := ctx.Value(app.RemoteAddrContextKey).(string)
	if !app.IsLoopbackRemoteAddr(remoteAddr) {
		return toolproto.CallResponse{}, fmt.Errorf("heartbeat restricted to loopback")
	}

	hbReq := parseHeartbeatRequest(req.Args)
	if hbReq.ServiceID == "" {
		return toolproto.CallResponse{}, fmt.Errorf("invalid heartbeat args: service_id required")
	}

	if strings.TrimSpace(identity.ID) != strings.TrimSpace(hbReq.ServiceID) {
		return toolproto.CallResponse{}, fmt.Errorf("service_id mismatch in auth")
	}

	reg, err := h.hubPlatform.AcceptServiceHeartbeat(app.HubServiceHeartbeatRequest{
		ServiceID:  strings.TrimSpace(hbReq.ServiceID),
		InstanceID: strings.TrimSpace(hbReq.InstanceID),
		PID:        hbReq.PID,
		Endpoint:   strings.TrimSpace(hbReq.Endpoint),
		Healthy:    hbReq.Healthy,
	})
	if err != nil {
		h.auditStore.Add("supervisor", "heartbeat", "error", map[string]any{
			"service_id":  strings.TrimSpace(hbReq.ServiceID),
			"instance_id": strings.TrimSpace(hbReq.InstanceID),
			"error":       err.Error(),
		})
		return toolproto.CallResponse{
			Ok: false,
			Error: &toolproto.Error{
				Code:    toolproto.ErrorCodeConflict,
				Message: "heartbeat rejected: " + err.Error(),
			},
		}, nil
	}

	h.registry.Heartbeat(hbReq.ServiceID, hbReq.InstanceID, hbReq.Status, hbReq.Healthy)
	h.auditStore.Add("supervisor", "heartbeat", "ok", map[string]any{
		"service_id":  reg.ServiceID,
		"instance_id": reg.InstanceID,
		"status":      reg.Status,
	})
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"status":       reg.Status,
			"last_seen_ms": reg.LastSeenAtMS,
		},
		Meta: toolproto.Meta{
			ServiceID:  reg.ServiceID,
			InstanceID: reg.InstanceID,
		},
	}, nil
}

func (h *SupervisorHandler) handleDrainTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	identity := app.IdentityFromContext(ctx)
	// Drain can be called by user (admin) or the service itself
	if identity.Type != app.IdentityUser && identity.Type != app.IdentityService {
		return toolproto.CallResponse{}, fmt.Errorf("drain restricted to users and services")
	}

	remoteAddr, _ := ctx.Value(app.RemoteAddrContextKey).(string)
	if identity.Type == app.IdentityService && !app.IsLoopbackRemoteAddr(remoteAddr) {
		return toolproto.CallResponse{}, fmt.Errorf("service drain restricted to loopback")
	}

	serviceID, _ := req.Args["service_id"].(string)
	instanceID, _ := req.Args["instance_id"].(string)
	reason, _ := req.Args["reason"].(string)

	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return toolproto.CallResponse{}, fmt.Errorf("service_id is required")
	}

	if identity.Type == app.IdentityService && strings.TrimSpace(identity.ID) != sid {
		return toolproto.CallResponse{}, fmt.Errorf("service can only drain itself")
	}

	if reason == "" {
		reason = "drain requested via tool"
	}

	h.hubPlatform.MarkServiceDown(sid, reason)
	h.registry.MarkDraining(sid, strings.TrimSpace(instanceID))
	h.routingEngine.SyncServices(h.hubPlatform.ListRegisteredServices())
	h.auditStore.Add("supervisor", "drain", "ok", map[string]any{
		"service_id":  sid,
		"instance_id": strings.TrimSpace(instanceID),
		"reason":      reason,
	})
	return toolproto.CallResponse{
		Ok: true,
		Result: map[string]any{
			"draining": true,
		},
		Meta: toolproto.Meta{
			ServiceID: sid,
		},
	}, nil
}

func (h *SupervisorHandler) SetOnServiceReady(callback func(serviceID string)) {
	if h == nil {
		return
	}
	h.onServiceReady = callback
}

// --- Helper functions (extracted from main.go) ---

func decodeInternalRegister(req toolproto.SupervisorRegisterRequest) (app.HubServiceRegisterRequest, string, error) {
	serviceID := strings.TrimSpace(req.ServiceID)
	if serviceID == "" {
		return app.HubServiceRegisterRequest{}, "", fmt.Errorf("service_id is required")
	}
	tools := make([]app.ServiceToolDescriptor, 0, len(req.Tools))
	for _, t := range req.Tools {
		toolID := strings.TrimSpace(t.ToolID)
		if toolID == "" {
			continue
		}
		category := ""
		typ := ""
		tool := ""
		parts := strings.Split(toolID, ".")
		if len(parts) >= 3 {
			category = parts[0]
			typ = parts[1]
			tool = strings.Join(parts[2:], ".")
		}
		streaming := ""
		if t.Streaming {
			streaming = "stream"
		}
		tools = append(tools, app.ServiceToolDescriptor{
			ToolID:               toolID,
			Category:             category,
			Type:                 typ,
			Tool:                 tool,
			Description:          strings.TrimSpace(t.Description),
			InputSchema:          t.InputSchema,
			OutputSchema:         t.OutputSchema,
			CapabilitiesRequired: t.CapabilitiesRequired,
			AllowedCallerTypes:   t.AllowedCallerTypes,
			TimeoutMSDefault:     t.TimeoutMS,
			Streaming:            streaming,
			WSPath:               strings.TrimSpace(t.WSPath),
		})
	}

	transportName := strings.ToLower(strings.TrimSpace(req.Transport))
	if transportName == "" {
		switch {
		case strings.TrimSpace(req.Endpoint.UDSPath) != "":
			transportName = "uds"
		case strings.TrimSpace(req.Endpoint.TCPURL) != "":
			transportName = "tcp"
		default:
			transportName = "tcp"
		}
	}

	endpoint := strings.TrimSpace(req.Endpoint.TCPURL)
	if transportName == "uds" {
		endpoint = strings.TrimSpace(req.Endpoint.UDSPath)
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(req.Endpoint.TCPURL)
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(req.Endpoint.UDSPath)
	}
	if endpoint == "" {
		endpoint = serviceID
	}

	return app.HubServiceRegisterRequest{
		Manifest: app.ServiceManifest{
			ServiceID:   serviceID,
			ServiceName: serviceID,
			Version:     strings.TrimSpace(req.Version),
			Reliability: "untrusted",
			Visibility:  "internal",
			Provides:    tools,
		},
		InstanceID: strings.TrimSpace(req.InstanceID),
		PID:        req.PID,
		Endpoint:   endpoint,
		Healthy:    req.Healthy,
	}, transportName, nil
}

func verifyServiceInternalAuth(hubPlatform *app.HubPlatform, r *http.Request, expectedServiceID string, expectedInstanceID string) (string, string, error) {
	if hubPlatform == nil {
		return "", "", fmt.Errorf("hub platform is nil")
	}
	serviceID, instanceID, serviceAuth := hubsvc.ExtractServiceAuthHeaders(r.Header)
	if serviceID == "" || instanceID == "" || serviceAuth == "" {
		return "", "", fmt.Errorf("missing service auth headers")
	}
	if sid := strings.TrimSpace(expectedServiceID); sid != "" && sid != serviceID {
		return "", "", fmt.Errorf("service auth service_id mismatch")
	}
	if iid := strings.TrimSpace(expectedInstanceID); iid != "" && iid != instanceID {
		return "", "", fmt.Errorf("service auth instance_id mismatch")
	}
	verified, err := hubPlatform.VerifyServiceAuth(serviceID, instanceID, serviceAuth)
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(verified.ServiceID), strings.TrimSpace(verified.InstanceID), nil
}

func instanceStatusFromHealth(healthy bool) string {
	if healthy {
		return InstanceStatusRegistered
	}
	return InstanceStatusUnhealthy
}

func (h *SupervisorHandler) ensureServiceStoppedForRegister(serviceID string, nextInstanceID string, timeout time.Duration) (app.HubServiceRegistration, bool, error) {
	if h.hubPlatform == nil {
		return app.HubServiceRegistration{}, false, fmt.Errorf("hub platform is nil")
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return app.HubServiceRegistration{}, false, nil
	}
	existing, ok := h.hubPlatform.GetService(sid)
	if !ok {
		return app.HubServiceRegistration{}, false, nil
	}
	if iid := strings.TrimSpace(nextInstanceID); iid != "" && iid == strings.TrimSpace(existing.InstanceID) {
		return existing, true, nil
	}
	if err := StopServiceRegistration(h.hubPlatform, existing, timeout); err != nil {
		return existing, true, err
	}
	h.hubPlatform.UnregisterService(existing.ServiceID, existing.InstanceID)
	if h.registry != nil {
		h.registry.Unregister(existing.ServiceID, existing.InstanceID)
	}
	return existing, true, nil
}

// --- Efficient Map Parsers (To avoid JSON re-serialization overhead) ---

func parseRegisterRequest(args map[string]any) toolproto.SupervisorRegisterRequest {
	if args == nil {
		return toolproto.SupervisorRegisterRequest{}
	}
	var out toolproto.SupervisorRegisterRequest
	out.ServiceID = asStr(args["service_id"])
	out.InstanceID = asStr(args["instance_id"])
	out.PID = asIntVal(args["pid"])
	out.Version = asStr(args["version"])
	out.Transport = asStr(args["transport"])

	if ep, ok := args["endpoint"].(map[string]any); ok {
		out.Endpoint.UDSPath = asStr(ep["uds_path"])
		out.Endpoint.TCPURL = asStr(ep["tcp_url"])
	}

	if tools, ok := args["tools"].([]any); ok {
		out.Tools = make([]toolproto.ServiceTool, 0, len(tools))
		for _, v := range tools {
			if tm, ok := v.(map[string]any); ok {
				var st toolproto.ServiceTool
				st.ToolID = asStr(tm["tool_id"])
				st.Version = asStr(tm["version"])
				st.Description = asStr(tm["description"])
				if is, ok := tm["input_schema"].(map[string]any); ok {
					st.InputSchema = is
				}
				if os, ok := tm["output_schema"].(map[string]any); ok {
					st.OutputSchema = os
				}
				st.Streaming, _ = tm["streaming"].(bool)
				st.WSPath = asStr(tm["ws_path"])
				st.TimeoutMS = asIntVal(tm["timeout_ms"])
				if cr, ok := tm["capabilities_required"].([]any); ok {
					for _, c := range cr {
						if cs, ok := c.(string); ok {
							st.CapabilitiesRequired = append(st.CapabilitiesRequired, cs)
						}
					}
				}
				if act, ok := tm["allowed_caller_types"].([]any); ok {
					for _, a := range act {
						if as, ok := a.(string); ok {
							st.AllowedCallerTypes = append(st.AllowedCallerTypes, as)
						}
					}
				}
				out.Tools = append(out.Tools, st)
			}
		}
	}

	if hVal, ok := args["healthy"]; ok {
		if hb, ok := hVal.(bool); ok {
			out.Healthy = &hb
		}
	}
	return out
}

func parseHeartbeatRequest(args map[string]any) toolproto.SupervisorHeartbeatRequest {
	if args == nil {
		return toolproto.SupervisorHeartbeatRequest{}
	}
	var out toolproto.SupervisorHeartbeatRequest
	out.ServiceID = asStr(args["service_id"])
	out.InstanceID = asStr(args["instance_id"])
	out.Status = asStr(args["status"])
	out.PID = asIntVal(args["pid"])
	out.Endpoint = asStr(args["endpoint"])

	if hVal, ok := args["healthy"]; ok {
		if hb, ok := hVal.(bool); ok {
			out.Healthy = &hb
		}
	}
	return out
}

func asStr(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asIntVal(v any) int {
	switch tv := v.(type) {
	case int:
		return tv
	case float64:
		return int(tv)
	case int64:
		return int(tv)
	default:
		return 0
	}
}
