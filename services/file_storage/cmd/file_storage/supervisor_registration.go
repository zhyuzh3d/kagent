package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/file_storage/internal/app"
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
		allowed := append([]string(nil), p.AllowedCallerTypes...)
		if len(allowed) == 0 {
			allowed = append(allowed, p.ScopeSupport...)
		}
		tools = append(tools, app.AIServiceToolDescriptor{
			Name:               p.ToolID,
			Description:        p.Description,
			InputSchema:        p.InputSchema,
			OutputSchema:       p.OutputSchema,
			SideEffect:         p.SideEffect,
			AllowedCallerTypes: allowed,
			TimeoutMSDefault:   p.TimeoutMSDefault,
			Streaming:          p.Streaming,
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
		allowed := append([]string(nil), descriptor.AllowedCallerTypes...)
		if len(allowed) == 0 {
			allowed = append(allowed, descriptor.ScopeSupport...)
		}
		tools = append(tools, toolproto.ServiceTool{
			ToolID:               toolID,
			Description:          strings.TrimSpace(descriptor.Description),
			Protocol:             "http",
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			TimeoutMSDefault:     descriptor.TimeoutMSDefault,
			InputSchema:          descriptor.InputSchema,
			OutputSchema:         descriptor.OutputSchema,
			ScopeSupport:         append([]string(nil), descriptor.ScopeSupport...),
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
			AllowedCallerTypes:   allowed,
		})
	}
	return tools
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, callReq toolproto.CallRequest) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(nil, hubToolCallURL, serviceAuth, callReq)
}
func buildHubToolCallURL(registerURL string) string {
	return hubsvc.BuildHubToolCallURL(registerURL)
}

func startHubToolHeartbeatGuard(hubToolCallURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  strings.TrimSpace(serviceID),
					"instance_id": strings.TrimSpace(instanceID),
					"status":      "ready",
					"healthy":     true,
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

func registerFileStorageServiceToHub(hubToolCallURL string, manifest app.ServiceManifest, instance string, addr string, runtimeManifestPath string, serviceSecretPath string, serviceBootstrap hubsvc.BootstrapSecret, shutdownNow func(string)) bool {
	healthy := true
	registerCall := toolproto.CallRequest{
		ToolID: "hub.governance.service.register",
		Args: map[string]any{
			"service_id":  strings.TrimSpace(manifest.ServiceID),
			"instance_id": strings.TrimSpace(instance),
			"version":     strings.TrimSpace(manifest.Version),
			"transport":   "tcp",
			"endpoint": map[string]any{
				"tcp_url": "http://" + strings.TrimSpace(addr),
			},
			"tools":   toSupervisorTools(manifest),
			"healthy": healthy,
		},
		Context: &toolproto.Context{
			RequestID: "reg-" + app.NewRequestID(),
			TraceID:   "tr-" + app.NewRequestID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: manifest.ServiceID,
			},
		},
	}
	rawResp, statusCode, err := postHubToolCall(hubToolCallURL, serviceBootstrap, registerCall)
	if err != nil {
		app.Errorf("register file_storage service to hub failed: %v", err)
		shutdownNow("register to hub failed")
		return false
	}
	if statusCode >= 300 {
		app.Errorf("register file_storage service to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
		shutdownNow("register to hub failed")
		return false
	}
	registerResp, err := hubsvc.DecodeSupervisorRegisterResult(rawResp)
	if err != nil {
		app.Errorf("decode register response failed: %v", err)
		shutdownNow("register to hub failed")
		return false
	}
	if err := hubsvc.WriteServiceRuntimeManifest(runtimeManifestPath, registerResp); err != nil {
		app.Errorf("write runtime manifest failed: %v", err)
		shutdownNow("register to hub failed")
		return false
	}
	if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
		app.Warnf("delete bootstrap secret failed: %v", err)
	}
	app.Infof("register file_storage service to hub status=%d", statusCode)
	startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(addr), serviceBootstrap, shutdownNow)
	return true
}
