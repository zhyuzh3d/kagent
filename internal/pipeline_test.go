package app

import (
	"context"
	"testing"
)

type fakeLLM struct {
	deltas []string
}

func (f *fakeLLM) Stream(ctx context.Context, input string, onDelta func(string)) (string, error) {
	for _, d := range f.deltas {
		onDelta(d)
	}
	return "", nil
}

type fakeTTS struct{}

func (f *fakeTTS) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	return []byte("audio-" + text), "audio/mpeg", nil
}

func TestTextSegmenter(t *testing.T) {
	s := NewTextSegmenter(3)
	if out := s.Push("你好"); len(out) != 0 {
		t.Fatalf("unexpected flush")
	}
	out := s.Push("世界。")
	if len(out) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(out))
	}
}

func TestTurnPipelineRun(t *testing.T) {
	chunks := 0
	p := NewTurnPipeline(&fakeLLM{deltas: []string{"你好，", "世界。"}}, &fakeTTS{}, TurnCallbacks{
		OnStatus: func(state string, detail string) {},
		OnEvent:  func(evt EventMessage) {},
		OnChunk: func(chunk TTSChunk) error {
			chunks++
			return nil
		},
	})
	if err := p.RunTurn(context.Background(), 1, "hi"); err != nil {
		t.Fatalf("run turn failed: %v", err)
	}
	if chunks == 0 {
		t.Fatalf("expected chunks > 0")
	}
}
