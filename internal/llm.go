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

const chatSurfaceActionPromptSuffix = "" +
	"你可以使用以下动作：get_surfaces、open_surface、close_surface、surface.get_state、surface.call.counter.set_count、surface.call.counter.increment、surface.call.counter.reset。\n" +
	"当你需要动作时，必须只输出 JSON（可以是纯 JSON 或 ```json 代码块），不能在 JSON 外再输出任何解释文字。\n" +
	"格式：{\"content\":\"给用户看的自然语言\",\"action\":{\"id\":\"可选\",\"name\":\"动作名\",\"args\":{\"target\":\"counter\",\"surface_id\":\"counter\",\"count\":number,\"step\":number},\"followup\":\"none|report\"}}\n" +
	"硬性约束：content 只能是给用户看的自然语言，严禁包含 action/args/followup/payload/[action_report]/ai_action.call 等协议字段片段。\n" +
	"流程约束：\n" +
	"1) 用户要求打开某个 surface：先调用 get_surfaces 且 followup=report；拿到列表后若命中目标再调用 open_surface(target) 且 followup=report；若不存在则直接回复找不到且不发动作。\n" +
	"2) 用户要求关闭某个 surface：直接调用 close_surface(target) 且 followup=report。\n" +
	"3) followup 仅允许 none/report；当需要根据动作结果继续推理时必须用 report。\n" +
	"如果不需要动作，输出普通自然文本即可，不要伪造动作执行结果。"

const continuationUserPrompt = "请基于最新的 action_report 继续推理并回复用户。只输出用户可读结论，禁止复述协议字段或 [action_report] 原文。"

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
	systemPrompt := buildChatSystemPrompt(chatCfg.LLM.SystemPrompt)
	// Build the input array: system + history + user input.
	// Some providers require the final message role to be `user`.
	inputArr := buildLLMInputMessages(systemPrompt, history, input)

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

func buildLLMInputMessages(systemPrompt string, history []ChatMessage, input string) []map[string]any {
	inputArr := make([]map[string]any, 0, len(history)+3)
	inputArr = append(inputArr, map[string]any{
		"role":    "system",
		"content": systemPrompt,
	})
	for _, m := range history {
		if !shouldIncludeInLLMHistory(m) {
			continue
		}
		content := strings.TrimSpace(semanticPromptContent(m))
		if content == "" {
			continue
		}
		inputArr = append(inputArr, map[string]any{
			"role":    mapProviderRole(m.Role),
			"content": content,
		})
	}
	cleanInput := strings.TrimSpace(input)
	if cleanInput != "" {
		inputArr = append(inputArr, map[string]any{
			"role":    "user",
			"content": cleanInput,
		})
		return inputArr
	}
	lastRole := ""
	if len(inputArr) > 0 {
		if v, ok := inputArr[len(inputArr)-1]["role"].(string); ok {
			lastRole = strings.ToLower(strings.TrimSpace(v))
		}
	}
	if lastRole != "user" {
		inputArr = append(inputArr, map[string]any{
			"role":    "user",
			"content": continuationUserPrompt,
		})
	}
	return inputArr
}

func shouldIncludeInLLMHistory(msg ChatMessage) bool {
	if strings.TrimSpace(msg.Content) == "" {
		return false
	}
	if msg.Category == CategoryAIAction && msg.MessageType == TypeActionCall {
		return false
	}
	return true
}

func buildChatSystemPrompt(base string) string {
	clean := strings.TrimSpace(base)
	if clean == "" {
		return strings.TrimSpace(chatSurfaceActionPromptSuffix)
	}
	if hasSurfaceActionHints(clean) {
		return clean
	}
	return clean + "\n\n" + strings.TrimSpace(chatSurfaceActionPromptSuffix)
}

func hasSurfaceActionHints(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "get_surfaces") &&
		strings.Contains(lower, "open_surface") &&
		strings.Contains(lower, "close_surface") &&
		strings.Contains(lower, "followup")
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
