package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeLLM struct {
	deltas []string
}

func (f *fakeLLM) Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error) {
	for _, d := range f.deltas {
		onDelta(d)
	}
	return "", nil
}

type fakeTTS struct{}

func (f *fakeTTS) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	return []byte("audio-" + text), "audio/mpeg", nil
}

type selectiveFakeTTS struct {
	failOn map[string]error
	calls  []string
}

func (f *selectiveFakeTTS) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	f.calls = append(f.calls, text)
	if err, ok := f.failOn[text]; ok {
		return nil, "", err
	}
	return []byte("audio-" + text), "audio/mpeg", nil
}

func TestSentenceSegmenter(t *testing.T) {
	s := NewSentenceSegmenter()
	out := s.Push("哈哈，看来心情")
	if len(out) != 0 {
		t.Fatalf("should wait for sentence break, got %#v", out)
	}
	out = s.Push("不错呢。还有别的")
	if len(out) != 1 || out[0] != "哈哈，看来心情不错呢。" {
		t.Fatalf("unexpected sentence output: %#v", out)
	}
	out = s.Push("问题吗？")
	if len(out) != 1 || out[0] != "还有别的问题吗？" {
		t.Fatalf("unexpected second sentence output: %#v", out)
	}
}

func TestSelectSentenceGroupByBacklog(t *testing.T) {
	sentences := []string{"第一句。", "第二句。", "第三句。", "第四句。"}

	group, used := selectSentenceGroup(sentences, 0, false)
	if used != 1 || group != "第一句。" {
		t.Fatalf("expected single-sentence group, got used=%d group=%q", used, group)
	}

	group, used = selectSentenceGroup(sentences, 3500, false)
	if used != 2 || group != "第一句。第二句。" {
		t.Fatalf("expected two-sentence group, got used=%d group=%q", used, group)
	}

	group, used = selectSentenceGroup(sentences, 6500, false)
	if used != 3 || group != "第一句。第二句。第三句。" {
		t.Fatalf("expected three-sentence group, got used=%d group=%q", used, group)
	}
}

func TestSelectSentenceGroupRespectsRuneLimit(t *testing.T) {
	sentences := []string{
		"这是第一句比较长的话语已经明显超过限制。",
		"这是第二句比较长的话语同样明显超过限制。",
		"第三句。",
	}

	group, used := selectSentenceGroup(sentences, 3500, false)
	if used != 1 || group != sentences[0] {
		t.Fatalf("expected fallback to one sentence under rune limit, got used=%d group=%q", used, group)
	}

	group, used = selectSentenceGroup(sentences, 6500, true)
	if used != 3 || group != sentences[0]+sentences[1]+sentences[2] {
		t.Fatalf("expected flush mode to take three sentences within rune limit, got used=%d group=%q", used, group)
	}
}

func TestPunctuationOnlySegmentsAreSkipped(t *testing.T) {
	if !isPunctuationOnly("，？！ ~") {
		t.Fatalf("expected punctuation-only text to be skipped")
	}
	if isPunctuationOnly("哈哈，") {
		t.Fatalf("text with letters should not be treated as punctuation-only")
	}
}

func TestTurnPipelineRun(t *testing.T) {
	chunks := 0
	p := NewTurnPipeline(&fakeLLM{deltas: []string{"你好，世界。", "今天天气不错。"}}, &fakeTTS{}, nil, TurnCallbacks{
		OnStatus: func(turnID uint64, state string, detail string) {},
		OnEvent:  func(evt EventMessage) {},
		OnChunk: func(chunk TTSChunk) error {
			chunks++
			return nil
		},
	})
	if err := p.RunTurn(context.Background(), 1, "hi", nil); err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
	if chunks == 0 {
		t.Fatalf("expected chunks > 0")
	}
}

