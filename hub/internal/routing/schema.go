package routing

import (
	"sort"
	"strings"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
)

type ToolRegistryItem struct {
	ToolID               string   `json:"tool_id"`
	Version              string   `json:"version,omitempty"`
	Streaming            bool     `json:"streaming"`
	DefaultTimeoutMS     int      `json:"default_timeout_ms"`
	CapabilitiesRequired []string `json:"capabilities_required,omitempty"`
	AllowedCallerTypes   []string `json:"allowed_caller_types,omitempty"`
	InputSchemaRef       string   `json:"input_schema_ref,omitempty"`
	OutputSchemaRef      string   `json:"output_schema_ref,omitempty"`
	WSPath               string   `json:"ws_path,omitempty"`
	OwnerServiceID       string   `json:"owner_service_id"`
	Enabled              bool     `json:"enabled"`
}

type ServiceRegistryItem struct {
	ServiceID           string   `json:"service_id"`
	Version             string   `json:"version,omitempty"`
	Enabled             bool     `json:"enabled"`
	DefaultWeight       int      `json:"default_weight"`
	TransportPreference string   `json:"transport_preference"`
	Tags                []string `json:"tags,omitempty"`
}

type InstanceRegistryItem struct {
	InstanceID          string `json:"instance_id"`
	ServiceID           string `json:"service_id"`
	Status              string `json:"status"`
	Transport           string `json:"transport"`
	Endpoint            string `json:"endpoint"`
	Score               int    `json:"score"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastSuccessAt       int64  `json:"last_success_at"`
	LastFailureAt       int64  `json:"last_failure_at"`
	LastHeartbeatAt     int64  `json:"last_heartbeat_at"`
}

type MetadataSchema struct {
	ToolRegistry     []ToolRegistryItem     `json:"tool_registry"`
	ServiceRegistry  []ServiceRegistryItem  `json:"service_registry"`
	InstanceRegistry []InstanceRegistryItem `json:"instance_registry"`
	ToolRouteBinding []Binding              `json:"tool_route_binding"`
	ToolCallAudit    []CallAudit            `json:"tool_call_audit"`
}

func (e *Engine) BuildMetadataSchema(services []app.HubServiceRegistration, instances []supervisor.Instance, auditLimit int) MetadataSchema {
	bindings := e.ListBindings()
	audits := e.ListAudits(auditLimit)
	toolRegistry := buildToolRegistry(services)
	serviceRegistry := buildServiceRegistry(services, instances, bindings)
	instanceRegistry := buildInstanceRegistry(instances)

	return MetadataSchema{
		ToolRegistry:     toolRegistry,
		ServiceRegistry:  serviceRegistry,
		InstanceRegistry: instanceRegistry,
		ToolRouteBinding: bindings,
		ToolCallAudit:    audits,
	}
}

func buildToolRegistry(services []app.HubServiceRegistration) []ToolRegistryItem {
	byToolID := map[string]ToolRegistryItem{}
	for _, service := range services {
		serviceID := strings.TrimSpace(service.ServiceID)
		serviceEnabled := strings.TrimSpace(service.Status) == app.ServiceStatusActive
		for _, descriptor := range service.Manifest.Provides {
			toolID := strings.TrimSpace(descriptor.ToolID)
			if toolID == "" {
				continue
			}
			current, exists := byToolID[toolID]
			if !exists {
				current = ToolRegistryItem{
					ToolID:               toolID,
					Streaming:            descriptor.Streaming,
					DefaultTimeoutMS:     descriptor.TimeoutMSDefault,
					CapabilitiesRequired: copyStrings(descriptor.CapabilitiesRequired),
					AllowedCallerTypes:   uniqueStrings(descriptor.AllowedCallerTypes),
					InputSchemaRef:       inlineSchemaRef(serviceID, toolID, "input", descriptor.InputSchema),
					OutputSchemaRef:      inlineSchemaRef(serviceID, toolID, "output", descriptor.OutputSchema),
					WSPath:               strings.TrimSpace(descriptor.WSPath),
					OwnerServiceID:       serviceID,
					Enabled:              serviceEnabled,
				}
				byToolID[toolID] = current
				continue
			}
			if !current.Enabled && serviceEnabled {
				current.Enabled = true
			}
			if current.OwnerServiceID == "" {
				current.OwnerServiceID = serviceID
			}
			if current.DefaultTimeoutMS <= 0 && descriptor.TimeoutMSDefault > 0 {
				current.DefaultTimeoutMS = descriptor.TimeoutMSDefault
			}
			if !current.Streaming && descriptor.Streaming {
				current.Streaming = true
			}
			if len(current.CapabilitiesRequired) == 0 && len(descriptor.CapabilitiesRequired) > 0 {
				current.CapabilitiesRequired = copyStrings(descriptor.CapabilitiesRequired)
			}
			if len(current.AllowedCallerTypes) == 0 && len(descriptor.AllowedCallerTypes) > 0 {
				current.AllowedCallerTypes = uniqueStrings(descriptor.AllowedCallerTypes)
			}
			if current.InputSchemaRef == "" {
				current.InputSchemaRef = inlineSchemaRef(serviceID, toolID, "input", descriptor.InputSchema)
			}
			if current.OutputSchemaRef == "" {
				current.OutputSchemaRef = inlineSchemaRef(serviceID, toolID, "output", descriptor.OutputSchema)
			}
			if current.WSPath == "" {
				current.WSPath = strings.TrimSpace(descriptor.WSPath)
			}
			byToolID[toolID] = current
		}
	}
	out := make([]ToolRegistryItem, 0, len(byToolID))
	for _, tool := range byToolID {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ToolID < out[j].ToolID
	})
	return out
}

func buildServiceRegistry(services []app.HubServiceRegistration, instances []supervisor.Instance, bindings []Binding) []ServiceRegistryItem {
	maxWeightByService := map[string]int{}
	for _, binding := range bindings {
		sid := strings.TrimSpace(binding.ServiceID)
		if sid == "" {
			continue
		}
		if current, ok := maxWeightByService[sid]; !ok || binding.Weight > current {
			maxWeightByService[sid] = binding.Weight
		}
	}

	transportByService := map[string]string{}
	for _, instance := range instances {
		sid := strings.TrimSpace(instance.ServiceID)
		if sid == "" {
			continue
		}
		transport := strings.ToLower(strings.TrimSpace(instance.Transport))
		if transport == "" {
			transport = "tcp"
		}
		current := transportByService[sid]
		if current == "uds" {
			continue
		}
		if transport == "uds" {
			transportByService[sid] = "uds"
			continue
		}
		if current == "" {
			transportByService[sid] = transport
		}
	}

	byService := map[string]ServiceRegistryItem{}
	for _, service := range services {
		sid := strings.TrimSpace(service.ServiceID)
		if sid == "" {
			continue
		}
		defaultWeight := maxWeightByService[sid]
		if defaultWeight <= 0 {
			defaultWeight = 100
		}
		transportPreference := transportByService[sid]
		if transportPreference == "" {
			transportPreference = "tcp"
		}
		tags := make([]string, 0, 2)
		if reliability := strings.TrimSpace(service.Reliability); reliability != "" {
			tags = append(tags, "reliability:"+reliability)
		}
		if visibility := strings.TrimSpace(service.Visibility); visibility != "" {
			tags = append(tags, "visibility:"+visibility)
		}
		byService[sid] = ServiceRegistryItem{
			ServiceID:           sid,
			Version:             strings.TrimSpace(service.Version),
			Enabled:             strings.TrimSpace(service.Status) == app.ServiceStatusActive,
			DefaultWeight:       defaultWeight,
			TransportPreference: transportPreference,
			Tags:                tags,
		}
	}

	out := make([]ServiceRegistryItem, 0, len(byService))
	for _, service := range byService {
		out = append(out, service)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ServiceID < out[j].ServiceID
	})
	return out
}

func buildInstanceRegistry(instances []supervisor.Instance) []InstanceRegistryItem {
	out := make([]InstanceRegistryItem, 0, len(instances))
	for _, instance := range instances {
		out = append(out, InstanceRegistryItem{
			InstanceID:          strings.TrimSpace(instance.InstanceID),
			ServiceID:           strings.TrimSpace(instance.ServiceID),
			Status:              strings.TrimSpace(instance.Status),
			Transport:           strings.TrimSpace(instance.Transport),
			Endpoint:            strings.TrimSpace(instance.Endpoint),
			Score:               instance.Score,
			ConsecutiveFailures: instance.ConsecutiveFailures,
			LastSuccessAt:       instance.LastSuccessAtMS,
			LastFailureAt:       instance.LastFailureAtMS,
			LastHeartbeatAt:     instance.LastHeartbeatAtMS,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceID == out[j].ServiceID {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].ServiceID < out[j].ServiceID
	})
	return out
}

func inlineSchemaRef(serviceID string, toolID string, direction string, schema map[string]any) string {
	if len(schema) == 0 {
		return ""
	}
	sid := strings.TrimSpace(serviceID)
	tid := strings.TrimSpace(toolID)
	kind := strings.TrimSpace(direction)
	if sid == "" || tid == "" || kind == "" {
		return ""
	}
	return "inline://" + sid + "/" + tid + "/" + kind
}

func copyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
