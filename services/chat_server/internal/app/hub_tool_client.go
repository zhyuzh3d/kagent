package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"

	"github.com/gorilla/websocket"
)

type HubToolClient struct {
	baseURL     string
	serviceAuth hubsvc.BootstrapSecret
	httpClient  *http.Client
}

func NewHubToolClient(baseURL string, serviceAuth hubsvc.BootstrapSecret, timeout time.Duration) *HubToolClient {
	if timeout <= 0 {
		timeout = 70 * time.Second
	}
	return &HubToolClient{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		serviceAuth: serviceAuth,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func (c *HubToolClient) Call(ctx context.Context, toolID string, args map[string]any, timeoutMS int) (map[string]any, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("hub tool client is not configured")
	}
	reqBody := CallRequest{
		ToolID: strings.TrimSpace(toolID),
		Args:   args,
		Context: &CallContext{
			TimeoutMS: timeoutMS,
		},
	}
	hubsvc.AttachDelegationFromContext((*toolproto.Context)(reqBody.Context), ctx)
	raw, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/tool/call", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(req.Header, c.serviceAuth)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out CallResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tool call response: %w", err)
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
	if m, ok := out.Result.(map[string]any); ok {
		return m, nil
	}
	rawResult, _ := json.Marshal(out.Result)
	m := map[string]any{}
	_ = json.Unmarshal(rawResult, &m)
	return m, nil
}

func (c *HubToolClient) DialToolWS(ctx context.Context, toolID string, query map[string]string, headers http.Header) (*websocket.Conn, *http.Response, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil, fmt.Errorf("hub tool client is not configured")
	}
	wsURL, err := c.buildWSURL(toolID, query)
	if err != nil {
		return nil, nil, err
	}
	h := http.Header{}
	for key, values := range headers {
		for _, v := range values {
			h.Add(key, v)
		}
	}
	hubsvc.ApplyServiceAuthHeaders(h, c.serviceAuth)
	hubsvc.ApplyDelegationHeaders(h, ctx)
	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, wsURL, h)
	if err != nil {
		return nil, resp, err
	}
	return conn, resp, nil
}

func (c *HubToolClient) buildWSURL(toolID string, query map[string]string) (string, error) {
	rawBase := strings.TrimSpace(c.baseURL)
	if rawBase == "" {
		return "", fmt.Errorf("hub base url is empty")
	}
	if !strings.Contains(rawBase, "://") {
		rawBase = "http://" + rawBase
	}
	parsed, err := url.Parse(rawBase)
	if err != nil {
		return "", fmt.Errorf("invalid hub base url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported hub scheme: %s", parsed.Scheme)
	}
	parsed.Path = "/api/tool/ws"
	q := parsed.Query()
	q.Set("tool_id", strings.TrimSpace(toolID))
	for key, value := range query {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		q.Set(k, strings.TrimSpace(value))
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}
