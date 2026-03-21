package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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

func inferMessageCategory(fallback string, actionType string, role string) string {
	if fallback != "" && fallback != CategoryChat {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(actionType)) {
	case TypeActionCall, TypeActionExecute, TypeActionReport, TypeActionProgress, TypeActionCombined:
		if role == RoleUser {
			return CategoryUserAction
		}
		return CategoryAIAction
	case TypeActionState:
		return CategorySurface
	default:
		return fallback
	}
}

func composeMessageContent(say string, aside string) string {
	main := strings.TrimSpace(say)
	note := strings.TrimSpace(aside)
	if main == "" {
		return note
	}
	if note == "" {
		return main
	}
	return main + "\n" + note
}

func normalizeActionJSON(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return clean
	}
	if len(parsed) == 0 {
		return ""
	}
	if asTrimmedString(parsed["type"]) == "" {
		if asTrimmedString(parsed["path"]) != "" || asTrimmedString(parsed["name"]) != "" {
			parsed["type"] = TypeActionCall
		}
	}
	compact, err := json.Marshal(parsed)
	if err != nil {
		return clean
	}
	return string(compact)
}

func detectActionTypeFromJSON(actionJSON string) string {
	clean := strings.TrimSpace(actionJSON)
	if clean == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return ""
	}
	return strings.ToLower(asTrimmedString(parsed["type"]))
}

func normalizeRawDataJSON(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return mustJSON(map[string]any{"raw_text": clean})
	}
	b, err := json.Marshal(parsed)
	if err != nil {
		return clean
	}
	return string(b)
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
