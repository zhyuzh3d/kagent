package app

type AIServiceInfo struct {
	ServiceID    string   `json:"service_id"`
	ServiceName  string   `json:"service_name"`
	Version      string   `json:"version"`
	Provider     string   `json:"provider"`
	Capabilities []string `json:"capabilities"`
	Transport    string   `json:"transport"`
}

type AIServiceHealth struct {
	OK        bool   `json:"ok"`
	Timestamp int64  `json:"timestamp_ms"`
	Version   string `json:"version,omitempty"`
}

type AIServiceASRStart struct {
	Type    string        `json:"type"`
	History []ChatMessage `json:"history,omitempty"`
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
	Input   string        `json:"input"`
	History []ChatMessage `json:"history,omitempty"`
}

type AIServiceLLMStreamEvent struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

type AIServiceTTSSynthesizeRequest struct {
	Text string `json:"text"`
}

type AIServiceTTSSynthesizeResponse struct {
	AudioBase64 string `json:"audio_base64"`
	Format      string `json:"format"`
}
