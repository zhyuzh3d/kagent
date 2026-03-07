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
	Chat ChatConfig `json:"chat"`
	ASR  ASRConfig  `json:"asr_s"`
	TTS  TTSConfig  `json:"tts_s"`
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
	if cfg.Chat.APIKey == "" || cfg.Chat.BaseURL == "" || cfg.Chat.Model == "" {
		return errors.New("chat config is incomplete")
	}
	if cfg.ASR.WSURL == "" || cfg.ASR.AppID == "" || cfg.ASR.AccessToken == "" {
		return errors.New("asr_s config is incomplete")
	}
	if cfg.TTS.WSURL == "" || cfg.TTS.AppID == "" || cfg.TTS.AccessToken == "" || cfg.TTS.VoiceType == "" {
		return errors.New("tts_s config is incomplete")
	}
	return nil
}
