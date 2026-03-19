package app

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
