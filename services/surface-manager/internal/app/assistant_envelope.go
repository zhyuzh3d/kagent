package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

type assistantEnvelope struct {
	Say        string
	Aside      string
	ActionJSON string
	RawData    string
	ParseError string
}

func parseAssistantEnvelope(raw string) assistantEnvelope {
	clean := strings.TrimSpace(raw)
	out := assistantEnvelope{
		RawData: mustJSON(map[string]any{
			"raw_text": clean,
			"llm":      map[string]any{},
			"extra":    map[string]any{},
		}),
	}
	if clean == "" {
		return out
	}
	payload, ok := parseLLMEnvelopePayload(clean)
	if !ok {
		if looksLikeLLMEnvelope(clean) {
			out.ParseError = "assistant output is not valid json envelope"
			out.Say = formatMalformedPreview(clean)
			return out
		}
		out.Say = clean
		return out
	}
	out.Say = firstNonEmpty(asTrimmedString(payload["say"]), asTrimmedString(payload["content"]))
	out.Aside = asTrimmedString(payload["aside"])
	action := pickActionPayload(payload)
	if len(action) > 0 {
		out.ActionJSON = normalizeActionJSON(mustJSON(action))
	}
	if out.Say == "" && out.Aside == "" && out.ActionJSON == "" {
		out.ParseError = "assistant json envelope missing say/aside/action"
		out.Say = formatMalformedPreview(clean)
	}
	return out
}

func parseLLMEnvelopePayload(raw string) (map[string]any, bool) {
	candidates := []string{strings.TrimSpace(raw)}
	text := strings.TrimSpace(raw)
	lower := strings.ToLower(text)
	if idx := strings.Index(lower, "```json"); idx >= 0 {
		rest := text[idx+7:]
		if end := strings.Index(strings.ToLower(rest), "```"); end >= 0 {
			rest = rest[:end]
		}
		candidates = append(candidates, strings.TrimSpace(rest))
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(candidate), &payload); err == nil && len(payload) > 0 {
			return payload, true
		}
	}
	return nil, false
}

func pickActionPayload(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return nil
	}
	keys := []string{"action", "action_call", "call"}
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if m := anyMap(value); len(m) > 0 {
				return normalizeCallActionPayload(m)
			}
		}
	}
	return nil
}

func normalizeCallActionPayload(action map[string]any) map[string]any {
	if len(action) == 0 {
		return nil
	}
	out := clonePayloadMap(action)
	if asTrimmedString(out["type"]) == "" {
		out["type"] = TypeActionCall
	}
	if asTrimmedString(out["path"]) == "" {
		if v := asTrimmedString(out["name"]); v != "" {
			out["path"] = v
		}
	}
	if asTrimmedString(out["followup"]) == "" {
		out["followup"] = "none"
	}
	if args := anyMap(out["args"]); len(args) == 0 {
		out["args"] = map[string]any{}
	}
	return out
}

func formatMalformedPreview(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "消息格式异常"
	}
	runes := []rune(clean)
	limit := 12
	if len(runes) <= limit {
		return "消息格式异常：" + clean
	}
	return fmt.Sprintf("消息格式异常：%s...", string(runes[:limit]))
}
