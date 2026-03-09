package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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

		for _, sentence := range segmenter.Push(d) {
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

	if finalBuilder.Len() == 0 {
		finalBuilder.WriteString(final)
	}
	p.cb.OnEvent(NewTextEvent("llm_final", turnID, strings.TrimSpace(finalBuilder.String())))
	return nil
}

type SentenceSegmenter struct {
	buf           string
	sentenceBreak map[rune]struct{}
}

func NewSentenceSegmenter() *SentenceSegmenter {
	return NewSentenceSegmenterWithBreaks(defaultPublicConfig().Chat.Pipeline.SentenceBreaks)
}

func NewSentenceSegmenterWithBreaks(breaks []string) *SentenceSegmenter {
	sentenceBreak := map[rune]struct{}{}
	for _, item := range breaks {
		runes := []rune(item)
		if len(runes) != 1 {
			continue
		}
		sentenceBreak[runes[0]] = struct{}{}
	}
	if len(sentenceBreak) == 0 {
		sentenceBreak = map[rune]struct{}{
			'。':  {},
			'！':  {},
			'？':  {},
			'；':  {},
			'.':  {},
			'!':  {},
			'?':  {},
			';':  {},
			'…':  {},
			'\n': {},
		}
	}
	return &SentenceSegmenter{
		sentenceBreak: sentenceBreak,
	}
}

func (s *SentenceSegmenter) Push(delta string) []string {
	if strings.TrimSpace(delta) == "" {
		return nil
	}
	s.buf += delta
	return s.extract(false)
}

func (s *SentenceSegmenter) Flush() []string {
	out := s.extract(true)
	s.buf = ""
	return out
}

func (s *SentenceSegmenter) extract(flush bool) []string {
	if strings.TrimSpace(s.buf) == "" {
		if flush {
			s.buf = ""
		}
		return nil
	}

	var out []string
	start := 0
	runes := []rune(s.buf)
	for i, r := range runes {
		if !s.isSentenceBreak(r) {
			continue
		}
		segment := strings.TrimSpace(string(runes[start : i+1]))
		if segment != "" {
			out = append(out, segment)
		}
		start = i + 1
	}

	if flush {
		if start < len(runes) {
			segment := strings.TrimSpace(string(runes[start:]))
			if segment != "" {
				out = append(out, segment)
			}
		}
		s.buf = ""
		return out
	}

	if start == 0 {
		return nil
	}
	s.buf = string(runes[start:])
	return out
}

func (s *SentenceSegmenter) isSentenceBreak(r rune) bool {
	_, ok := s.sentenceBreak[r]
	return ok
}

type sentenceGroupPolicy struct {
	targetSentences int
	maxRunes        int
}

func selectSentenceGroup(sentences []string, backlogMS int64, flush bool) (string, int) {
	return selectSentenceGroupWithConfig(sentences, backlogMS, flush, defaultPublicConfig().Chat.Pipeline)
}

func selectSentenceGroupWithConfig(sentences []string, backlogMS int64, flush bool, cfg ChatPipelinePublicConfig) (string, int) {
	if len(sentences) == 0 {
		return "", 0
	}

	policy := groupingPolicy(backlogMS, cfg)
	if !flush && policy.targetSentences > 1 && len(sentences) < policy.targetSentences {
		return "", 0
	}

	count := policy.targetSentences
	if count > len(sentences) {
		count = len(sentences)
	}
	if count <= 0 {
		count = 1
	}
	for count > 1 && joinedRuneCount(sentences[:count]) > policy.maxRunes {
		count--
	}
	if count == 1 {
		return strings.TrimSpace(sentences[0]), 1
	}
	return strings.TrimSpace(strings.Join(sentences[:count], "")), count
}

func groupingPolicy(backlogMS int64, cfg ChatPipelinePublicConfig) sentenceGroupPolicy {
	for _, rule := range cfg.GroupingPolicies {
		if backlogMS >= int64(rule.BacklogMs) {
			return sentenceGroupPolicy{targetSentences: rule.TargetSentences, maxRunes: rule.MaxRunes}
		}
	}
	return sentenceGroupPolicy{targetSentences: 1, maxRunes: 80}
}

func joinedRuneCount(parts []string) int {
	total := 0
	for _, part := range parts {
		total += utf8.RuneCountInString(strings.TrimSpace(part))
	}
	return total
}

func estimateSpeechDuration(text string) int64 {
	return estimateSpeechDurationWithConfig(text, defaultPublicConfig().Chat.Pipeline)
}

func estimateSpeechDurationWithConfig(text string, cfg ChatPipelinePublicConfig) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	runes := []rune(text)
	ms := cfg.SpeechRuneMs * len(runes)
	for _, r := range runes {
		switch r {
		case '。', '！', '？', '；', '.', '!', '?', ';', '…':
			ms += cfg.SentencePauseMs
		case '，', '、', ',', ':', '：':
			ms += cfg.ClausePauseMs
		}
	}
	if ms < cfg.MinimumSpeechMs {
		ms = cfg.MinimumSpeechMs
	}
	return int64(ms)
}

type playbackBacklogEstimator struct {
	mu        sync.Mutex
	pendingMS float64
	lastAt    time.Time
	capMS     float64
}

func newPlaybackBacklogEstimator(capMS int64) *playbackBacklogEstimator {
	if capMS <= 0 {
		capMS = int64(defaultPublicConfig().Chat.Pipeline.BacklogCapMs)
	}
	return &playbackBacklogEstimator{lastAt: time.Now(), capMS: float64(capMS)}
}

func (e *playbackBacklogEstimator) Add(ms int64) {
	if ms <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.decayLocked(time.Now())
	e.pendingMS += float64(ms)
	if e.pendingMS > e.capMS {
		e.pendingMS = e.capMS
	}
}

func (e *playbackBacklogEstimator) CurrentMS() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.decayLocked(time.Now())
	return int64(e.pendingMS)
}

func (e *playbackBacklogEstimator) decayLocked(now time.Time) {
	if e.lastAt.IsZero() {
		e.lastAt = now
		return
	}
	elapsed := now.Sub(e.lastAt).Milliseconds()
	if elapsed > 0 {
		e.pendingMS -= float64(elapsed)
		if e.pendingMS < 0 {
			e.pendingMS = 0
		}
	}
	e.lastAt = now
}

func isPunctuationOnly(text string) bool {
	hasRune := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		hasRune = true
		if !unicode.IsPunct(r) && !unicode.IsSymbol(r) {
			return false
		}
	}
	return hasRune
}

func (p *TurnPipeline) chatConfig() ChatPublicConfig {
	if p.runtimeConfig != nil {
		return p.runtimeConfig.Snapshot().Chat
	}
	return defaultPublicConfig().Chat
}
