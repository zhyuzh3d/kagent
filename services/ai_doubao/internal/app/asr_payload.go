package app

import "strings"

func parseASRPayload(raw []byte, finalHint bool) []ASREvent {
	m, err := unmarshalMap(raw)
	if err != nil {
		return nil
	}
	text := extractASRText(m)
	if text == "" {
		return nil
	}
	isFinal := finalHint || asBool(m["is_final"]) || asBool(m["final"]) || hasDefiniteUtterance(m)
	if isFinal {
		return []ASREvent{{Type: ASREventFinal, Text: text}}
	}
	return []ASREvent{{Type: ASREventPartial, Text: text}}
}

func extractASRText(m map[string]any) string {
	if result, ok := m["result"].(map[string]any); ok {
		if t := asString(result["text"]); t != "" {
			return t
		}
	}
	keys := map[string]struct{}{
		"text":       {},
		"result":     {},
		"transcript": {},
		"utterance":  {},
		"sentence":   {},
		"content":    {},
	}
	items := make([]string, 0, 8)
	collectStringsByKeys(m, keys, &items)
	uniq := uniqueNonEmpty(items)
	if len(uniq) == 0 {
		return ""
	}
	return uniq[0]
}

func hasDefiniteUtterance(m map[string]any) bool {
	result, ok := m["result"].(map[string]any)
	if !ok {
		return false
	}
	utterances, ok := result["utterances"].([]any)
	if !ok {
		return false
	}
	for _, item := range utterances {
		u, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if asBool(u["definite"]) {
			return true
		}
	}
	return false
}

func asString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asBool(v any) bool {
	b, ok := v.(bool)
	return ok && b
}
