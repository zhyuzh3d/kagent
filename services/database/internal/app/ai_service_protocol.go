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
	Idempotency          string         `json:"idempotency,omitempty"`
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	Streaming            string         `json:"streaming,omitempty"`
}

type AIServiceListToolsResponse struct {
	ServiceID string                    `json:"service_id"`
	Tools     []AIServiceToolDescriptor `json:"tools"`
}

type AIServiceHealth struct {
	OK        bool   `json:"ok"`
	Timestamp int64  `json:"timestamp_ms"`
	Version   string `json:"version,omitempty"`
}

type AIServiceASRStart struct {
	Type      string        `json:"type"`
	RequestID string        `json:"request_id,omitempty"`
	TurnID    uint64        `json:"turn_id,omitempty"`
	History   []ChatMessage `json:"history,omitempty"`
}

type AIServiceASRControl struct {
	Type string `json:"type"`
}

type AIServiceASREvent struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

type AIServiceLLMStreamRequest struct {
	RequestID string        `json:"request_id,omitempty"`
	TurnID    uint64        `json:"turn_id,omitempty"`
	Input     string        `json:"input"`
	History   []ChatMessage `json:"history,omitempty"`
}

type AIServiceLLMStreamEvent struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

type AIServiceTTSSynthesizeRequest struct {
	RequestID string `json:"request_id,omitempty"`
	TurnID    uint64 `json:"turn_id,omitempty"`
	Text      string `json:"text"`
}

type AIServiceTTSSynthesizeResponse struct {
	AudioBase64 string `json:"audio_base64"`
	Format      string `json:"format"`
}
