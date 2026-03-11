package app

import (
	"strings"
	"testing"
)

func TestBuildChatSystemPrompt_AppendsSuffix(t *testing.T) {
	base := "你是语音助手。"
	got := buildChatSystemPrompt(base)
	if !strings.Contains(got, base) {
		t.Fatalf("base prompt missing, got=%q", got)
	}
	if !strings.Contains(got, "surface.call.counter.") {
		t.Fatalf("action suffix missing, got=%q", got)
	}
}

func TestBuildChatSystemPrompt_BackfillsMissingGetStateHint(t *testing.T) {
	base := "你是语音助手。支持 surface.call.counter.set_count。"
	got := buildChatSystemPrompt(base)
	if !strings.Contains(got, "surface.get_state") || !strings.Contains(got, "followup") {
		t.Fatalf("prompt should include get_state/followup hint, got=%q", got)
	}
}

func TestBuildChatSystemPrompt_NoDuplicateWhenAlreadyRich(t *testing.T) {
	base := "你是语音助手。支持 surface.call.counter.set_count 和 surface.get_state，action 带 followup 字段。"
	got := buildChatSystemPrompt(base)
	if got != strings.TrimSpace(base) {
		t.Fatalf("prompt should stay unchanged, got=%q", got)
	}
}

func TestBuildLLMInputMessages_AddsContinuationUserTail(t *testing.T) {
	msgs := buildLLMInputMessages("sys", []ChatMessage{
		{Role: "user", Content: "你现在能看到 counter 数字是多少？"},
		{Role: "assistant", Content: "我来看看"},
		{Role: "observer", Content: "[action_report] name=surface.get_state status=ok"},
	}, "")
	if len(msgs) < 2 {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
	last := msgs[len(msgs)-1]
	if last["role"] != "user" {
		t.Fatalf("last role should be user, got=%#v", last)
	}
	content, _ := last["content"].(string)
	if !strings.Contains(content, "action_report") {
		t.Fatalf("continuation tail should mention action_report, got=%q", content)
	}
}

func TestBuildLLMInputMessages_UsesUserInputWhenProvided(t *testing.T) {
	msgs := buildLLMInputMessages("sys", []ChatMessage{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好，我在。"},
	}, "现在是多少")
	last := msgs[len(msgs)-1]
	if last["role"] != "user" || last["content"] != "现在是多少" {
		t.Fatalf("unexpected user tail: %#v", last)
	}
}
