package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModelConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "configx.json")
	content := `{
  "models": [
    {
      "name": "doubao",
      "config": {
        "flash": {"apiKey":"fk", "baseUrl":"https://flash", "model":"fm"},
        "chat": {"apiKey":"k", "baseUrl":"https://x", "model":"m"},
        "asr_s": {"appId":"a", "accessToken":"b", "resourceId":"c", "wsUrl":"wss://a"},
        "tts_s": {"appId":"a", "accessToken":"b", "resourceId":"c", "wsUrl":"wss://b", "voiceType":"v"}
      }
    }
  ]
}`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg, err := LoadModelConfig(p, "doubao")
	if err != nil {
		t.Fatalf("LoadModelConfig failed: %v", err)
	}
	// ActiveChat should return flash config when present
	active := cfg.ActiveChat()
	if active.Model != "fm" {
		t.Fatalf("expected flash model 'fm', got: %s", active.Model)
	}
	if active.BaseURL != "https://flash" {
		t.Fatalf("expected flash baseURL, got: %s", active.BaseURL)
	}
}

func TestLoadModelConfigWithServiceMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "configx.json")
	content := `{
  "models": [
    {
      "name": "doubao",
      "config": {
        "chat": {"apiKey":"k", "baseUrl":"https://x", "model":"m"},
        "asr_s": {"appId":"a", "accessToken":"b", "resourceId":"c", "wsUrl":"wss://a"},
        "tts_s": {"appId":"a", "accessToken":"b", "resourceId":"c", "wsUrl":"wss://b", "voiceType":"v"},
        "ai_service": {"mode":"service","baseUrl":"http://127.0.0.1:18081"}
      }
    }
  ]
}`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cfg, err := LoadModelConfig(p, "doubao")
	if err != nil {
		t.Fatalf("LoadModelConfig failed: %v", err)
	}
	if mode := cfg.EffectiveAIService().Mode; mode != "service" {
		t.Fatalf("expected ai_service mode service, got %s", mode)
	}
}
