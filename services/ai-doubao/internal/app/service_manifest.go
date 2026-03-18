package app

import "strings"

type ServiceToolDescriptor struct {
	ToolID               string         `json:"tool_id"`
	Category             string         `json:"category"`
	Type                 string         `json:"type"`
	Tool                 string         `json:"tool"`
	Description          string         `json:"description"`
	InputSchema          map[string]any `json:"input_schema,omitempty"`
	OutputSchema         map[string]any `json:"output_schema,omitempty"`
	SideEffect           string         `json:"side_effect,omitempty"`
	CapabilitiesRequired []string       `json:"capabilities_required,omitempty"`
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	Streaming            string         `json:"streaming,omitempty"`
	WSPath               string         `json:"ws_path,omitempty"`
	ScopeSupport         []string       `json:"scope_support,omitempty"`
}

type ServiceManifest struct {
	ServiceID   string                  `json:"service_id"`
	ServiceName string                  `json:"service_name"`
	Version     string                  `json:"version,omitempty"`
	Reliability string                  `json:"reliability,omitempty"`
	Visibility  string                  `json:"visibility,omitempty"`
	Provides    []ServiceToolDescriptor `json:"provides,omitempty"`
	Requires    []string                `json:"requires,omitempty"`
}

func BuildAIServiceManifest(info *AIServiceInfo, tools []AIServiceToolDescriptor, healthy bool) ServiceManifest {
	serviceID := "ai-doubao"
	serviceName := "AI Doubao"
	version := "1.0.0"
	if info != nil {
		serviceID = firstNonEmpty(strings.TrimSpace(info.ServiceID), serviceID)
		serviceName = firstNonEmpty(strings.TrimSpace(info.ServiceName), serviceName)
		version = firstNonEmpty(strings.TrimSpace(info.Version), version)
	}
	provides := make([]ServiceToolDescriptor, 0, len(tools))
	for _, t := range tools {
		toolID := strings.TrimSpace(t.Name)
		if toolID == "" {
			continue
		}
		td := ServiceToolDescriptor{
			ToolID:               toolID,
			Description:          strings.TrimSpace(t.Description),
			InputSchema:          cloneAnyMap(t.InputSchema),
			OutputSchema:         cloneAnyMap(t.OutputSchema),
			SideEffect:           strings.TrimSpace(t.SideEffect),
			CapabilitiesRequired: uniqueNonEmpty(t.CapabilitiesRequired),
			TimeoutMSDefault:     t.TimeoutMSDefault,
			Streaming:            strings.TrimSpace(t.Streaming),
			WSPath:               strings.TrimSpace(t.WSPath),
		}
		parts := strings.Split(toolID, ".")
		if len(parts) >= 3 {
			td.Category = parts[0]
			td.Type = parts[1]
			td.Tool = strings.Join(parts[2:], ".")
		}
		provides = append(provides, td)
	}
	reliability := "verified"
	if !healthy {
		reliability = "unverified"
	}
	return ServiceManifest{
		ServiceID:   serviceID,
		ServiceName: serviceName,
		Version:     version,
		Reliability: reliability,
		Visibility:  "public",
		Provides:    provides,
	}
}
