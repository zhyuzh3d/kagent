package app

import (
	"context"
	"strings"
	"unicode/utf8"
)

type TTSChunk struct {
	TurnID uint64
	Seq    int
	Format string
	Data   []byte
}

type TurnCallbacks struct {
	OnStatus func(state string, detail string)
	OnEvent  func(evt EventMessage)
	OnChunk  func(chunk TTSChunk) error
}

type TurnPipeline struct {
	llm LLMClient
	tts TTSClient
	cb  TurnCallbacks
}

func NewTurnPipeline(llm LLMClient, tts TTSClient, cb TurnCallbacks) *TurnPipeline {
	return &TurnPipeline{llm: llm, tts: tts, cb: cb}
}

func (p *TurnPipeline) RunTurn(ctx context.Context, turnID uint64, userText string, history []ChatMessage) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	segmenter := NewTextSegmenter(6) // balanced: not too small (excessive connections) nor too large (delays TTFB)
	var finalBuilder strings.Builder
	seq := 0
	spokenOnce := false

	// Channel for TTS segments — decouples LLM streaming from TTS calls
	ttsCh := make(chan string, 16)
	ttsErrCh := make(chan error, 1)

	// TTS worker goroutine: consumes text segments and synthesizes complete audio per segment
	go func() {
		defer close(ttsErrCh)
		for seg := range ttsCh {
			if ctx.Err() != nil {
				return
			}
			audio, format, err := p.tts.Synthesize(ctx, seg)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				ttsErrCh <- err
				return
			}
			if len(audio) == 0 {
				continue
			}
			if !spokenOnce {
				spokenOnce = true
				p.cb.OnStatus(StateSpeaking, "ai is speaking")
			}
			seq++
			if err := p.cb.OnChunk(TTSChunk{TurnID: turnID, Seq: seq, Format: format, Data: audio}); err != nil {
				ttsErrCh <- err
				return
			}
		}
	}()

	// Enqueue a text segment for TTS (blocking, respects ctx cancellation)
	enqueueSeg := func(seg string) {
		if seg == "" || ctx.Err() != nil {
			return
		}
		select {
		case ttsCh <- seg:
		case <-ctx.Done():
		}
	}

	// Emit speaking status immediately on first LLM delta for faster feedback
	llmDeltaOnce := false

	// Stream LLM — deltas are sent to frontend immediately, TTS segments are queued
	final, err := p.llm.Stream(ctx, userText, history, func(delta string) {
		d := strings.TrimSpace(delta)
		if d == "" {
			return
		}

		if !llmDeltaOnce {
			llmDeltaOnce = true
			// Immediately signal that we're actively generating
			p.cb.OnStatus(StateSpeaking, "ai is speaking")
		}

		finalBuilder.WriteString(d)
		p.cb.OnEvent(NewTextEvent("llm_delta", turnID, d))

		for _, seg := range segmenter.Push(d) {
			enqueueSeg(seg)
		}
	})

	// Flush remaining text to TTS
	for _, seg := range segmenter.Flush() {
		enqueueSeg(seg)
	}
	close(ttsCh) // Signal TTS worker to finish

	// Wait for TTS worker to complete
	if ttsErr := <-ttsErrCh; ttsErr != nil && err == nil {
		err = ttsErr
	}

	if err != nil {
		return err
	}

	if finalBuilder.Len() == 0 {
		finalBuilder.WriteString(final)
	}
	p.cb.OnEvent(NewTextEvent("llm_final", turnID, strings.TrimSpace(finalBuilder.String())))
	return nil
}

type TextSegmenter struct {
	buf      strings.Builder
	minRunes int
	endPunc  map[rune]struct{}
}

func NewTextSegmenter(minRunes int) *TextSegmenter {
	if minRunes <= 0 {
		minRunes = 6
	}
	return &TextSegmenter{
		minRunes: minRunes,
		endPunc: map[rune]struct{}{
			'。':  {},
			'！':  {},
			'？':  {},
			'；':  {},
			'，':  {},
			'、':  {},
			'.':  {},
			'!':  {},
			'?':  {},
			';':  {},
			',':  {},
			'\n': {},
		},
	}
}

func (s *TextSegmenter) Push(delta string) []string {
	if strings.TrimSpace(delta) == "" {
		return nil
	}
	s.buf.WriteString(delta)
	content := s.buf.String()
	if utf8.RuneCountInString(content) < s.minRunes {
		return nil
	}
	last, _ := utf8.DecodeLastRuneInString(content)
	if _, ok := s.endPunc[last]; !ok {
		return nil
	}
	segment := strings.TrimSpace(content)
	s.buf.Reset()
	if segment == "" {
		return nil
	}
	return []string{segment}
}

func (s *TextSegmenter) Flush() []string {
	segment := strings.TrimSpace(s.buf.String())
	s.buf.Reset()
	if segment == "" {
		return nil
	}
	return []string{segment}
}
