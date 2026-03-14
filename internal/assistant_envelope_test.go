package app

import "testing"

func TestParseAssistantEnvelopeJSON(t *testing.T) {
	out := parseAssistantEnvelope(`{"say":"你好","aside":"（准备执行）","action":{"type":"call","id":"act-1","path":"get_surfaces","args":{},"followup":"report"}}`)
	if out.ParseError != "" {
		t.Fatalf("unexpected parse error: %s", out.ParseError)
	}
	if out.Say != "你好" {
		t.Fatalf("unexpected say: %q", out.Say)
	}
	if out.Aside != "（准备执行）" {
		t.Fatalf("unexpected aside: %q", out.Aside)
	}
	if out.ActionJSON == "" {
		t.Fatalf("expected action_json")
	}
}

func TestParseAssistantEnvelopeMalformed(t *testing.T) {
	out := parseAssistantEnvelope("{\"say\":\"abc\",")
	if out.ParseError == "" {
		t.Fatalf("expected parse error for malformed envelope")
	}
	if out.Say == "" {
		t.Fatalf("expected fallback preview text")
	}
	if out.RawData == "" {
		t.Fatalf("expected raw_data")
	}
}
