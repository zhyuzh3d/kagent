package app

type ServiceCallRecord struct {
	RequestID   string `json:"request_id"`
	ServiceID   string `json:"service_id"`
	Capability  string `json:"capability"`
	TurnID      uint64 `json:"turn_id,omitempty"`
	StartedAtMS int64  `json:"started_at_ms"`
	LatencyMS   int64  `json:"latency_ms"`
	Result      string `json:"result"`
	Error       string `json:"error,omitempty"`
}

type ServiceCallObserver interface {
	RecordCall(record ServiceCallRecord)
}
