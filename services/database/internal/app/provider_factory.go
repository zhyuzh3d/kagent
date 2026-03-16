package app

import "strings"

type ProviderFactory interface {
	Name() string
	NewASRClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) ASRClient
	NewLLMClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) LLMClient
	NewTTSClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) TTSClient
}

type LocalProviderFactory struct{}

func NewLocalProviderFactory() *LocalProviderFactory {
	return &LocalProviderFactory{}
}

func (f *LocalProviderFactory) Name() string {
	return "local"
}

func (f *LocalProviderFactory) NewASRClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) ASRClient {
	return NewDoubaoASRClient(cfg.ASR, runtimeConfig)
}

func (f *LocalProviderFactory) NewLLMClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) LLMClient {
	return NewDoubaoLLMClient(cfg.ActiveChat(), runtimeConfig)
}

func (f *LocalProviderFactory) NewTTSClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) TTSClient {
	return NewDoubaoTTSClient(cfg.TTS, runtimeConfig)
}

func IsServiceMode(cfg *ModelConfig) bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.EffectiveAIService().Mode), "service")
}
