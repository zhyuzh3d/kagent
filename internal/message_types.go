package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleObserver  = "observer"
	RoleSystem    = "system"

	CategoryChat       = "chat"
	CategoryAIAction   = "ai_action"
	CategoryUserAction = "user_action"
	CategorySurface    = "surface"
	CategoryPhase      = "phase"
	CategoryConfig     = "config"
	CategoryError      = "error"

	TypeUserMessage      = "user_message"
	TypeAssistantMessage = "assistant_message"
	TypeActionCall       = "call"
	TypeActionReport     = "report"
	TypeActionCombined   = "combined"
	TypeSurfaceOpen      = "surface_open"
	TypeSurfaceState     = "surface_state"
	TypeSurfaceChange    = "surface_change"
	TypeConvoStart       = "convo_start"
	TypeConvoStop        = "convo_stop"
	TypePageClose        = "page_close"
	TypeTurnNack         = "turn_nack"
	TypeConfigChange     = "config_change"
	TypeErrorEvent       = "error_event"
	TypeWarningEvent     = "warning_event"

	CompletionStatusComplete    = "complete"
	CompletionStatusInterrupted = "interrupted"
	CompletionStatusError       = "error"

	InterruptNone   = "none"
	InterruptVAD    = "vad"
	InterruptManual = "manual"
	InterruptOther  = "other"

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

func BuildMessage(in MessageWrite) (ChatMessage, error) {
	role := normalizeMessageRole(in.Role)
	category := normalizeMessageCategory(in.Category)
	messageType := normalizeMessageType(category, in.MessageType, role)
	payloadVersion := in.PayloadSchemaVersion
	if payloadVersion <= 0 {
		payloadVersion = PayloadSchemaVersion1
	}
	payload := clonePayloadMap(in.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON := strings.TrimSpace(in.PayloadJSON)
	if payloadJSON == "" {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return ChatMessage{}, fmt.Errorf("marshal payload: %w", err)
		}
		payloadJSON = string(encoded)
	} else if len(payload) == 0 {
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		content = strings.TrimSpace(renderMessageContent(category, messageType, payload))
	}
	if content == "" {
		return ChatMessage{}, fmt.Errorf("message content is empty for %s.%s", category, messageType)
	}
	createdAtMS := in.CreatedAtMS
	if createdAtMS <= 0 {
		createdAtMS = nowMS()
	}
	timeFields := buildSemanticTimeFields(createdAtMS)
	completionStatus := normalizeCompletionStatus(in.CompletionStatus)
	if role == RoleAssistant && category == CategoryChat && messageType == TypeAssistantMessage && completionStatus == "" {
		completionStatus = CompletionStatusComplete
	}
	interrupt := normalizeInterrupt(in.Interrupt)
	if completionStatus == "" {
		interrupt = ""
	} else if interrupt == "" {
		interrupt = InterruptNone
	}
	entry := ChatMessage{
		MessageID:             strings.TrimSpace(in.MessageID),
		TurnID:                in.TurnID,
		Seq:                   in.Seq,
		Role:                  role,
		Category:              category,
		MessageType:           messageType,
		Content:               content,
		PayloadSchemaVersion:  payloadVersion,
		PayloadJSON:           payloadJSON,
		Visibility:            messageVisibility(role, category, messageType),
		CreatedAtMS:           createdAtMS,
		CreatedAtISO:          timeFields.ISO,
		CreatedAtLocalYMDHMS:  timeFields.LocalYMDHMS,
		CreatedAtLocalWeekday: timeFields.LocalWeekday,
		CreatedAtLocalLunar:   timeFields.LocalLunar,
		CompletionStatus:      completionStatus,
		Interrupt:             interrupt,
		InterruptAtMS:         in.InterruptAtMS,
		PartialText:           strings.TrimSpace(in.PartialText),
	}
	if entry.MessageID == "" {
		entry.MessageID = "msg-" + newRequestID()
	}
	return entry, nil
}

func normalizeMessageRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleUser:
		return RoleUser
	case RoleAssistant:
		return RoleAssistant
	case RoleObserver:
		return RoleObserver
	case RoleSystem:
		return RoleSystem
	default:
		return RoleAssistant
	}
}

func normalizeMessageCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case CategoryChat:
		return CategoryChat
	case CategoryAIAction:
		return CategoryAIAction
	case CategoryUserAction:
		return CategoryUserAction
	case CategorySurface:
		return CategorySurface
	case CategoryPhase:
		return CategoryPhase
	case CategoryConfig:
		return CategoryConfig
	case CategoryError:
		return CategoryError
	default:
		return CategoryChat
	}
}

func normalizeMessageType(category string, messageType string, role string) string {
	clean := strings.ToLower(strings.TrimSpace(messageType))
	if clean != "" {
		return clean
	}
	switch category {
	case CategoryChat:
		if role == RoleUser {
			return TypeUserMessage
		}
		return TypeAssistantMessage
	case CategoryAIAction, CategoryUserAction:
		return TypeActionReport
	case CategorySurface:
		return TypeSurfaceChange
	case CategoryPhase:
		return TypeConvoStart
	case CategoryConfig:
		return TypeConfigChange
	case CategoryError:
		return TypeErrorEvent
	default:
		return TypeAssistantMessage
	}
}

func normalizeCompletionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case CompletionStatusComplete:
		return CompletionStatusComplete
	case CompletionStatusInterrupted:
		return CompletionStatusInterrupted
	case CompletionStatusError:
		return CompletionStatusError
	default:
		return ""
	}
}

func normalizeInterrupt(interrupt string) string {
	switch strings.ToLower(strings.TrimSpace(interrupt)) {
	case InterruptNone:
		return InterruptNone
	case InterruptVAD:
		return InterruptVAD
	case InterruptManual:
		return InterruptManual
	case InterruptOther:
		return InterruptOther
	default:
		return ""
	}
}

func messageVisibility(role string, category string, messageType string) string {
	if category == CategoryChat && (messageType == TypeUserMessage || messageType == TypeAssistantMessage) {
		return "visible"
	}
	return "hidden"
}

func isAnchorMessage(msg ChatMessage) bool {
	return msg.Category == CategoryChat && (msg.MessageType == TypeUserMessage || msg.MessageType == TypeAssistantMessage)
}

func isUIVisibleMessage(msg ChatMessage) bool {
	return messageVisibility(msg.Role, msg.Category, msg.MessageType) == "visible"
}

func semanticPromptContent(msg ChatMessage) string {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if msg.CreatedAtLocalYMDHMS != "" {
		parts = append(parts, msg.CreatedAtLocalYMDHMS)
	}
	if msg.CreatedAtLocalWeekday != "" {
		parts = append(parts, msg.CreatedAtLocalWeekday)
	}
	if msg.CreatedAtLocalLunar != "" {
		parts = append(parts, msg.CreatedAtLocalLunar)
	}
	if len(parts) == 0 {
		return content
	}
	return strings.Join(parts, " ") + " " + content
}

func renderMessageContent(category string, messageType string, payload map[string]any) string {
	switch category {
	case CategoryChat:
		return firstNonEmpty(asTrimmedString(payload["text"]), asTrimmedString(payload["content"]))
	case CategoryAIAction, CategoryUserAction:
		return renderActionContent(category, messageType, payload)
	case CategorySurface:
		return renderSurfaceContent(messageType, payload)
	case CategoryPhase:
		return renderPhaseContent(messageType)
	case CategoryConfig:
		return renderConfigContent(payload)
	case CategoryError:
		return renderErrorContent(messageType, payload)
	default:
		return firstNonEmpty(asTrimmedString(payload["text"]), jsonCompactString(payload))
	}
}

func renderActionContent(category string, messageType string, payload map[string]any) string {
	name := firstNonEmpty(asTrimmedString(payload["action_name"]), asTrimmedString(payload["name"]), "unknown_action")
	followup := normalizeFollowup(asTrimmedString(payload["followup"]))
	argsText := jsonCompactString(anyMap(payload["args"]))
	resultText := firstNonEmpty(asTrimmedString(payload["result_summary"]), jsonCompactString(anyMap(payload["result"])))
	effectText := firstNonEmpty(asTrimmedString(payload["effect_summary"]), jsonCompactString(anyMap(payload["effect"])))
	status := firstNonEmpty(asTrimmedString(payload["status"]), "unknown")
	switch messageType {
	case TypeActionCall:
		return fmt.Sprintf("准备执行动作：%s。（%s.call name=%s followup=%s args=%s）", name, category, name, followup, argsText)
	case TypeActionCombined:
		return fmt.Sprintf("动作已执行：%s。（%s.combined name=%s status=%s followup=%s result=%s effect=%s）", name, category, name, status, followup, resultText, effectText)
	default:
		return fmt.Sprintf("动作执行%s：%s。（%s.report name=%s status=%s followup=%s result=%s effect=%s）", humanizeActionStatus(status), name, category, name, status, followup, resultText, effectText)
	}
}

