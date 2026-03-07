package app

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	StateIdle        = "Idle"
	StateConnecting  = "Connecting"
	StateListening   = "Listening"
	StateRecognizing = "Recognizing"
	StateThinking    = "Thinking"
	StateSpeaking    = "Speaking"
	StateInterrupted = "Interrupted"
	StateError       = "Error"
)

type ControlMessage struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
	TurnID uint64 `json:"turn_id"`
	Text   string `json:"text,omitempty"`
}

type EventMessage struct {
	Type        string `json:"type"`
	TsMS        int64  `json:"ts_ms"`
	TurnID      uint64 `json:"turn_id"`
	Value       string `json:"value,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`
	Recoverable bool   `json:"recoverable,omitempty"`
	Text        string `json:"text,omitempty"`
	Seq         int    `json:"seq,omitempty"`
	Format      string `json:"format,omitempty"`
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func NewStatusEvent(turnID uint64, value string, detail string) EventMessage {
	return EventMessage{
		Type:   "status",
		TsMS:   nowMS(),
		TurnID: turnID,
		Value:  value,
		Detail: detail,
	}
}

func NewErrorEvent(turnID uint64, code string, message string, recoverable bool) EventMessage {
	return EventMessage{
		Type:        "error",
		TsMS:        nowMS(),
		TurnID:      turnID,
		Code:        code,
		Message:     message,
		Recoverable: recoverable,
	}
}

func NewTextEvent(typ string, turnID uint64, text string) EventMessage {
	return EventMessage{
		Type:   typ,
		TsMS:   nowMS(),
		TurnID: turnID,
		Text:   strings.TrimSpace(text),
	}
}

func NewTTSChunkEvent(turnID uint64, seq int, format string) EventMessage {
	return EventMessage{
		Type:   "tts_chunk",
		TsMS:   nowMS(),
		TurnID: turnID,
		Seq:    seq,
		Format: format,
	}
}

func encodeEvent(evt EventMessage) ([]byte, error) {
	return json.Marshal(evt)
}
