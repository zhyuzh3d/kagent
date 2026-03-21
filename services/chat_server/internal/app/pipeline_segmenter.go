package app

import (
	"strings"
	"unicode/utf8"
)

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
	return &SentenceSegmenter{sentenceBreak: sentenceBreak}
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
