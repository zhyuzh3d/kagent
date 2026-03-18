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

type ASREventType string

const (
	ASREventPartial  ASREventType = "partial"
	ASREventFinal    ASREventType = "final"
	ASREventEndpoint ASREventType = "endpoint"
)

type ASREvent struct {
	Type ASREventType
	Text string
}

type ASRClient interface {
	Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent, history []ChatMessage) error
	Finish()
}

type HubASRClient struct {
	runtimeConfig *RuntimeConfigManager
	toolClient    *HubToolClient
	finishCh      chan struct{}
}

func NewHubASRClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager, hubBaseURL string, serviceAuth hubsvc.BootstrapSecret) *HubASRClient {
	requestTimeout := 70000
	if cfg != nil {
		requestTimeout = cfg.EffectiveAIService().RequestTimeoutMS
	}
	return &HubASRClient{
		runtimeConfig: runtimeConfig,
		toolClient:    NewHubToolClient(hubBaseURL, serviceAuth, durationFromMS(requestTimeout, 70*time.Second)),
		finishCh:      make(chan struct{}, 1),
	}
}

func (c *HubASRClient) Finish() {
	select {
	case c.finishCh <- struct{}{}:
	default:
	}
}

func (c *HubASRClient) Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent, history []ChatMessage) error {
	if c == nil || c.toolClient == nil {
		return fmt.Errorf("asr client is not configured")
	}
	turnID := TurnIDFromContext(ctx)
	// Drain any pending finish signals from a previous run
	select {
	case <-c.finishCh:
	default:
	}
	conn, _, err := c.toolClient.DialToolWS(ctx, "ai.speech.asr", nil, nil)
	if err != nil {
		return fmt.Errorf("dial hub asr ws: %w", err)
	}
	defer conn.Close()

	start := AIServiceASRStart{
		Type:      "start",
		RequestID: "chat-" + newRequestID(),
		TurnID:    turnID,
		History:   history,
	}
	if err := conn.WriteJSON(start); err != nil {
		return fmt.Errorf("send asr start: %w", err)
	}

	writerErrCh := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				writerErrCh <- nil
				return
			case <-c.finishCh:
				_ = conn.WriteJSON(AIServiceASRControl{Type: "finish"})
			case frame, ok := <-audio:
				if !ok {
					writerErrCh <- nil
					return
				}
				if len(frame) == 0 {
					continue
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
					writerErrCh <- err
					return
				}
			}
		}
	}()

	readTimeout := durationFromMS(c.chatConfig().ASR.ReadTimeoutMs, 60*time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-writerErrCh:
			if err != nil {
				return err
			}
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				return nil
			}
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			return fmt.Errorf("read asr ws: %w", err)
		}
		var evt AIServiceASREvent
		if err := json.Unmarshal(payload, &evt); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(evt.Type)) {
		case "partial":
			events <- ASREvent{Type: ASREventPartial, Text: evt.Text}
		case "final":
			events <- ASREvent{Type: ASREventFinal, Text: evt.Text}
		case "endpoint":
			events <- ASREvent{Type: ASREventEndpoint, Text: evt.Text}
		case "error":
			return fmt.Errorf("asr failed: %s", strings.TrimSpace(evt.Error))
		}
	}
}

func (c *HubASRClient) chatConfig() ChatPublicConfig {
	if c.runtimeConfig != nil {
		return c.runtimeConfig.Snapshot().Chat
	}
	return defaultPublicConfig().Chat
}
