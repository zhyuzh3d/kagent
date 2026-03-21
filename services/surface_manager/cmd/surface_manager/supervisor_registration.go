package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/surface_manager/internal/app"
)

func builtinManifest(serviceID string) app.ServiceManifest {
	for _, m := range app.BuiltinServiceManifests() {
		if strings.TrimSpace(m.ServiceID) == strings.TrimSpace(serviceID) {
			return m
		}
	}
	return app.ServiceManifest{ServiceID: serviceID, ServiceName: serviceID, Version: "1.0.0"}
}

func manifestTools(manifest app.ServiceManifest) []app.AIServiceToolDescriptor {
	tools := make([]app.AIServiceToolDescriptor, 0, len(manifest.Provides))
	for _, p := range manifest.Provides {
		streamingMode := "none"
		if p.Streaming {
			streamingMode = "ws"
		}
		tools = append(tools, app.AIServiceToolDescriptor{
			Name:             p.ToolID,
			Description:      p.Description,
			InputSchema:      p.InputSchema,
			OutputSchema:     p.OutputSchema,
			SideEffect:       p.SideEffect,
			TimeoutMSDefault: p.TimeoutMSDefault,
			Streaming:        streamingMode,
		})
	}
	return tools
}

func toSupervisorTools(manifest app.ServiceManifest) []toolproto.ServiceTool {
	tools := make([]toolproto.ServiceTool, 0, len(manifest.Provides))
	for _, descriptor := range manifest.Provides {
		toolID := strings.TrimSpace(descriptor.ToolID)
		if toolID == "" {
			continue
		}
		protocol := strings.TrimSpace(descriptor.Protocol)
		if protocol == "" {
			protocol = "http"
		}
		tools = append(tools, toolproto.ServiceTool{
			ToolID:               toolID,
			Description:          strings.TrimSpace(descriptor.Description),
			Protocol:             protocol,
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            descriptor.Streaming,
			StreamingMode:        strings.TrimSpace(descriptor.StreamingMode),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			TimeoutMSDefault:     descriptor.TimeoutMSDefault,
			InputSchema:          descriptor.InputSchema,
			OutputSchema:         descriptor.OutputSchema,
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
			AllowedCallerTypes:   append([]string(nil), descriptor.AllowedCallerTypes...),
			WSPath:               strings.TrimSpace(descriptor.WSPath),
			ScopeSupport:         append([]string(nil), descriptor.ScopeSupport...),
		})
	}
	return tools
}

func callHubToolAsService(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, serviceID string, requestID string, traceID string, toolID string, args map[string]any) (toolproto.CallResponse, error) {
	callReq := toolproto.CallRequest{
		ToolID: strings.TrimSpace(toolID),
		Args:   args,
		Context: &toolproto.Context{
			RequestID: strings.TrimSpace(requestID),
			TraceID:   strings.TrimSpace(traceID),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: strings.TrimSpace(serviceID),
			},
		},
	}
	httpRespBody, statusCode, err := hubsvc.PostHubToolCall(&http.Client{Timeout: 8 * time.Second}, hubToolCallURL, serviceAuth, callReq)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	var callResp toolproto.CallResponse
	if err := json.Unmarshal(httpRespBody, &callResp); err != nil {
		return toolproto.CallResponse{}, fmt.Errorf("decode hub tool response failed: %w", err)
	}
	if statusCode >= 300 && callResp.Error == nil {
		return toolproto.CallResponse{}, fmt.Errorf("hub tool call status=%d", statusCode)
	}
	return callResp, nil
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(&http.Client{Timeout: 5 * time.Second}, hubToolCallURL, serviceAuth, req)
}

func buildHubToolCallURL(registerURL string) string {
	return hubsvc.BuildHubToolCallURL(registerURL)
}

func startHubToolHeartbeatGuard(hubToolCallURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string), statusFn func() string, healthyFn func() bool) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			status := "ready"
			if statusFn != nil && strings.TrimSpace(statusFn()) != "" {
				status = strings.TrimSpace(statusFn())
			}
			healthy := true
			if healthyFn != nil {
				healthy = healthyFn()
			}
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  strings.TrimSpace(serviceID),
					"instance_id": strings.TrimSpace(instanceID),
					"status":      status,
					"healthy":     healthy,
					"pid":         pid,
					"endpoint":    strings.TrimSpace(endpoint),
				},
				Context: &toolproto.Context{
					RequestID: "hb-" + app.NewRequestID(),
					TraceID:   "tr-" + app.NewRequestID(),
					Caller: toolproto.Caller{
						Type:      "service",
						ServiceID: strings.TrimSpace(serviceID),
					},
				},
			}
			rawResp, statusCode, err := postHubToolCall(hubToolCallURL, serviceAuth, callReq)
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
