package app

import "strings"

func nonNilMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	return in
}

func normalizeFollowup(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "report") {
		return "report"
	}
	return "none"
}
