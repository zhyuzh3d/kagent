package app

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

func ChatServerServiceManifest() ServiceManifest {
	return ServiceManifest{
		ServiceID:   "chat-server",
		ServiceName: "Chat Server",
		Version:     "1.0.0",
		Reliability: "trusted",
		Visibility:  "public",
		Provides: []ServiceToolDescriptor{
			{ToolID: "app.chat.project_list", Category: "app", Type: "chat", Tool: "project_list", Description: "list projects"},
			{ToolID: "app.chat.project_create", Category: "app", Type: "chat", Tool: "project_create", Description: "create project"},
			{ToolID: "app.chat.project_update", Category: "app", Type: "chat", Tool: "project_update", Description: "update project"},
			{ToolID: "app.chat.project_delete", Category: "app", Type: "chat", Tool: "project_delete", Description: "delete project"},
			{ToolID: "app.chat.thread_list", Category: "app", Type: "chat", Tool: "thread_list", Description: "list threads"},
			{ToolID: "app.chat.thread_create", Category: "app", Type: "chat", Tool: "thread_create", Description: "create thread"},
			{ToolID: "app.chat.thread_update", Category: "app", Type: "chat", Tool: "thread_update", Description: "update thread"},
			{ToolID: "app.chat.thread_delete", Category: "app", Type: "chat", Tool: "thread_delete", Description: "delete thread"},
			{ToolID: "app.chat.stream", Category: "app", Type: "chat", Tool: "stream", Description: "chat stream websocket", Streaming: "ws", WSPath: "/service/tool/ws"},
		},
		Requires: []string{"ai.llm.stream", "ai.speech.asr", "ai.speech.tts"},
	}
}
