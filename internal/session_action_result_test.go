package app

import (
	"strings"
	"testing"
)

func TestSessionHandleActionResult_PersistAndHistory(t *testing.T) {
	s := &Session{}
	s.handleActionResult(ControlMessage{
		Type:         "action_result",
		TurnID:       11,
		Reason:       "dispatch",
		Text:         "已把数字改成 11",
		ActionID:     "act-11",
		ActionName:   "surface.call.counter.set_count",
		ActionStatus: "ok",
		ActionArgs:   map[string]any{"count": 11},
		ActionResult: map[string]any{"queued": false},
	})

	history := s.getHistory()
	if len(history) < 2 {
		t.Fatalf("history should contain action observation")
	}
	execute := history[len(history)-2]
	last := history[len(history)-1]
	if execute.MessageType != TypeActionExecute {
		t.Fatalf("expected execute before report, got %#v", execute)
	}
	if last.Role != "observer" || !strings.Contains(last.Content, "[action_report]") {
		t.Fatalf("unexpected last history: %#v", last)
	}
	if last.RefMessageID == "" {
		t.Fatalf("expected report ref_message_id, got %#v", last)
	}
}

func TestSummarizeActionResultForReport_PrefersFailureReason(t *testing.T) {
	got := summarizeActionResultForReport("正在关闭界面...", "fail", map[string]any{
		"reason": "surface_closed",
	})
	if !strings.Contains(got, "surface_closed") {
		t.Fatalf("failure summary should carry reason, got=%q", got)
	}
}
