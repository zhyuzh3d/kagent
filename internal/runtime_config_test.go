package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeConfigManagerLoadsDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	publicPath := filepath.Join(dir, "config.json")
	userPath := filepath.Join(dir, "user_custom_config.json")

	publicCfg := `{
  "chat": {
    "session": {
      "triggerLLMWaitFinalMs": 480
    },
    "llm": {
      "systemPrompt": "test prompt"
    }
  }
}`
	if err := os.WriteFile(publicPath, []byte(publicCfg), 0o644); err != nil {
		t.Fatalf("write public config: %v", err)
	}
	userCfg := `{
  "schemaVersion": 1,
  "userId": "default",
  "updatedAt": "2026-03-09T12:00:00+08:00",
  "overrides": {
    "chat": {
      "session": {
        "maxHistoryMessages": 9
      }
    }
  }
}`
	if err := os.WriteFile(userPath, []byte(userCfg), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	mgr, err := NewRuntimeConfigManager(publicPath, userPath)
	if err != nil {
		t.Fatalf("new runtime config manager: %v", err)
	}
	got := mgr.Snapshot()
	if got.Chat.Session.TriggerLLMWaitFinalMs != 480 {
		t.Fatalf("unexpected trigger wait: %d", got.Chat.Session.TriggerLLMWaitFinalMs)
	}
	if got.Chat.Session.MaxHistoryMessages != 9 {
		t.Fatalf("unexpected history limit: %d", got.Chat.Session.MaxHistoryMessages)
	}
	if got.Chat.LLM.SystemPrompt != "test prompt" {
		t.Fatalf("unexpected system prompt: %q", got.Chat.LLM.SystemPrompt)
	}
}

func TestRuntimeConfigManagerUpdateWritesOverrides(t *testing.T) {
	dir := t.TempDir()
	publicPath := filepath.Join(dir, "config.json")
	userPath := filepath.Join(dir, "data", "users", "default", "user_custom_config.json")

	if err := os.WriteFile(publicPath, []byte(`{"chat":{"session":{"triggerLLMWaitFinalMs":320}}}`), 0o644); err != nil {
		t.Fatalf("write public config: %v", err)
	}

	mgr, err := NewRuntimeConfigManager(publicPath, userPath)
	if err != nil {
		t.Fatalf("new runtime config manager: %v", err)
	}
	next := mgr.EffectiveMap()
	chatMap := next["chat"].(map[string]any)
	sessionMap := chatMap["session"].(map[string]any)
	sessionMap["triggerLLMWaitFinalMs"] = float64(700)
	sessionMap["unknownField"] = "kept"

	effective, err := mgr.UpdateEffectiveMap(next)
	if err != nil {
		t.Fatalf("update effective config: %v", err)
	}

	updatedChat := effective["chat"].(map[string]any)
	updatedSession := updatedChat["session"].(map[string]any)
	if updatedSession["triggerLLMWaitFinalMs"] != float64(700) {
		t.Fatalf("unexpected updated wait value: %#v", updatedSession["triggerLLMWaitFinalMs"])
	}
	if updatedSession["unknownField"] != "kept" {
		t.Fatalf("expected unknown field to survive merge, got %#v", updatedSession["unknownField"])
	}

	raw, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("read user custom config: %v", err)
	}
	fileMap, err := unmarshalMap(raw)
	if err != nil {
		t.Fatalf("parse user custom config: %v", err)
	}
	overrides := fileMap["overrides"].(map[string]any)
	overrideChat := overrides["chat"].(map[string]any)
	overrideSession := overrideChat["session"].(map[string]any)
	if overrideSession["triggerLLMWaitFinalMs"] != float64(700) {
		t.Fatalf("unexpected override wait: %#v", overrideSession["triggerLLMWaitFinalMs"])
	}
	if overrideSession["unknownField"] != "kept" {
		t.Fatalf("unexpected override unknown field: %#v", overrideSession["unknownField"])
	}
}
