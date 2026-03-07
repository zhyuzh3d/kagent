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
	if cfg.Chat.Model != "m" {
		t.Fatalf("unexpected model: %s", cfg.Chat.Model)
	}
}
