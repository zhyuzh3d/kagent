package app

import "strings"

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeFollowup(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "report") {
		return "report"
	}
	return "none"
}
