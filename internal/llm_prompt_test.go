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
	if !strings.Contains(got, "get_surfaces") || !strings.Contains(got, "open_surface") || !strings.Contains(got, "close_surface") {
		t.Fatalf("action suffix missing, got=%q", got)
	}
}

func TestBuildChatSystemPrompt_BackfillsMissingGetStateHint(t *testing.T) {
	base := "你是语音助手。支持 surface.call.counter.set_count。"
	got := buildChatSystemPrompt(base)
	if !strings.Contains(got, "surface.get_state") || !strings.Contains(got, "get_surfaces") || !strings.Contains(got, "followup") {
		t.Fatalf("prompt should include get/open/close/followup hints, got=%q", got)
	}
}

func TestBuildChatSystemPrompt_NoDuplicateWhenAlreadyRich(t *testing.T) {
	base := "你是语音助手。支持 get_surfaces/open_surface/close_surface 和 surface.get_state，action 带 followup 字段。"
	got := buildChatSystemPrompt(base)
	if got != strings.TrimSpace(base) {
		t.Fatalf("prompt should stay unchanged, got=%q", got)
	}
}

func TestBuildLLMInputMessages_AddsContinuationUserTail(t *testing.T) {
	msgs := buildLLMInputMessages("sys", []ChatMessage{
		{Role: "user", Content: "你现在能看到 counter 数字是多少？"},
		{Role: "assistant", Content: "我来看看"},
		{Role: "observer", Category: CategoryAIAction, MessageType: TypeActionCall, Content: "准备执行动作：surface.get_state"},
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
	if !strings.Contains(content, "observer") {
		t.Fatalf("continuation tail should mention observer events, got=%q", content)
	}
	for _, msg := range msgs {
		text, _ := msg["content"].(string)
		if strings.Contains(text, "准备执行动作") {
			t.Fatalf("action_call template should be excluded from llm history, got=%#v", msgs)
		}
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

func TestBuildLLMInputMessages_PrefixesSemanticTime(t *testing.T) {
	msgs := buildLLMInputMessages("sys", []ChatMessage{
		{
			Role:                  RoleObserver,
			Content:               "counter 当前状态：{\"count\":9}。",
			CreatedAtLocalYMDHMS:  "2026年03月11日 15:32:10",
			CreatedAtLocalWeekday: "星期三",
			CreatedAtLocalLunar:   "农历二月廿二",
		},
	}, "")
	if len(msgs) < 2 {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
	historyMsg := msgs[1]
	content, _ := historyMsg["content"].(string)
	if !strings.Contains(content, "2026年03月11日 15:32:10 星期三 农历二月廿二") {
		t.Fatalf("semantic time prefix missing: %#v", historyMsg)
	}
}
