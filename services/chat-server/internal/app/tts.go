package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
)

type TTSClient interface {
	Synthesize(ctx context.Context, text string) ([]byte, string, error)
}

type HubTTSClient struct {
	toolClient *HubToolClient
}

func NewHubTTSClient(cfg *ModelConfig, _ *RuntimeConfigManager, hubBaseURL string, serviceAuth hubsvc.BootstrapSecret) *HubTTSClient {
	requestTimeout := 70000
	if cfg != nil {
		requestTimeout = cfg.EffectiveAIService().RequestTimeoutMS
	}
	return &HubTTSClient{
		toolClient: NewHubToolClient(hubBaseURL, serviceAuth, durationFromMS(requestTimeout, 70*time.Second)),
	}
}

func (c *HubTTSClient) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	cleanText := strings.TrimSpace(text)
	if cleanText == "" {
		return nil, "", fmt.Errorf("empty tts text")
	}
	if c == nil || c.toolClient == nil {
		return nil, "", fmt.Errorf("tts client is not configured")
	}
	result, err := c.toolClient.Call(ctx, "ai.speech.tts", map[string]any{
		"text": cleanText,
	}, int(durationFromMS(35_000, 35*time.Second).Milliseconds()))
	if err != nil {
		return nil, "", err
	}
	rawAudio := strings.TrimSpace(asStringAny(result["audio_base64"]))
	if rawAudio == "" {
		return nil, "", fmt.Errorf("tts response missing audio_base64")
	}
	audio, err := base64.StdEncoding.DecodeString(rawAudio)
	if err != nil {
		return nil, "", fmt.Errorf("decode tts audio: %w", err)
	}
	format := strings.TrimSpace(asStringAny(result["format"]))
	if format == "" {
		format = "audio/mpeg"
	}
	return audio, format, nil
}

func asStringAny(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	default:
		return fmt.Sprint(tv)
	}
}
