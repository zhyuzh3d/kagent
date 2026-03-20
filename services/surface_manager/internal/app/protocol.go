package app

import "time"

type SurfaceState struct {
	SurfaceID      string         `json:"surface_id"`
	SurfaceType    string         `json:"surface_type,omitempty"`
	SurfaceVersion string         `json:"surface_version,omitempty"`
	EventType      string         `json:"event_type,omitempty"`
	BusinessState  map[string]any `json:"business_state,omitempty"`
	VisibleText    string         `json:"visible_text,omitempty"`
	Status         string         `json:"status,omitempty"`
	StateVersion   int64          `json:"state_version,omitempty"`
	UpdatedAtMS    int64          `json:"updated_at_ms,omitempty"`
}

type ActionCall struct {
	ActionID    string         `json:"action_id"`
	ActionName  string         `json:"action_name"`
	SurfaceID   string         `json:"surface_id,omitempty"`
	TurnID      uint64         `json:"turn_id,omitempty"`
	Followup    string         `json:"followup"`
	Args        map[string]any `json:"args,omitempty"`
	RequestedAt int64          `json:"requested_at_ms"`
}

type ActionReport struct {
	ReportID       string         `json:"report_id"`
	Origin         string         `json:"origin"`
	UserID         string         `json:"user_id"`
	ChatID         string         `json:"chat_id"`
	ProjectID      string         `json:"project_id,omitempty"`
	ThreadID       string         `json:"thread_id,omitempty"`
	TurnID         uint64         `json:"turn_id"`
	SurfaceID      string         `json:"surface_id,omitempty"`
	SurfaceType    string         `json:"surface_type,omitempty"`
	SurfaceVersion string         `json:"surface_version,omitempty"`
	ActionID       string         `json:"action_id"`
	ActionName     string         `json:"action_name"`
	Followup       string         `json:"followup"`
	Status         string         `json:"status"`
	ResultSummary  string         `json:"result_summary,omitempty"`
	EffectSummary  string         `json:"effect_summary,omitempty"`
	BusinessState  map[string]any `json:"business_state,omitempty"`
	ManualConfirm  string         `json:"manual_confirm,omitempty"`
	BlockReason    string         `json:"block_reason,omitempty"`
	CreatedAtMS    int64          `json:"created_at_ms"`
	CreatedAtISO   string         `json:"created_at_iso"`
	ProviderRole   string         `json:"provider_role,omitempty"`
	MessageType    string         `json:"message_type,omitempty"`
	Visibility     string         `json:"visibility,omitempty"`
	ContinuationID string         `json:"continuation_id,omitempty"`
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}
