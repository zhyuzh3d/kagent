package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/chat_server/internal/app"
)

func toSupervisorTools(manifest app.ServiceManifest) []app.ServiceTool {
	tools := make([]app.ServiceTool, 0, len(manifest.Provides))
	for _, descriptor := range manifest.Provides {
		toolID := strings.TrimSpace(descriptor.ToolID)
		if toolID == "" {
			continue
		}
		tools = append(tools, app.ServiceTool{
			ToolID:      toolID,
			Description: strings.TrimSpace(descriptor.Description),
			Protocol: firstNonEmpty(strings.TrimSpace(descriptor.Protocol), func() string {
				if descriptor.Streaming {
					return "ws"
				}
				return "http"
			}()),
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            descriptor.Streaming,
			StreamingMode:        strings.TrimSpace(descriptor.StreamingMode),
			WSPath:               strings.TrimSpace(descriptor.WSPath),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			TimeoutMSDefault:     descriptor.TimeoutMSDefault,
			InputSchema:          descriptor.InputSchema,
			OutputSchema:         descriptor.OutputSchema,
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
			AllowedCallerTypes:   append([]string(nil), descriptor.AllowedCallerTypes...),
		})
	}
	return tools
}

func buildHubToolCallURL(registerURL string) string {
	return hubsvc.BuildHubToolCallURL(registerURL)
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(&http.Client{Timeout: 3 * time.Second}, hubToolCallURL, serviceAuth, req)
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

func registerChatServerToHub(hubToolCallURL string, manifest app.ServiceManifest, instance string, addr string, runtimeManifestPath string, serviceSecretPath string, serviceBootstrap hubsvc.BootstrapSecret, shutdownNow func(string)) bool {
	healthy := true
	registerPayload := app.SupervisorRegisterRequest{
		ServiceID:  strings.TrimSpace(manifest.ServiceID),
		InstanceID: strings.TrimSpace(instance),
		Version:    strings.TrimSpace(manifest.Version),
		Transport:  "tcp",
		Endpoint: app.Endpoint{
			TCPURL: "http://" + strings.TrimSpace(addr),
		},
		Tools:   toSupervisorTools(manifest),
		Healthy: &healthy,
	}
	callReq := toolproto.CallRequest{
		ToolID: "hub.governance.service.register",
		Args:   structToMap(registerPayload),
		Context: &toolproto.Context{
			RequestID: "reg-" + app.NewRequestID(),
			TraceID:   "tr-" + app.NewRequestID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: strings.TrimSpace(manifest.ServiceID),
			},
		},
	}
	rawResp, statusCode, err := postHubToolCall(hubToolCallURL, serviceBootstrap, callReq)
	if err != nil {
		app.Errorf("register chat_server to hub failed: %v", err)
		shutdownNow("register to hub failed")
		return false
	}
	if statusCode >= 300 {
		app.Errorf("register chat_server to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
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
	app.Infof("register chat_server to hub status=%d", statusCode)
	startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(addr), serviceBootstrap, shutdownNow)
	return true
}
