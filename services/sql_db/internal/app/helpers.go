package app

import (
	"encoding/json"
	"strings"
)

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeFollowup(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "report") {
		return "report"
	}
	return "none"
}
