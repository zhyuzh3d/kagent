package app

type AIServiceInfo struct {
	ServiceID    string   `json:"service_id"`
	ServiceName  string   `json:"service_name"`
	Version      string   `json:"version"`
	Provider     string   `json:"provider"`
	Capabilities []string `json:"capabilities"`
	Transport    string   `json:"transport"`
}

type AIServiceToolDescriptor struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description"`
	InputSchema          map[string]any `json:"input_schema"`
	OutputSchema         map[string]any `json:"output_schema"`
	SideEffect           string         `json:"side_effect"`
	CapabilitiesRequired []string       `json:"capabilities_required,omitempty"`
	AllowedCallerTypes   []string       `json:"allowed_caller_types,omitempty"`
	Idempotency          string         `json:"idempotency,omitempty"`
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	Streaming            string         `json:"streaming,omitempty"`
}

type AIServiceListToolsResponse struct {
	ServiceID string                    `json:"service_id"`
	Tools     []AIServiceToolDescriptor `json:"tools"`
}
