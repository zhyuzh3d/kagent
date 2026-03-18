package toolproto

type Endpoint struct {
	UDSPath string `json:"uds_path,omitempty"`
	TCPURL  string `json:"tcp_url,omitempty"`
}

type ServiceTool struct {
	ToolID               string   `json:"tool_id"`
	Version              string   `json:"version,omitempty"`
	Streaming            bool     `json:"streaming,omitempty"`
	WSPath               string   `json:"ws_path,omitempty"`
	TimeoutMS            int      `json:"timeout_ms,omitempty"`
	CapabilitiesRequired []string `json:"capabilities_required,omitempty"`
}

type SupervisorRegisterRequest struct {
	ServiceID  string        `json:"service_id"`
	InstanceID string        `json:"instance_id"`
	Version    string        `json:"version,omitempty"`
	Transport  string        `json:"transport,omitempty"`
	Endpoint   Endpoint      `json:"endpoint"`
	Tools      []ServiceTool `json:"tools,omitempty"`
	Weight     int           `json:"weight,omitempty"`
	Tags       []string      `json:"tags,omitempty"`
	HealthPath string        `json:"health_path,omitempty"`
	Healthy    *bool         `json:"healthy,omitempty"`
}

type SupervisorRegisterResult struct {
	ServiceSessionToken            string `json:"service_session_token,omitempty"`
	ExpiresInSec                   int    `json:"expires_in_sec"`
	HeartbeatIntervalSec           int    `json:"heartbeat_interval_sec"`
	InverseHeartbeatIntervalSec    int    `json:"inverse_heartbeat_interval_sec"`
	InverseHeartbeatFailuresToExit int    `json:"inverse_heartbeat_failures_to_exit"`
	DrainGracePeriodSec            int    `json:"drain_grace_period_sec"`
}

type SupervisorHeartbeatRequest struct {
	ServiceID  string         `json:"service_id"`
	InstanceID string         `json:"instance_id"`
	Status     string         `json:"status,omitempty"`
	Healthy    *bool          `json:"healthy,omitempty"`
	PID        int            `json:"pid,omitempty"`
	Endpoint   string         `json:"endpoint,omitempty"`
	Metrics    map[string]any `json:"metrics,omitempty"`
}

type SupervisorPrepareStartRequest struct {
	ServiceID         string `json:"service_id"`
	InstanceID        string `json:"instance_id,omitempty"`
	ExpectedTransport string `json:"expected_transport,omitempty"`
}

type SupervisorPrepareStartResult struct {
	Prepared bool     `json:"prepared"`
	Endpoint Endpoint `json:"endpoint,omitempty"`
}

type SupervisorDrainRequest struct {
	ServiceID      string `json:"service_id"`
	InstanceID     string `json:"instance_id"`
	Reason         string `json:"reason,omitempty"`
	GracePeriodSec int    `json:"grace_period_sec,omitempty"`
}

type SupervisorUnregisterRequest struct {
	ServiceID  string `json:"service_id"`
	InstanceID string `json:"instance_id"`
}
