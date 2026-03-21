package app

import (
	"context"
	"fmt"
	"strings"
)

type TTSChunk struct {
	TurnID uint64
	Seq    int
	Format string
	Data   []byte
}

type TurnCallbacks struct {
	OnStatus func(turnID uint64, state string, detail string)
	OnEvent  func(evt EventMessage)
	OnChunk  func(chunk TTSChunk) error
}

type TurnPipeline struct {
	llm           LLMClient
	tts           TTSClient
	runtimeConfig *RuntimeConfigManager
	cb            TurnCallbacks
}

func NewTurnPipeline(llm LLMClient, tts TTSClient, runtimeConfig *RuntimeConfigManager, cb TurnCallbacks) *TurnPipeline {
	return &TurnPipeline{llm: llm, tts: tts, runtimeConfig: runtimeConfig, cb: cb}
}

func (p *TurnPipeline) RunTurn(ctx context.Context, turnID uint64, userText string, history []ChatMessage) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pipelineCfg := p.chatConfig().Pipeline
	segmenter := NewSentenceSegmenterWithBreaks(pipelineCfg.SentenceBreaks)
	contentProjector := newLLMContentProjector()
	backlog := newPlaybackBacklogEstimator(int64(pipelineCfg.BacklogCapMs))
	var finalBuilder strings.Builder
	seq := 0
	segmentSeq := 0
	spokenOnce := false
	pendingSentences := make([]string, 0, 8)

	ttsCh := make(chan string, 16)
	type ttsRunResult struct {
		firstErr error
		audioOut int
	}
	ttsDoneCh := make(chan ttsRunResult, 1)

	go func() {
		var firstErr error
		audioOut := 0
		defer func() {
			ttsDoneCh <- ttsRunResult{firstErr: firstErr, audioOut: audioOut}
		}()
		for seg := range ttsCh {
			if ctx.Err() != nil {
				return
			}
			segmentSeq++
			audio, format, err := p.tts.Synthesize(ctx, seg)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if firstErr == nil {
					firstErr = err
				}
				Errorf("[Turn:%d] %q -> TTS segment synth failed: err=%v", turnID, seg, err)
				p.cb.OnEvent(NewTTSWarnEvent(turnID, segmentSeq, "tts_segment_failed", err.Error(), seg))
				continue
			}
			if len(audio) == 0 {
				if firstErr == nil {
					firstErr = fmt.Errorf("tts session finished without audio: %s", seg)
				}
				Warnf("[Turn:%d] %q -> TTS synth returned empty audio", turnID, seg)
				p.cb.OnEvent(NewTTSWarnEvent(turnID, segmentSeq, "tts_empty_audio", "tts synth returned empty audio", seg))
				continue
			}
			if !spokenOnce {
				spokenOnce = true
				Infof("[Turn:%d] %q -> TTS start sending...", turnID, Snippet(seg))
				p.cb.OnStatus(turnID, StateSpeaking, "ai is speaking")
			}
			seq = segmentSeq
			audioOut++
			backlog.Add(estimateSpeechDurationWithConfig(seg, pipelineCfg))
			if err := p.cb.OnChunk(TTSChunk{TurnID: turnID, Seq: seq, Format: format, Data: audio}); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
		}
	}()

	enqueueSeg := func(seg string) {
		seg = strings.TrimSpace(seg)
		if seg == "" || isPunctuationOnly(seg) || ctx.Err() != nil {
			return
		}
		select {
		case ttsCh <- seg:
		case <-ctx.Done():
		}
	}

	drainReady := func(flush bool) {
		for {
			group, used := selectSentenceGroupWithConfig(pendingSentences, backlog.CurrentMS(), flush, pipelineCfg)
			if used == 0 {
				return
			}
			enqueueSeg(group)
			pendingSentences = pendingSentences[used:]
		}
	}

	final, err := p.llm.Stream(ctx, userText, history, func(delta string) {
		d := strings.TrimSpace(delta)
		if d == "" {
			return
		}

		finalBuilder.WriteString(d)
		p.cb.OnEvent(NewTextEvent("llm_delta", turnID, d))

		spokenDelta := contentProjector.Push(d)
		if strings.TrimSpace(spokenDelta) == "" {
			return
		}

		for _, sentence := range segmenter.Push(spokenDelta) {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" || isPunctuationOnly(sentence) {
				continue
			}
			pendingSentences = append(pendingSentences, sentence)
		}
		drainReady(false)
	})

	for _, sentence := range segmenter.Flush() {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" || isPunctuationOnly(sentence) {
			continue
		}
		pendingSentences = append(pendingSentences, sentence)
	}
	drainReady(true)
	close(ttsCh)

	ttsResult := <-ttsDoneCh
	if err == nil && ttsResult.audioOut == 0 && ttsResult.firstErr != nil {
		err = ttsResult.firstErr
	}

	if err != nil {
		return err
	}

	Infof("[Turn:%d] -> TTS finished sending", turnID)

	rawFinal := strings.TrimSpace(finalBuilder.String())
	if rawFinal == "" {
		rawFinal = strings.TrimSpace(final)
	}
	contentFinal := strings.TrimSpace(contentProjector.FinalContent())
	finalText := rawFinal
	if contentFinal != "" {
		finalText = contentFinal
	} else if looksLikeLLMEnvelope(rawFinal) {
		finalText = ""
	}
	p.cb.OnEvent(NewTextEvent("llm_final", turnID, finalText))
	return nil
}

func (p *TurnPipeline) chatConfig() ChatPublicConfig {
	if p.runtimeConfig != nil {
		return p.runtimeConfig.Snapshot().Chat
	}
	return defaultPublicConfig().Chat
}
