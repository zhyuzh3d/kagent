package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func firstRow(rows []map[string]any) map[string]any {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

func asStringValue(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	raw, ok := row[key]
	if !ok || raw == nil {
		return ""
	}
	switch tv := raw.(type) {
	case string:
		return strings.TrimSpace(tv)
	default:
		return strings.TrimSpace(fmt.Sprint(tv))
	}
}

func asInt64Value(row map[string]any, key string) int64 {
	if row == nil {
		return 0
	}
	raw, ok := row[key]
	if !ok || raw == nil {
		return 0
	}
	switch tv := raw.(type) {
	case int:
		return int64(tv)
	case int32:
		return int64(tv)
	case int64:
		return tv
	case float32:
		return int64(tv)
	case float64:
		return int64(tv)
	case json.Number:
		i, _ := tv.Int64()
		return i
	case string:
		var out int64
		_, _ = fmt.Sscan(strings.TrimSpace(tv), &out)
		return out
	default:
		return 0
	}
}

func asIntValue(row map[string]any, key string) int {
	return int(asInt64Value(row, key))
}

func (s *HubDatabaseStore) query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if ctx == nil {
		ctx = s.baseCtx
	}
	result, err := s.client.Call(ctx, "storage.database.query", map[string]any{
		"query":        strings.TrimSpace(query),
		"args":         args,
		"scope_source": "origin",
	}, 60000)
	if err != nil {
		return nil, err
	}
	rawRows, ok := result["rows"]
	if !ok || rawRows == nil {
		return []map[string]any{}, nil
	}
	list, ok := rawRows.([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *HubDatabaseStore) execute(ctx context.Context, query string, args ...any) error {
	if ctx == nil {
		ctx = s.baseCtx
	}
	_, err := s.client.Call(ctx, "storage.database.execute", map[string]any{
		"query":        strings.TrimSpace(query),
		"args":         args,
		"scope_source": "origin",
	}, 60000)
	return err
}
