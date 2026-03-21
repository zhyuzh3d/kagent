package app

import (
	"strconv"
	"strings"
)

const (
	llmProjectModeUnknown = iota
	llmProjectModePlain
	llmProjectModeJSONCandidate
	llmProjectModeJSON
)

type llmContentProjector struct {
	raw         strings.Builder
	mode        int
	lastContent string
}

func newLLMContentProjector() *llmContentProjector {
	return &llmContentProjector{mode: llmProjectModeUnknown}
}

func (p *llmContentProjector) Push(delta string) string {
	if delta == "" {
		return ""
	}
	p.raw.WriteString(delta)
	raw := p.raw.String()

	found, _, content := extractLLMEnvelopeSayPreview(raw)
	if found {
		p.mode = llmProjectModeJSON
		if strings.HasPrefix(content, p.lastContent) {
			next := content[len(p.lastContent):]
			p.lastContent = content
			return next
		}
		if content != p.lastContent {
			p.lastContent = content
			return content
		}
		return ""
	}

	switch p.mode {
	case llmProjectModeUnknown:
		if looksLikeLLMEnvelope(raw) {
			p.mode = llmProjectModeJSONCandidate
			return ""
		}
		p.mode = llmProjectModePlain
		return delta
	case llmProjectModePlain:
		return delta
	case llmProjectModeJSONCandidate, llmProjectModeJSON:
		return ""
	default:
		return delta
	}
}

func (p *llmContentProjector) FinalContent() string {
	return strings.TrimSpace(p.lastContent)
}

func looksLikeLLMEnvelope(raw string) bool {
	text := strings.TrimSpace(raw)
	if text == "" {
		return false
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "```") {
		return true
	}
	return strings.Contains(text, `"say"`) || strings.Contains(text, `"aside"`) || strings.Contains(text, `"content"`) || strings.Contains(text, `"action"`)
}

func normalizeLLMEnvelopeRaw(raw string) string {
	text := strings.TrimLeft(raw, " \t\r\n")
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	fenceIdx := strings.Index(lower, "```json")
	if fenceIdx >= 0 {
		return strings.TrimLeft(text[fenceIdx+7:], " \t\r\n")
	}
	return text
}

func extractLLMEnvelopeSayPreview(raw string) (found bool, complete bool, value string) {
	source := normalizeLLMEnvelopeRaw(raw)
	if source == "" {
		return false, false, ""
	}

	if idx := strings.Index(source, "{"); idx > 0 && (strings.Contains(source, `"say"`) || strings.Contains(source, `"content"`)) {
		source = source[idx:]
	}

	key := `"say"`
	keyIdx := strings.Index(source, key)
	if keyIdx < 0 {
		key = `"content"`
		keyIdx = strings.Index(source, key)
		if keyIdx < 0 {
			return false, false, ""
		}
	}
	i := keyIdx + len(key)
	for i < len(source) && isJSONSpace(source[i]) {
		i++
	}
	if i >= len(source) || source[i] != ':' {
		return true, false, ""
	}
	i++
	for i < len(source) && isJSONSpace(source[i]) {
		i++
	}
	if i >= len(source) {
		return true, false, ""
	}
	if source[i] != '"' {
		return true, true, ""
	}
	i++

	var b strings.Builder
	escaped := false
	for i < len(source) {
		ch := source[i]
		if escaped {
			escaped = false
			switch ch {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'u':
				if i+4 >= len(source) {
					return true, false, b.String()
				}
				code := source[i+1 : i+5]
				if !isHex4(code) {
					return true, false, b.String()
				}
				num, _ := strconv.ParseInt(code, 16, 32)
				b.WriteRune(rune(num))
				i += 4
			default:
				b.WriteByte(ch)
			}
			i++
			continue
		}
		if ch == '\\' {
			escaped = true
			i++
			continue
		}
		if ch == '"' {
			return true, true, b.String()
		}
		b.WriteByte(ch)
		i++
	}
	return true, false, b.String()
}

func isJSONSpace(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t'
}

func isHex4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		ch := s[i]
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}
