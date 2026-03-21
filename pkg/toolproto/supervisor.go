package toolproto

import "strings"

type Endpoint struct {
	UDSPath string `json:"uds_path,omitempty"`
	TCPURL  string `json:"tcp_url,omitempty"`
}

type ServiceTool struct {
	Category             string         `json:"category,omitempty"`
	Type                 string         `json:"type,omitempty"`
	Tool                 string         `json:"tool,omitempty"`
	ToolID               string         `json:"tool_id"`
	Description          string         `json:"description,omitempty"`
	InputSchema          map[string]any `json:"input_schema,omitempty"`
	OutputSchema         map[string]any `json:"output_schema,omitempty"`
	Protocol             string         `json:"protocol,omitempty"`
	Version              string         `json:"version,omitempty"`
	HubOnly              bool           `json:"hub_only,omitempty"`
	HasEffects           bool           `json:"has_effects,omitempty"`
	RiskLV               int            `json:"risk_lv,omitempty"`
	Streaming            bool           `json:"streaming,omitempty"`
	StreamingMode        string         `json:"streaming_mode,omitempty"`
	WSPath               string         `json:"ws_path,omitempty"`
	TimeoutMS            int            `json:"timeout_ms,omitempty"`
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	SideEffect           string         `json:"side_effect,omitempty"`
	ScopeSupport         []string       `json:"scope_support,omitempty"`
	CapabilitiesRequired []string       `json:"capabilities_required,omitempty"`
	AllowedCallerTypes   []string       `json:"allowed_caller_types,omitempty"`
}

type ToolSpec = ServiceTool

type ToolObserved struct {
	Registered           bool   `json:"registered"`
	HealthyInstanceCount int    `json:"healthy_instance_count,omitempty"`
	LastSeenAtMS         int64  `json:"last_seen_at_ms,omitempty"`
	Transport            string `json:"transport,omitempty"`
	Endpoint             string `json:"endpoint,omitempty"`
	Source               string `json:"source,omitempty"`
}

type ToolGovernance struct {
	Enabled        bool    `json:"enabled"`
	BoundServiceID string  `json:"bound_service_id,omitempty"`
	BindingReason  string  `json:"binding_reason,omitempty"`
	ManualOverride bool    `json:"manual_override,omitempty"`
	Reliability    string  `json:"reliability,omitempty"`
	SuccessRate    float64 `json:"success_rate,omitempty"`
	CallCount      int64   `json:"call_count,omitempty"`
	ConflictReason string  `json:"conflict_reason,omitempty"`
}

type ToolCandidate struct {
	ServiceID     string  `json:"service_id"`
	ServiceName   string  `json:"service_name,omitempty"`
	Reliability   string  `json:"reliability,omitempty"`
	Enabled       bool    `json:"enabled"`
	Healthy       bool    `json:"healthy"`
	Status        string  `json:"status,omitempty"`
	Transport     string  `json:"transport,omitempty"`
	Endpoint      string  `json:"endpoint,omitempty"`
	LastSeenAtMS  int64   `json:"last_seen_at_ms,omitempty"`
	SuccessRate   float64 `json:"success_rate,omitempty"`
	CallCount     int64   `json:"call_count,omitempty"`
	LastLatencyMS int64   `json:"last_latency_ms,omitempty"`
	ManualWeight  int     `json:"manual_weight,omitempty"`
	LastErrorRate int     `json:"last_error_rate,omitempty"`
}

type ToolView struct {
	ToolID     string          `json:"tool_id"`
	ServiceID  string          `json:"service_id,omitempty"`
	Spec       ToolSpec        `json:"spec"`
	Observed   ToolObserved    `json:"observed"`
	Governance ToolGovernance  `json:"governance"`
	Candidates []ToolCandidate `json:"candidates,omitempty"`
}

type ServiceManifest struct {
	ServiceID           string        `json:"service_id"`
	ServiceName         string        `json:"service_name"`
	Version             string        `json:"version,omitempty"`
	BuildHash           string        `json:"build_hash,omitempty"`
	Visibility          string        `json:"visibility,omitempty"`
	Entry               string        `json:"entry,omitempty"`
	ConfigSchemaVersion int           `json:"config_schema_version,omitempty"`
	Provides            []ServiceTool `json:"provides,omitempty"`
	Requires            []string      `json:"requires,omitempty"`
	DependsOn           []string      `json:"depends_on,omitempty"`
}

type AccountPublicKey struct {
	KID       string `json:"kid"`
	Alg       string `json:"alg"`
	PublicKey string `json:"public_key"`
}

type AccountPublicKeysResult struct {
	Keys []AccountPublicKey `json:"keys"`
}

type AccountActiveSession struct {
	UserID string `json:"user_id"`
	SID    string `json:"sid"`
}

type AccountActiveSessionsResult struct {
	Items []AccountActiveSession `json:"items"`
}

type SupervisorRegisterRequest struct {
	ServiceID  string        `json:"service_id"`
	InstanceID string        `json:"instance_id"`
	PID        int           `json:"pid,omitempty"`
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
	ServiceSessionToken            string                 `json:"service_session_token,omitempty"`
	ExpiresInSec                   int                    `json:"expires_in_sec"`
	HeartbeatIntervalSec           int                    `json:"heartbeat_interval_sec"`
	InverseHeartbeatIntervalSec    int                    `json:"inverse_heartbeat_interval_sec"`
	InverseHeartbeatFailuresToExit int                    `json:"inverse_heartbeat_failures_to_exit"`
	DrainGracePeriodSec            int                    `json:"drain_grace_period_sec"`
	RegisteredService              *RegisteredServiceInfo `json:"registered_service,omitempty"`
}

