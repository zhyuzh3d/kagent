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

// ChatMessage represents a single message in the conversation history.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMClient interface {
	Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error)
}

type DoubaoLLMClient struct {
	cfg           ChatConfig
	runtimeConfig *RuntimeConfigManager
}

func NewDoubaoLLMClient(cfg ChatConfig, runtimeConfig *RuntimeConfigManager) *DoubaoLLMClient {
	return &DoubaoLLMClient{
		cfg:           cfg,
		runtimeConfig: runtimeConfig,
	}
}

func (c *DoubaoLLMClient) Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error) {
	chatCfg := c.chatConfig()
	// Build the input array: system + history + current user message
	inputArr := make([]map[string]any, 0, len(history)+2)
	inputArr = append(inputArr, map[string]any{
		"role":    "system",
		"content": chatCfg.LLM.SystemPrompt,
	})
	for _, m := range history {
		inputArr = append(inputArr, map[string]any{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	inputArr = append(inputArr, map[string]any{
		"role":    "user",
		"content": input,
	})

	reqBody := map[string]any{
		"model":  c.cfg.Model,
		"stream": true,
		"input":  inputArr,
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/")
	// If the baseUrl already ends with /responses, use it directly.
	// Otherwise append /chat/completions for backwards compatibility.
	if !strings.HasSuffix(endpoint, "/responses") {
		endpoint += "/chat/completions"
	}

	b, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("build llm request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: durationFromMS(chatCfg.LLM.StreamTimeoutMs, 65*time.Second)}
	resp, err := client.Do(req)
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

func (c *DoubaoLLMClient) chatConfig() ChatPublicConfig {
	if c.runtimeConfig != nil {
		return c.runtimeConfig.Snapshot().Chat
	}
	return defaultPublicConfig().Chat
}

func parseLLMSSE(ctx context.Context, body io.Reader, onDelta func(string)) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var finalBuilder strings.Builder
	var currentEvent string

	for scanner.Scan() {
		if ctx.Err() != nil {
			return strings.TrimSpace(finalBuilder.String()), ctx.Err()
		}
		line := scanner.Text()

		// SSE event type line
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}

		// Skip comments and empty lines
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ":") {
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

		// Handle Responses API events
		if currentEvent != "" {
			delta := extractResponsesDelta(m, currentEvent)
			if delta != "" {
				finalBuilder.WriteString(delta)
				onDelta(delta)
			}
			if currentEvent == "response.completed" {
				break
			}
			currentEvent = ""
			continue
		}

		// Fallback: Chat Completions API format
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

// extractResponsesDelta extracts text delta from Responses API SSE events.
func extractResponsesDelta(m map[string]any, event string) string {
	switch event {
	case "response.output_text.delta":
		return asString(m["delta"])
	case "response.content_part.delta":
		return asString(m["delta"])
	default:
		return ""
	}
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
		// Also try Responses API non-stream format
		final = extractResponsesFinal(m)
	}
	if final == "" {
		return "", fmt.Errorf("llm response does not contain text")
	}
	return final, nil
}

// extractResponsesFinal extracts text from non-streaming Responses API response.
func extractResponsesFinal(m map[string]any) string {
	output, ok := m["output"].([]any)
	if !ok {
		return ""
	}
	for _, item := range output {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if asString(im["type"]) != "message" {
			continue
		}
		content, ok := im["content"].([]any)
		if !ok {
			continue
		}
		for _, c := range content {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if asString(cm["type"]) == "output_text" {
				return asString(cm["text"])
			}
		}
	}
	return ""
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