func renderSurfaceContent(messageType string, payload map[string]any) string {
	surfaceID := firstNonEmpty(asTrimmedString(payload["surface_id"]), asTrimmedString(payload["name"]), "surface")
	stateText := firstNonEmpty(asTrimmedString(payload["state_text"]), jsonCompactString(anyMap(payload["business_state"])), jsonCompactString(anyMap(payload["state"])))
	status := firstNonEmpty(asTrimmedString(payload["status"]), "unknown")
	eventType := firstNonEmpty(asTrimmedString(payload["event_type"]), "state_change")
	if stateText == "" {
		stateText = fmt.Sprintf("status=%s event=%s", status, eventType)
	}
	switch messageType {
	case TypeSurfaceOpen:
		return fmt.Sprintf("已打开 surface：%s。（surface_open name=%s status=%s）", surfaceID, surfaceID, status)
	case TypeSurfaceState:
		return fmt.Sprintf("%s 当前状态：%s。（surface_state name=%s status=%s state=%s）", surfaceID, stateText, surfaceID, status, stateText)
	default:
		return fmt.Sprintf("%s 发生变化：%s。（surface_change name=%s status=%s event=%s delta=%s）", surfaceID, stateText, surfaceID, status, eventType, stateText)
	}
}

func renderPhaseContent(messageType string) string {
	switch messageType {
	case TypeConvoStart:
		return "对话开始。"
	case TypeConvoStop:
		return "对话停止。"
	case TypePageClose:
		return "页面关闭。"
	case TypeTurnNack:
		return "本轮输入无有效文本。"
	default:
		return "阶段事件。"
	}
}

func renderConfigContent(payload map[string]any) string {
	paths := stringSlice(payload["changed_paths"])
	source := firstNonEmpty(asTrimmedString(payload["source"]), "unknown")
	if len(paths) == 0 {
		return fmt.Sprintf("配置已更新。（config_change source=%s）", source)
	}
	label := paths[0]
	if len(paths) > 1 {
		label = fmt.Sprintf("%s 等 %d 项", paths[0], len(paths))
	}
	return fmt.Sprintf("配置已更新：%s。（config_change source=%s paths=%s）", label, source, jsonCompactString(paths))
}

func renderErrorContent(messageType string, payload map[string]any) string {
	text := firstNonEmpty(asTrimmedString(payload["message"]), asTrimmedString(payload["text"]), "unknown")
	switch messageType {
	case TypeWarningEvent:
		return fmt.Sprintf("系统警告：%s。", text)
	default:
		return fmt.Sprintf("系统错误：%s。", text)
	}
}

func humanizeActionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "complete", "completed":
		return "成功"
	case "blocked":
		return "阻塞"
	case "cancelled", "canceled":
		return "取消"
	case "fail", "failed", "error":
		return "失败"
	default:
		return "完成"
	}
}

func clonePayloadMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func anyMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func stringSlice(v any) []string {
	switch vv := v.(type) {
	case []string:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			text := asTrimmedString(item)
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func asTrimmedString(v any) string {
	if v == nil {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return strings.TrimSpace(vv)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func jsonCompactString(v any) string {
	if v == nil {
		return "{}"
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	if arr, ok := v.([]string); ok {
		clone := append([]string(nil), arr...)
		sort.Strings(clone)
		b, err := json.Marshal(clone)
		if err == nil {
			return string(b)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func mapProviderRole(roleInternal string) string {
	switch normalizeMessageRole(roleInternal) {
	case RoleObserver:
		return RoleSystem
	case RoleSystem:
		return RoleSystem
	case RoleUser:
		return RoleUser
	default:
		return RoleAssistant
	}
}
