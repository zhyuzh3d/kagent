package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LLMClient interface {
	Stream(ctx context.Context, input string, onDelta func(string)) (string, error)
}

type DoubaoLLMClient struct {
	cfg    ChatConfig
	client *http.Client
}

func NewDoubaoLLMClient(cfg ChatConfig) *DoubaoLLMClient {
	return &DoubaoLLMClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: 65 * time.Second,
		},
	}
}

func (c *DoubaoLLMClient) Stream(ctx context.Context, input string, onDelta func(string)) (string, error) {
	reqBody := map[string]any{
		"model":  c.cfg.Model,
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": input},
		},
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("build llm request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call llm stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("llm stream status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		return parseLLMSSE(ctx, resp.Body, onDelta)
	}
	return parseLLMNonStream(resp.Body)
}

func parseLLMSSE(ctx context.Context, body io.Reader, onDelta func(string)) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var finalBuilder strings.Builder

	for scanner.Scan() {
		if ctx.Err() != nil {
			return strings.TrimSpace(finalBuilder.String()), ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		m, err := unmarshalMap([]byte(data))
		if err != nil {
			continue
		}
		typ := strings.ToLower(firstNonEmpty(asString(m["type"]), asString(m["event"])))
		delta := extractLLMDelta(m, typ)
		if delta != "" {
			finalBuilder.WriteString(delta)
			onDelta(delta)
			continue
		}
		if lowerContainsAny(typ, "completed", "done", "final") {
			final := extractLLMFinal(m)
			if final != "" && finalBuilder.Len() == 0 {
				finalBuilder.WriteString(final)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read llm stream: %w", err)
	}
	return strings.TrimSpace(finalBuilder.String()), nil
}

func parseLLMNonStream(body io.Reader) (string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("read llm response: %w", err)
	}
	m, err := unmarshalMap(raw)
	if err != nil {
		return "", fmt.Errorf("parse llm response: %w", err)
	}
	final := extractLLMFinal(m)
	if final == "" {
		return "", fmt.Errorf("llm response does not contain text")
	}
	return final, nil
}

func extractLLMDelta(m map[string]any, _ string) string {
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		if ch, ok := choices[0].(map[string]any); ok {
			if delta, ok := ch["delta"].(map[string]any); ok {
				return asString(delta["content"])
			}
		}
	}
	return ""
}

func extractLLMFinal(m map[string]any) string {
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		if ch, ok := choices[0].(map[string]any); ok {
			if msg, ok := ch["message"].(map[string]any); ok {
				return asString(msg["content"])
			}
		}
	}
	return ""
}
