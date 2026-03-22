package app

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleObserver  = "observer"
	RoleSystem    = "system"

	CategoryChat           = "chat"
	CategoryAIAction       = "ai_action"
	CategoryUserAction     = "user_action"
	CategorySurface        = "surface"
	CategorySurfaceContext = "surface_context"
	CategoryPhase          = "phase"
	CategoryConfig         = "config"
	CategoryError          = "error"

	TypeUserMessage           = "user_message"
	TypeAssistantMessage      = "assistant_message"
	TypeActionCall            = "call"
	TypeActionExecute         = "execute"
	TypeActionReport          = "report"
	TypeActionProgress        = "progress"
	TypeActionCombined        = "combined"
	TypeActionState           = "state_change"
	TypeSurfaceOpen           = "surface_open"
	TypeSurfaceState          = "surface_state"
	TypeSurfaceChange         = "surface_change"
	TypeSurfaceRegistrySync   = "surface_registry_sync"
	TypeSurfaceActiveChange   = "surface_active_change"
	TypeSurfaceRuntimeContext = "surface_runtime_context"
	TypeConvoStart            = "convo_start"
	TypeConvoStop             = "convo_stop"
	TypePageClose             = "page_close"
	TypeTurnNack              = "turn_nack"
	TypeConfigChange          = "config_change"
	TypeErrorEvent            = "error_event"
	TypeWarningEvent          = "warning_event"

	CompletionStatusComplete    = "complete"
	CompletionStatusInterrupted = "interrupted"
	CompletionStatusError       = "error"

	InterruptNone   = "none"
	InterruptVAD    = "vad"
	InterruptManual = "manual"
	InterruptOther  = "other"

	ObserverSourceKindSurface = "surface"
	ObserverSourceKindPage    = "page"
	ObserverSourceKindSystem  = "system"
	ObserverSourceIDPageChat  = "page/chat"
	ObserverSourceLabelPage   = "Chat Page"

	PayloadSchemaVersion1 = 1
)

type ChatMessage struct {
	StoreID               int64  `json:"store_id,omitempty"`
	MessageID             string `json:"message_id,omitempty"`
	ProjectID             string `json:"project_id,omitempty"`
	ThreadID              string `json:"thread_id,omitempty"`
	TurnID                uint64 `json:"turn_id,omitempty"`
	Seq                   int64  `json:"seq,omitempty"`
	Role                  string `json:"role"`
	Say                   string `json:"say,omitempty"`
	Aside                 string `json:"aside,omitempty"`
	ActionJSON            string `json:"action_json,omitempty"`
	RefMessageID          string `json:"ref_message_id,omitempty"`
	RefActionSlot         int    `json:"ref_action_slot,omitempty"`
	RawData               string `json:"raw_data,omitempty"`
	ParseError            string `json:"parse_error,omitempty"`
	Category              string `json:"category,omitempty"`
	MessageType           string `json:"message_type,omitempty"`
	Content               string `json:"content"`
	PayloadSchemaVersion  int    `json:"payload_schema_version,omitempty"`
	PayloadJSON           string `json:"payload_json,omitempty"`
	Visibility            string `json:"visibility,omitempty"`
	CreatedAtMS           int64  `json:"created_at_ms,omitempty"`
	CreatedAtISO          string `json:"created_at_iso,omitempty"`
	CreatedAtLocalYMDHMS  string `json:"created_at_local_ymdhms,omitempty"`
	CreatedAtLocalWeekday string `json:"created_at_local_weekday,omitempty"`
	CreatedAtLocalLunar   string `json:"created_at_local_lunar,omitempty"`
	CompletionStatus      string `json:"completion_status,omitempty"`
	Interrupt             string `json:"interrupt,omitempty"`
	InterruptAtMS         int64  `json:"interrupt_at_ms,omitempty"`
	PartialText           string `json:"partial_text,omitempty"`
}

type MessageWrite struct {
	MessageID            string
	TurnID               uint64
	Seq                  int64
	Role                 string
	Say                  string
	Aside                string
	ActionJSON           string
	RefMessageID         string
	RefActionSlot        int
	RawData              string
	ParseError           string
	Category             string
	MessageType          string
	Content              string
	PayloadSchemaVersion int
	Payload              map[string]any
	PayloadJSON          string
	CreatedAtMS          int64
	CompletionStatus     string
	Interrupt            string
	InterruptAtMS        int64
	PartialText          string
}
