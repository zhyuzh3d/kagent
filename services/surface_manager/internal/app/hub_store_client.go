package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func (s *HubStore) databaseQuery(ctx context.Context, query string, args []any) ([]map[string]any, error) {
	result, err := s.callTool(ctx, "storage.database.query", map[string]any{
		"db_name": surfaceDBName,
		"query":   strings.TrimSpace(query),
		"args":    args,
	})
	if err != nil {
		return nil, err
	}
	rawRows, ok := result["rows"]
	if !ok || rawRows == nil {
		return []map[string]any{}, nil
	}
	items, ok := rawRows.([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *HubStore) databaseExecute(ctx context.Context, query string, args []any) (map[string]any, error) {
	return s.callTool(ctx, "storage.database.execute", map[string]any{
		"db_name": surfaceDBName,
		"query":   strings.TrimSpace(query),
		"args":    args,
	})
}

func (s *HubStore) shareWrite(ctx context.Context, args map[string]any) (map[string]any, error) {
	return s.callTool(ctx, "storage.share.write", args)
}

func (s *HubStore) shareRead(ctx context.Context, args map[string]any) ([]map[string]any, error) {
	result, err := s.callTool(ctx, "storage.share.read", args)
	if err != nil {
		return nil, err
	}
	rawItems, ok := result["items"]
	if !ok || rawItems == nil {
		return []map[string]any{}, nil
	}
	items, ok := rawItems.([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *HubStore) callTool(ctx context.Context, toolID string, args map[string]any) (map[string]any, error) {
	if s == nil || s.toolCallURL == "" {
		return nil, fmt.Errorf("hub store is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callReq := toolproto.CallRequest{
		ToolID: strings.TrimSpace(toolID),
		Args:   args,
		Context: &toolproto.Context{
			RequestID: "req_" + newRequestID(),
			TraceID:   "tr_" + newRequestID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: strings.TrimSpace(s.serviceID),
			},
		},
	}
	hubsvc.AttachDelegationFromContext(callReq.Context, ctx)
	rawReq, err := json.Marshal(callReq)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.toolCallURL, bytes.NewReader(rawReq))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(httpReq.Header, s.serviceAuth)
	httpResp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	var out toolproto.CallResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tool response: %w", err)
	}
	if !out.Ok {
		if out.Error == nil {
			return nil, fmt.Errorf("tool call failed")
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(out.Error.Message))
	}
	if out.Result == nil {
		return map[string]any{}, nil
	}
	if result, ok := out.Result.(map[string]any); ok {
		return result, nil
	}
	rawResult, _ := json.Marshal(out.Result)
	result := map[string]any{}
	_ = json.Unmarshal(rawResult, &result)
	return result, nil
}

func asMapString(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	switch t := row[key].(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		if t == nil {
			return ""
		}
		return fmt.Sprintf("%v", t)
	}
}

func asMapInt64(row map[string]any, key string) int64 {
	if row == nil {
		return 0
	}
	switch t := row[key].(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		v, _ := t.Int64()
		return v
	case string:
		var v int64
		_, _ = fmt.Sscan(strings.TrimSpace(t), &v)
		return v
	default:
		return 0
	}
}
