package app

import (
	"strings"

	"kagent/pkg/hubsvc"
)

type ProviderFactory interface {
	Name() string
	NewASRClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) ASRClient
	NewLLMClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) LLMClient
	NewTTSClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) TTSClient
}

type HubProviderFactory struct {
	hubBaseURL  string
	serviceAuth hubsvc.BootstrapSecret
}

func NewHubProviderFactory(hubBaseURL string, serviceAuth hubsvc.BootstrapSecret) *HubProviderFactory {
	return &HubProviderFactory{
		hubBaseURL:  strings.TrimSpace(hubBaseURL),
		serviceAuth: serviceAuth,
	}
}

func (f *HubProviderFactory) Name() string {
	return "hub"
}

func (f *HubProviderFactory) NewASRClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) ASRClient {
	return NewHubASRClient(cfg, runtimeConfig, f.hubBaseURL, f.serviceAuth)
}

func (f *HubProviderFactory) NewLLMClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) LLMClient {
	return NewHubLLMClient(cfg, runtimeConfig, f.hubBaseURL, f.serviceAuth)
}

func (f *HubProviderFactory) NewTTSClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) TTSClient {
	return NewHubTTSClient(cfg, runtimeConfig, f.hubBaseURL, f.serviceAuth)
}
