package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type RawConfig struct {
	Models []ModelEntry `json:"models"`
}

type ModelEntry struct {
	Name   string      `json:"name"`
	Config ModelConfig `json:"config"`
}

type ModelConfig struct {
	Flash     ChatConfig      `json:"flash"`
	Chat      ChatConfig      `json:"chat"`
	ASR       ASRConfig       `json:"asr_s"`
	TTS       TTSConfig       `json:"tts_s"`
	AIService AIServiceConfig `json:"ai_service"`
}

// ActiveChat returns the Flash config if it is fully specified,
// otherwise falls back to the Chat config.
func (m *ModelConfig) ActiveChat() ChatConfig {
	if m.Flash.APIKey != "" && m.Flash.BaseURL != "" && m.Flash.Model != "" {
		return m.Flash
	}
	return m.Chat
}

type ChatConfig struct {
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
}

type ASRConfig struct {
	AppID       string `json:"appId"`
	AccessToken string `json:"accessToken"`
	ResourceID  string `json:"resourceId"`
	WSURL       string `json:"wsUrl"`
}

type TTSConfig struct {
	AppID       string `json:"appId"`
	AccessToken string `json:"accessToken"`
	ResourceID  string `json:"resourceId"`
	WSURL       string `json:"wsUrl"`
	VoiceType   string `json:"voiceType"`
}

type AIServiceConfig struct {
	Mode                 string   `json:"mode"`
	BaseURL              string   `json:"baseUrl"`
	AutoStart            bool     `json:"autoStart"`
	StartCommand         []string `json:"startCommand"`
	HealthIntervalMS     int      `json:"healthIntervalMs"`
	HealthTimeoutMS      int      `json:"healthTimeoutMs"`
	RequestTimeoutMS     int      `json:"requestTimeoutMs"`
	StartupGracePeriodMS int      `json:"startupGracePeriodMs"`
}

func (m *ModelConfig) EffectiveAIService() AIServiceConfig {
	cfg := m.AIService
	if cfg.Mode == "" {
		cfg.Mode = "local"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:18081"
	}
	if cfg.HealthIntervalMS <= 0 {
		cfg.HealthIntervalMS = 5000
	}
	if cfg.HealthTimeoutMS <= 0 {
		cfg.HealthTimeoutMS = 1500
	}
	if cfg.RequestTimeoutMS <= 0 {
		cfg.RequestTimeoutMS = 70000
	}
	if cfg.StartupGracePeriodMS <= 0 {
		cfg.StartupGracePeriodMS = 6000
	}
	return cfg
}

func LoadModelConfig(path string, modelName string) (*ModelConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var raw RawConfig
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if len(raw.Models) == 0 {
		return nil, errors.New("config models is empty")
	}
	for i := range raw.Models {
		if raw.Models[i].Name == modelName {
			if err := validateModelConfig(&raw.Models[i].Config); err != nil {
				return nil, err
			}
			return &raw.Models[i].Config, nil
		}
	}
	return nil, fmt.Errorf("model %q not found in config", modelName)
}

func validateModelConfig(cfg *ModelConfig) error {
	active := cfg.ActiveChat()
	if active.APIKey == "" || active.BaseURL == "" || active.Model == "" {
		return errors.New("chat/flash config is incomplete")
	}
	if cfg.ASR.WSURL == "" || cfg.ASR.AppID == "" || cfg.ASR.AccessToken == "" {
		return errors.New("asr_s config is incomplete")
	}
	if cfg.TTS.WSURL == "" || cfg.TTS.AppID == "" || cfg.TTS.AccessToken == "" || cfg.TTS.VoiceType == "" {
		return errors.New("tts_s config is incomplete")
	}
	svc := cfg.EffectiveAIService()
	if svc.Mode != "local" && svc.Mode != "service" {
		return fmt.Errorf("ai_service.mode must be local or service")
	}
	if svc.Mode == "service" && svc.BaseURL == "" {
		return fmt.Errorf("ai_service.baseUrl is required in service mode")
	}
	return nil
}
