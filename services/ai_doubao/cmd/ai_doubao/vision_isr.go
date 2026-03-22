package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	app "kagent/services/ai_doubao/internal/app"
)

const defaultVisionSystemPrompt = "你是视觉结构化理解模型。请根据截图内容完成界面理解，并严格输出 JSON。不要输出解释文字。"

func callVisionISR(ctx context.Context, cfg *app.ModelConfig, args map[string]any) (map[string]any, error) {
	chatCfg := cfg.ActiveChat()
	if strings.TrimSpace(chatCfg.APIKey) == "" || strings.TrimSpace(chatCfg.BaseURL) == "" || strings.TrimSpace(chatCfg.Model) == "" {
		return nil, fmt.Errorf("vision model config is incomplete")
	}
	instruction := asString(args["instruction"])
	if instruction == "" {
		return nil, fmt.Errorf("instruction is required")
	}
	images := normalizeVisionImages(args["images"])
	if len(images) == 0 {
		return nil, fmt.Errorf("images is required")
	}
	schema := asMap(args["response_schema"])
	systemPrompt := firstNonEmpty(asString(args["system_prompt"]), defaultVisionSystemPrompt)
	temperature := asFloat(args["temperature"], 0.1)

	requestBody, err := buildVisionRequest(chatCfg, instruction, systemPrompt, images, schema, temperature)
	if err != nil {
		return nil, err
	}
	rawResp, err := doVisionRequest(ctx, chatCfg, requestBody)
	if err != nil {
		return nil, err
	}
	text, finishReason, err := extractVisionResponse(rawResp)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"text":          text,
		"model":         strings.TrimSpace(chatCfg.Model),
		"finish_reason": finishReason,
	}
	if len(schema) > 0 {
		parsed, err := parseVisionJSON(text)
		if err != nil {
			return nil, fmt.Errorf("vision output is not valid json: %w", err)
		}
		result["json"] = parsed
	}
	return result, nil
}

func buildVisionRequest(chatCfg app.ChatConfig, instruction string, systemPrompt string, images []string, schema map[string]any, temperature float64) (map[string]any, error) {
	endpoint := strings.TrimRight(chatCfg.BaseURL, "/")
	if strings.HasSuffix(endpoint, "/responses") {
		input := []map[string]any{
			{
				"role": "system",
				"content": []map[string]any{
					{"type": "input_text", "text": systemPrompt},
				},
			},
			{
				"role":    "user",
				"content": buildVisionResponsesUserContent(instruction, images, schema),
			},
		}
		return map[string]any{
			"model":       chatCfg.Model,
			"input":       input,
			"temperature": temperature,
		}, nil
	}
	messages := []map[string]any{
		{
			"role":    "system",
			"content": systemPrompt,
		},
		{
			"role":    "user",
			"content": buildVisionChatUserContent(instruction, images, schema),
		},
	}
	return map[string]any{
		"model":       chatCfg.Model,
		"messages":    messages,
		"stream":      false,
		"temperature": temperature,
	}, nil
}

func buildVisionPrompt(instruction string, schema map[string]any) string {
	prompt := strings.TrimSpace(instruction)
	if len(schema) == 0 {
		return prompt
	}
	schemaRaw, _ := json.Marshal(schema)
	return prompt + "\n\n请严格返回 JSON，并匹配以下 schema：\n" + string(schemaRaw)
}

func buildVisionChatUserContent(instruction string, images []string, schema map[string]any) []map[string]any {
	content := []map[string]any{
		{
			"type": "text",
			"text": buildVisionPrompt(instruction, schema),
		},
	}
	for _, image := range images {
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": image,
			},
		})
	}
	return content
}

func buildVisionResponsesUserContent(instruction string, images []string, schema map[string]any) []map[string]any {
	content := []map[string]any{
		{
			"type": "input_text",
			"text": buildVisionPrompt(instruction, schema),
		},
	}
	for _, image := range images {
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": image,
		})
	}
	return content
}

func doVisionRequest(ctx context.Context, chatCfg app.ChatConfig, body map[string]any) ([]byte, error) {
	endpoint := strings.TrimRight(chatCfg.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/responses") {
		endpoint += "/chat/completions"
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal vision request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return nil, fmt.Errorf("build vision request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+chatCfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 70 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call vision model: %w", err)
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read vision response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("vision status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}
	return rawResp, nil
}

func extractVisionResponse(raw []byte) (string, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", fmt.Errorf("parse vision response: %w", err)
	}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			finishReason := asString(first["finish_reason"])
			if message, ok := first["message"].(map[string]any); ok {
				text := asString(message["content"])
				if text != "" {
					return text, finishReason, nil
				}
			}
		}
	}
	if output, ok := payload["output"].([]any); ok {
		for _, item := range output {
			message, ok := item.(map[string]any)
			if !ok {
				continue
			}
			content, ok := message["content"].([]any)
			if !ok {
				continue
			}
			for _, part := range content {
				partMap, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if asString(partMap["type"]) == "output_text" {
					return asString(partMap["text"]), asString(payload["status"]), nil
				}
			}
		}
	}
	return "", "", fmt.Errorf("vision response missing text output")
}

func parseVisionJSON(text string) (map[string]any, error) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		if idx := strings.Index(trimmed, "\n"); idx >= 0 {
			trimmed = trimmed[idx+1:]
		}
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	if parsed, err := extractJSONObject(trimmed); err == nil {
		return parsed, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func extractJSONObject(text string) (map[string]any, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("json object not found")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeVisionImages(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if single := asString(value); single != "" {
			if normalized := normalizeVisionImage(single); normalized != "" {
				return []string{normalized}
			}
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if normalized := normalizeVisionImage(asString(item)); normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func normalizeVisionImage(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	if strings.HasPrefix(clean, "data:image/") || strings.HasPrefix(clean, "http://") || strings.HasPrefix(clean, "https://") {
		return clean
	}
	return "data:image/png;base64," + clean
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func asFloat(value any, fallback float64) float64 {
	switch tv := value.(type) {
	case float64:
		return tv
	case float32:
		return float64(tv)
	case int:
		return float64(tv)
	case int64:
		return float64(tv)
	default:
		return fallback
	}
}
