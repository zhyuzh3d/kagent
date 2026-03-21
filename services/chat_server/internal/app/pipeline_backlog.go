package app

import (
	"strings"
	"sync"
	"time"
	"unicode"
)

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