func TestTurnPipelineContinuesAfterSegmentFailure(t *testing.T) {
	tts := &selectiveFakeTTS{
		failOn: map[string]error{
			"第一句。": errors.New("mock fail"),
		},
	}
	var chunks []string
	var warnEvents []EventMessage
	p := NewTurnPipeline(&fakeLLM{deltas: []string{"第一句。", "第二句。", "第三句。"}}, tts, nil, TurnCallbacks{
		OnStatus: func(turnID uint64, state string, detail string) {},
		OnEvent: func(evt EventMessage) {
			if evt.Type == "tts_warn" {
				warnEvents = append(warnEvents, evt)
			}
		},
		OnChunk: func(chunk TTSChunk) error {
			chunks = append(chunks, string(chunk.Data))
			return nil
		},
	})
	if err := p.RunTurn(context.Background(), 1, "hi", nil); err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
	wantCalls := []string{"第一句。", "第二句。", "第三句。"}
	if !reflect.DeepEqual(tts.calls, wantCalls) {
		t.Fatalf("unexpected tts calls: got %#v want %#v", tts.calls, wantCalls)
	}
	wantChunks := []string{"audio-第二句。", "audio-第三句。"}
	if !reflect.DeepEqual(chunks, wantChunks) {
		t.Fatalf("unexpected audio chunks: got %#v want %#v", chunks, wantChunks)
	}
	if len(warnEvents) != 1 {
		t.Fatalf("expected one tts warning event, got %#v", warnEvents)
	}
	if warnEvents[0].Code != "tts_segment_failed" || warnEvents[0].Seq != 1 || warnEvents[0].Text != "第一句。" {
		t.Fatalf("unexpected tts warning event: %#v", warnEvents[0])
	}
}

func TestTurnPipelineFailsIfNoAudioProduced(t *testing.T) {
	tts := &selectiveFakeTTS{
		failOn: map[string]error{
			"第一句。": errors.New("mock fail"),
		},
	}
	p := NewTurnPipeline(&fakeLLM{deltas: []string{"第一句。"}}, tts, nil, TurnCallbacks{
		OnStatus: func(turnID uint64, state string, detail string) {},
		OnEvent:  func(evt EventMessage) {},
		OnChunk: func(chunk TTSChunk) error {
			return nil
		},
	})
	if err := p.RunTurn(context.Background(), 1, "hi", nil); err == nil {
		t.Fatalf("expected error when all tts segments fail")
	}
}

func TestLLMEnvelopeContentPreview(t *testing.T) {
	found, complete, value := extractLLMEnvelopeContentPreview(`{"content":"你好，世界。","action":{"name":"x"}}`)
	if !found || !complete || value != "你好，世界。" {
		t.Fatalf("unexpected parse result found=%v complete=%v value=%q", found, complete, value)
	}

	found, complete, value = extractLLMEnvelopeContentPreview("```json\n{\"content\":\"abc\"}\n```")
	if !found || !complete || value != "abc" {
		t.Fatalf("unexpected fenced parse result found=%v complete=%v value=%q", found, complete, value)
	}
}

func TestLLMContentProjectorJSONEnvelope(t *testing.T) {
	projector := newLLMContentProjector()
	d1 := projector.Push(`{"content":"你好，`)
	d2 := projector.Push(`世界。","action":{"name":"surface.call.counter.set_count","args":{"count":5}}}`)
	if d1 != "你好，" {
		t.Fatalf("unexpected first projected delta: %q", d1)
	}
	if d2 != "世界。" {
		t.Fatalf("unexpected second projected delta: %q", d2)
	}
}

func TestTurnPipelineJSONEnvelopeTTSContentOnly(t *testing.T) {
	tts := &selectiveFakeTTS{failOn: map[string]error{}}
	p := NewTurnPipeline(&fakeLLM{
		deltas: []string{
			`{"content":"请把数字改成 42。","action":{"name":"surface.call.counter.set_count","args":{"count":42}}}`,
		},
	}, tts, nil, TurnCallbacks{
		OnStatus: func(turnID uint64, state string, detail string) {},
		OnEvent:  func(evt EventMessage) {},
		OnChunk: func(chunk TTSChunk) error {
			return nil
		},
	})
	if err := p.RunTurn(context.Background(), 7, "set counter", nil); err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
	if len(tts.calls) != 1 {
		t.Fatalf("unexpected tts call size: %#v", tts.calls)
	}
	if got := tts.calls[0]; got != "请把数字改成 42。" {
		t.Fatalf("tts should receive content only, got=%q", got)
	}
}
