package app

import (
	"strings"

	"kagent/pkg/toolproto"
)

type ServiceToolDescriptor = toolproto.ServiceTool
type ServiceManifest = toolproto.ServiceManifest

func BuildAIServiceManifest(info *AIServiceInfo, tools []AIServiceToolDescriptor) ServiceManifest {
	serviceID := "ai_doubao"
	serviceName := "ai_doubao"
	version := "1.0.0"
	if info != nil {
		serviceID = firstNonEmpty(strings.TrimSpace(info.ServiceID), serviceID)
		serviceName = firstNonEmpty(strings.TrimSpace(info.ServiceName), serviceName)
		version = firstNonEmpty(strings.TrimSpace(info.Version), version)
	}
	provides := make([]ServiceToolDescriptor, 0, len(tools)+3)
	for _, t := range tools {
		toolID := strings.TrimSpace(t.Name)
		if toolID == "" {
			continue
		}
		category, typ, tool := toolproto.SplitToolID(toolID)
		streamingMode := strings.TrimSpace(t.Streaming)
		provides = append(provides, ServiceToolDescriptor{
			ToolID:               toolID,
			Category:             category,
			Type:                 typ,
			Tool:                 tool,
			Description:          strings.TrimSpace(t.Description),
			InputSchema:          cloneAnyMap(t.InputSchema),
			OutputSchema:         cloneAnyMap(t.OutputSchema),
			SideEffect:           strings.TrimSpace(t.SideEffect),
			CapabilitiesRequired: uniqueNonEmpty(t.CapabilitiesRequired),
			AllowedCallerTypes:   uniqueNonEmpty(t.AllowedCallerTypes),
			TimeoutMSDefault:     t.TimeoutMSDefault,
			Streaming:            streamingMode != "" && !strings.EqualFold(streamingMode, "none"),
			StreamingMode:        streamingMode,
			WSPath:               strings.TrimSpace(t.WSPath),
		})
	}
	provides = append(provides,
		ServiceToolDescriptor{ToolID: "service.lifecycle.health", Category: "service", Type: "lifecycle", Tool: "health", Description: "service health probe", AllowedCallerTypes: []string{"service"}},
		ServiceToolDescriptor{ToolID: "service.lifecycle.state.get", Category: "service", Type: "lifecycle", Tool: "state.get", Description: "service lifecycle state snapshot", AllowedCallerTypes: []string{"service"}},
		ServiceToolDescriptor{ToolID: "service.lifecycle.shutdown", Category: "service", Type: "lifecycle", Tool: "shutdown", Description: "service shutdown", AllowedCallerTypes: []string{"service"}},
	)
	return toolproto.NormalizeServiceManifest(ServiceManifest{
		ServiceID:   serviceID,
		ServiceName: serviceName,
		Version:     version,
		Visibility:  "public",
		Provides:    provides,
	})
}
