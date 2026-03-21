package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	app "kagent/services/chat_server/internal/app"
)

func structToMap(v any) map[string]any {
	raw, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func firstNonEmpty(items ...string) string {
	for _, it := range items {
		clean := strings.TrimSpace(it)
		if clean != "" {
			return clean
		}
	}
	return ""
}

func writeToolResponse(w http.ResponseWriter, statusCode int, resp app.CallResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}

func asString(v any) string {
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv)
	default:
		return ""
	}
}

func asConfigMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return nil, false
	}
	return m, true
}

func asInt(v any, defaultValue int) int {
	switch tv := v.(type) {
	case int:
		return tv
	case int32:
		return int(tv)
	case int64:
		return int(tv)
	case float32:
		return int(tv)
	case float64:
		return int(tv)
	case json.Number:
		i, err := tv.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		var parsed int
		_, err := fmt.Sscan(strings.TrimSpace(tv), &parsed)
		if err == nil {
			return parsed
		}
	}
	return defaultValue
}
