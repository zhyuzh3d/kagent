package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"kagent/pkg/hubsvc"

	"github.com/gorilla/websocket"
)

type LLMClient interface {
	Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error)
}

type HubLLMClient struct {
	toolClient *HubToolClient
}

func NewHubLLMClient(cfg *ModelConfig, _ *RuntimeConfigManager, hubBaseURL string, serviceAuth hubsvc.BootstrapSecret) *HubLLMClient {
	requestTimeout := 70000
	if cfg != nil {
		requestTimeout = cfg.EffectiveAIService().RequestTimeoutMS
	}
	return &HubLLMClient{
		toolClient: NewHubToolClient(hubBaseURL, serviceAuth, durationFromMS(requestTimeout, 70*time.Second)),
	}
}

func (c *HubLLMClient) Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error) {
	if c == nil || c.toolClient == nil {
		return "", fmt.Errorf("llm client is not configured")
	}
	conn, _, err := c.toolClient.DialToolWS(ctx, "ai.llm.stream", nil, nil)
	if err != nil {
		return "", fmt.Errorf("dial hub llm ws: %w", err)
	}
	defer conn.Close()

	req := AIServiceLLMStreamRequest{
		RequestID: "chat-" + newRequestID(),
		TurnID:    TurnIDFromContext(ctx),
		Input:     strings.TrimSpace(input),
		History:   history,
	}
	if err := conn.WriteJSON(req); err != nil {
		return "", fmt.Errorf("send llm request: %w", err)
	}

	var builder strings.Builder
	for {
		select {
		case <-ctx.Done():
			return strings.TrimSpace(builder.String()), ctx.Err()
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(65 * time.Second))
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return strings.TrimSpace(builder.String()), ctx.Err()
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				return strings.TrimSpace(builder.String()), nil
			}
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			return "", fmt.Errorf("read llm ws: %w", err)
		}
		var evt AIServiceLLMStreamEvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(evt.Type)) {
		case "delta":
			if strings.TrimSpace(evt.Text) == "" {
				continue
			}
			builder.WriteString(evt.Text)
			if onDelta != nil {
				onDelta(evt.Text)
			}
		case "final":
			finalText := strings.TrimSpace(firstNonEmpty(evt.Text, builder.String()))
			return finalText, nil
		case "error":
			return "", fmt.Errorf("llm failed: %s", strings.TrimSpace(evt.Error))
		}
	}
}
