package app

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	StateIdle        = "Idle"
	StateConnecting  = "Connecting"
	StateListening   = "Listening"
	StateRecognizing = "Recognizing"
	StateThinking    = "Thinking"
	StateSpeaking    = "Speaking"
	StateInterrupted = "Interrupted"
	StateError       = "Error"
)

type ControlMessage struct {
	Type                string         `json:"type"`
	Reason              string         `json:"reason,omitempty"`
	TurnID              uint64         `json:"turn_id"`
	Limit               int            `json:"limit,omitempty"`
	BeforeID            int64          `json:"before_id,omitempty"`
	Cursor              int64          `json:"cursor,omitempty"`
	ShowMore            bool           `json:"show_more,omitempty"`
	Text                string         `json:"text,omitempty"`
	ActionID            string         `json:"action_id,omitempty"`
	ActionName          string         `json:"action_name,omitempty"`
	ActionStatus        string         `json:"action_status,omitempty"`
	ActionFollowup      string         `json:"action_followup,omitempty"`
	ActionSurfaceID     string         `json:"action_surface_id,omitempty"`
	ActionManualConfirm string         `json:"action_manual_confirm,omitempty"`
	ActionBlockReason   string         `json:"action_block_reason,omitempty"`
	ActionArgs          map[string]any `json:"action_args,omitempty"`
	ActionResult        map[string]any `json:"action_result,omitempty"`
	ActionEffect        map[string]any `json:"action_effect,omitempty"`
	ActionState         map[string]any `json:"action_state,omitempty"`

	SurfaceID      string         `json:"surface_id,omitempty"`
	SurfaceType    string         `json:"surface_type,omitempty"`
	SurfaceVersion string         `json:"surface_version,omitempty"`
	EventType      string         `json:"event_type,omitempty"`
	BusinessState  map[string]any `json:"business_state,omitempty"`
	VisibleText    string         `json:"visible_text,omitempty"`
	Status         string         `json:"status,omitempty"`
	StateVersion   int64          `json:"state_version,omitempty"`
	UpdatedAtMS    int64          `json:"updated_at_ms,omitempty"`

	ConfigSource       string         `json:"config_source,omitempty"`
	ConfigChangedPaths []string       `json:"config_changed_paths,omitempty"`
	ConfigSnapshot     map[string]any `json:"config_snapshot,omitempty"`
}

type EventMessage struct {
	Type        string `json:"type"`
	TsMS        int64  `json:"ts_ms"`
	TurnID      uint64 `json:"turn_id"`
	Value       string `json:"value,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`
	Recoverable bool   `json:"recoverable,omitempty"`
	Text        string `json:"text,omitempty"`
	Seq         int    `json:"seq,omitempty"`
	Format      string `json:"format,omitempty"`

	Origin         string         `json:"origin,omitempty"`
	MessageType    string         `json:"message_type,omitempty"`
	ActionID       string         `json:"action_id,omitempty"`
	ActionName     string         `json:"action_name,omitempty"`
	ActionStatus   string         `json:"action_status,omitempty"`
	Followup       string         `json:"followup,omitempty"`
	ManualConfirm  string         `json:"manual_confirm,omitempty"`
	BlockReason    string         `json:"block_reason,omitempty"`
	SurfaceID      string         `json:"surface_id,omitempty"`
	SurfaceType    string         `json:"surface_type,omitempty"`
	SurfaceVersion string         `json:"surface_version,omitempty"`
	StateVersion   int64          `json:"state_version,omitempty"`
	BusinessState  map[string]any `json:"business_state,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`

	HasMore  bool          `json:"has_more,omitempty"`
	Messages []ChatMessage `json:"messages,omitempty"`
}

type SurfaceCapabilities struct {
	GetState bool `json:"get_state"`
}

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

func NewStatusEvent(turnID uint64, value string, detail string) EventMessage {
	return EventMessage{
		Type:   "status",
		TsMS:   nowMS(),
		TurnID: turnID,
		Value:  value,
		Detail: detail,
	}
}

func NewErrorEvent(turnID uint64, code string, message string, recoverable bool) EventMessage {
	return EventMessage{
		Type:        "error",
		TsMS:        nowMS(),
		TurnID:      turnID,
		Code:        code,
		Message:     message,
		Recoverable: recoverable,
	}
}

func NewTextEvent(typ string, turnID uint64, text string) EventMessage {
	return EventMessage{
		Type:   typ,
		TsMS:   nowMS(),
		TurnID: turnID,
		Text:   strings.TrimSpace(text),
	}
}

func NewTTSChunkEvent(turnID uint64, seq int, format string) EventMessage {
	return EventMessage{
		Type:   "tts_chunk",
		TsMS:   nowMS(),
		TurnID: turnID,
		Seq:    seq,
		Format: format,
	}
}

func NewTTSWarnEvent(turnID uint64, seq int, code string, message string, text string) EventMessage {
	return EventMessage{
		Type:        "tts_warn",
		TsMS:        nowMS(),
		TurnID:      turnID,
		Seq:         seq,
		Code:        code,
		Message:     message,
		Recoverable: true,
		Text:        strings.TrimSpace(text),
	}
}

func encodeEvent(evt EventMessage) ([]byte, error) {
	return json.Marshal(evt)
}