type RegisteredServiceInfo struct {
	ServiceID          string          `json:"service_id"`
	ServiceName        string          `json:"service_name,omitempty"`
	Version            string          `json:"version,omitempty"`
	BuildHash          string          `json:"build_hash,omitempty"`
	Reliability        string          `json:"reliability,omitempty"`
	Visibility         string          `json:"visibility,omitempty"`
	InstanceID         string          `json:"instance_id,omitempty"`
	PID                int             `json:"pid,omitempty"`
	Endpoint           string          `json:"endpoint,omitempty"`
	Status             string          `json:"status,omitempty"`
	Healthy            bool            `json:"healthy"`
	ManifestHash       string          `json:"manifest_hash,omitempty"`
	ToolCount          int             `json:"tool_count,omitempty"`
	RegisteredAtMS     int64           `json:"registered_at_ms,omitempty"`
	LastSeenAtMS       int64           `json:"last_seen_at_ms,omitempty"`
	RegisteredManifest ServiceManifest `json:"registered_manifest,omitempty"`
}

type ServiceRuntimeManifest struct {
	ServiceName        string          `json:"service_name,omitempty"`
	ServiceID          string          `json:"service_id"`
	Version            string          `json:"version,omitempty"`
	BuildHash          string          `json:"build_hash,omitempty"`
	Reliability        string          `json:"reliability,omitempty"`
	Visibility         string          `json:"visibility,omitempty"`
	Registered         bool            `json:"registered"`
	Active             bool            `json:"active"`
	InstanceID         string          `json:"instance_id,omitempty"`
	PID                int             `json:"pid,omitempty"`
	Endpoint           string          `json:"endpoint,omitempty"`
	Status             string          `json:"status,omitempty"`
	Healthy            bool            `json:"healthy"`
	ManifestHash       string          `json:"manifest_hash,omitempty"`
	ToolCount          int             `json:"tool_count,omitempty"`
	RegisteredAtMS     int64           `json:"registered_at_ms,omitempty"`
	LastSeenAtMS       int64           `json:"last_seen_at_ms,omitempty"`
	RegisteredManifest ServiceManifest `json:"registered_manifest,omitempty"`
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

type LifecycleState struct {
	Status      string         `json:"status,omitempty"`
	Healthy     bool           `json:"healthy"`
	ServiceID   string         `json:"service_id,omitempty"`
	InstanceID  string         `json:"instance_id,omitempty"`
	Endpoint    string         `json:"endpoint,omitempty"`
	PID         int            `json:"pid,omitempty"`
	StartedAtMS int64          `json:"started_at_ms,omitempty"`
	UpdatedAtMS int64          `json:"updated_at_ms,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type LifecycleDrainRequest struct {
	Reason         string `json:"reason,omitempty"`
	GracePeriodSec int    `json:"grace_period_sec,omitempty"`
}

func NormalizeServiceTool(in ServiceTool) ServiceTool {
	in.ToolID = strings.TrimSpace(in.ToolID)
	in.Category = strings.TrimSpace(in.Category)
	in.Type = strings.TrimSpace(in.Type)
	in.Tool = strings.TrimSpace(in.Tool)
	in.Description = strings.TrimSpace(in.Description)
	in.Protocol = strings.TrimSpace(in.Protocol)
	in.Version = strings.TrimSpace(in.Version)
	in.StreamingMode = strings.TrimSpace(in.StreamingMode)
	in.WSPath = strings.TrimSpace(in.WSPath)
	in.SideEffect = strings.TrimSpace(in.SideEffect)
	in.ScopeSupport = UniqueNonEmptyStrings(in.ScopeSupport)
	in.CapabilitiesRequired = UniqueNonEmptyStrings(in.CapabilitiesRequired)
	in.AllowedCallerTypes = NormalizeAllowedCallerTypes(in.AllowedCallerTypes)
	if in.TimeoutMSDefault <= 0 && in.TimeoutMS > 0 {
		in.TimeoutMSDefault = in.TimeoutMS
	}
	if in.Streaming && in.StreamingMode == "" {
		in.StreamingMode = "ws"
	}
	if in.Protocol == "" {
		if in.Streaming || in.WSPath != "" {
			in.Protocol = "ws"
		} else {
			in.Protocol = "http"
		}
	}
	if in.ToolID != "" && (in.Category == "" || in.Type == "" || in.Tool == "") {
		category, typ, tool := SplitToolID(in.ToolID)
		if in.Category == "" {
			in.Category = category
		}
		if in.Type == "" {
			in.Type = typ
		}
		if in.Tool == "" {
			in.Tool = tool
		}
	}
	return in
}

func NormalizeServiceManifest(in ServiceManifest) ServiceManifest {
	in.ServiceID = strings.TrimSpace(in.ServiceID)
	in.ServiceName = strings.TrimSpace(in.ServiceName)
	in.Version = strings.TrimSpace(in.Version)
	in.BuildHash = strings.TrimSpace(in.BuildHash)
	in.Visibility = strings.TrimSpace(in.Visibility)
	in.Entry = strings.TrimSpace(in.Entry)
	in.Requires = UniqueNonEmptyStrings(in.Requires)
	in.DependsOn = UniqueNonEmptyStrings(in.DependsOn)
	if len(in.Provides) > 0 {
		out := make([]ServiceTool, 0, len(in.Provides))
		for _, tool := range in.Provides {
			if strings.TrimSpace(tool.ToolID) == "" {
				continue
			}
			out = append(out, NormalizeServiceTool(tool))
		}
		in.Provides = out
	}
	return in
}
