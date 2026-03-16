package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type TTSClient interface {
	Synthesize(ctx context.Context, text string) ([]byte, string, error)
}

type DoubaoTTSClient struct {
	cfg           TTSConfig
	runtimeConfig *RuntimeConfigManager
	dialer        *websocket.Dialer
	writeTTL      time.Duration
	readTTL       time.Duration
}

type ttsDialTarget struct {
	wsURL      string
	header     http.Header
	resourceID string
}

type ttsServerFrame struct {
	MessageType byte
	Event       uint32
	StreamID    string
	Payload     []byte
}

const (
	ttsProtocolVersion byte = 0x1
	ttsHeaderSizeWords byte = 0x1

	ttsMsgTypeFullClient byte = 0x1
	ttsMsgTypeFullServer byte = 0x9
	ttsMsgTypeAudio      byte = 0xB
	ttsMsgTypeError      byte = 0xF

	ttsFlagNone      byte = 0x0
	ttsFlagWithEvent byte = 0x4

	ttsSerializationRaw  byte = 0x0
	ttsSerializationJSON byte = 0x1

	ttsCompressionNone byte = 0x0
	ttsCompressionGzip byte = 0x1

	ttsEventStartConnection  uint32 = 1
	ttsEventFinishConnection uint32 = 2

	ttsEventConnectionStarted  uint32 = 50
	ttsEventConnectionFailed   uint32 = 51
	ttsEventConnectionFinished uint32 = 52

	ttsEventStartSession  uint32 = 100
	ttsEventCancelSession uint32 = 101
	ttsEventFinishSession uint32 = 102

	ttsEventSessionStarted  uint32 = 150
	ttsEventSessionCanceled uint32 = 151
	ttsEventSessionFinished uint32 = 152
	ttsEventSessionFailed   uint32 = 153

	ttsEventTaskRequest     uint32 = 200
	ttsEventTTSSentenceHead uint32 = 350
	ttsEventTTSSentenceTail uint32 = 351
	ttsEventTTSResponse     uint32 = 352
)

func NewDoubaoTTSClient(cfg TTSConfig, runtimeConfig *RuntimeConfigManager) *DoubaoTTSClient {
	return &DoubaoTTSClient{
		cfg:           cfg,
		runtimeConfig: runtimeConfig,
		dialer: &websocket.Dialer{
			HandshakeTimeout: 6 * time.Second,
		},
		writeTTL: 6 * time.Second,
		readTTL:  35 * time.Second,
	}
}

func (c *DoubaoTTSClient) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	txt := strings.TrimSpace(text)
	if txt == "" {
		return nil, "", fmt.Errorf("empty tts text")
	}
	chatCfg := c.chatConfig()
	writeTTL := durationFromMS(chatCfg.TTS.WriteTimeoutMs, c.writeTTL)
	readTTL := durationFromMS(chatCfg.TTS.ReadTimeoutMs, c.readTTL)
	voiceType := firstNonEmpty(chatCfg.TTS.VoiceType, c.cfg.VoiceType)

	targets := c.prepareDialTargets()
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("dial tts websocket: no valid target")
	}

	var conn *websocket.Conn
	allErrs := make([]string, 0, len(targets))
	for _, t := range targets {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		cn, resp, err := c.dialer.DialContext(ctx, t.wsURL, t.header)
		if err != nil {
			dErr := wrapWSDialError("dial tts websocket", err, resp)
			allErrs = append(allErrs, fmt.Sprintf("resource_id=%s err=%v", t.resourceID, dErr))
			continue
		}
		conn = cn
		break
	}
	if conn == nil {
		return nil, "", fmt.Errorf("dial tts websocket failed for all targets: %s", strings.Join(allErrs, " | "))
	}
	defer conn.Close()

	if err := c.sendControlEvent(conn, ttsEventStartConnection, "", []byte(`{}`), writeTTL); err != nil {
		return nil, "", fmt.Errorf("send tts start_connection: %w", err)
	}
	startConnResp, err := c.readServerFrame(conn, readTTL)
	if err != nil {
		return nil, "", fmt.Errorf("read tts start_connection response: %w", err)
	}
	if startConnResp.Event == ttsEventConnectionFailed {
		return nil, "", fmt.Errorf("tts connection failed: %s", compactPayloadString(startConnResp.Payload))
	}
	if startConnResp.Event != ttsEventConnectionStarted {
		return nil, "", fmt.Errorf("unexpected start_connection event=%d", startConnResp.Event)
	}

	sessionID := newRequestID()

	startSessionPayload := map[string]any{
		"user": map[string]any{
			"uid": "kagent",
		},
		"event":     ttsEventStartSession,
		"namespace": "BidirectionalTTS",
		"req_params": map[string]any{
			"speaker": voiceType,
			"audio_params": map[string]any{
				"format":      "mp3",
				"sample_rate": 24000,
			},
		},
	}
	startSessionBytes, _ := json.Marshal(startSessionPayload)
	if err := c.sendControlEvent(conn, ttsEventStartSession, sessionID, startSessionBytes, writeTTL); err != nil {
		return nil, "", fmt.Errorf("send tts start_session: %w", err)
	}
	startSessionResp, err := c.readServerFrame(conn, readTTL)
	if err != nil {
		return nil, "", fmt.Errorf("read tts start_session response: %w", err)
	}
	if startSessionResp.Event == ttsEventSessionFailed {
		return nil, "", fmt.Errorf("tts session failed: %s", compactPayloadString(startSessionResp.Payload))
	}
	if startSessionResp.Event != ttsEventSessionStarted {
		return nil, "", fmt.Errorf("unexpected start_session event=%d", startSessionResp.Event)
	}

	taskPayload := map[string]any{
		"user": map[string]any{
			"uid": "kagent",
		},
		"event":     ttsEventTaskRequest,
		"namespace": "BidirectionalTTS",
		"req_params": map[string]any{
			"text": txt,
		},
	}
	taskBytes, _ := json.Marshal(taskPayload)
	if err := c.sendControlEvent(conn, ttsEventTaskRequest, sessionID, taskBytes, writeTTL); err != nil {
		return nil, "", fmt.Errorf("send tts task_request: %w", err)
	}

	// Tell server no more tasks in this session.
	if err := c.sendControlEvent(conn, ttsEventFinishSession, sessionID, []byte(`{}`), writeTTL); err != nil {
		return nil, "", fmt.Errorf("send tts finish_session: %w", err)
	}

	var audio []byte
	format := "audio/mpeg"
	for {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		frame, err := c.readServerFrame(conn, readTTL)
		if err != nil {
			if len(audio) > 0 {
				return audio, format, nil
			}
			return nil, "", fmt.Errorf("read tts frame: %w", err)
		}

		switch frame.Event {
		case ttsEventTTSResponse:
			if b := extractTTSAudio(frame); len(b) > 0 {
				audio = append(audio, b...)
			}
		case ttsEventSessionFinished:
			if len(audio) == 0 {
				return nil, "", fmt.Errorf("tts session finished without audio: %s", compactPayloadString(frame.Payload))
			}
			_ = c.sendControlEvent(conn, ttsEventFinishConnection, "", []byte(`{}`), writeTTL)
			return audio, format, nil
		case ttsEventSessionFailed:
			return nil, "", fmt.Errorf("tts session failed: %s", compactPayloadString(frame.Payload))
		case ttsEventConnectionFinished:
			if len(audio) > 0 {
				return audio, format, nil
			}
			return nil, "", fmt.Errorf("tts connection finished without audio")
		case ttsEventTTSSentenceHead, ttsEventTTSSentenceTail:
			// sentence boundary events — ignore
		default:
			if frame.MessageType == ttsMsgTypeAudio {
				if len(frame.Payload) > 0 {
					audio = append(audio, frame.Payload...)
				}
			}
		}
	}
}

func (c *DoubaoTTSClient) sendControlEvent(conn *websocket.Conn, event uint32, sessionID string, payloadJSON []byte, writeTTL time.Duration) error {
	payload := payloadJSON
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	frame := buildTTSFrame(ttsMsgTypeFullClient, ttsFlagWithEvent, ttsSerializationJSON, ttsCompressionNone, event, sessionID, payload)
	_ = conn.SetWriteDeadline(time.Now().Add(writeTTL))
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func buildTTSFrame(msgType byte, flags byte, serialization byte, compression byte, event uint32, streamID string, payload []byte) []byte {
	header := []byte{
		(ttsProtocolVersion << 4) | ttsHeaderSizeWords,
		(msgType << 4) | (flags & 0x0F),
		(serialization << 4) | (compression & 0x0F),
		0x00,
	}
	out := make([]byte, 0, 16+len(streamID)+len(payload))
	out = append(out, header...)

	eb := make([]byte, 4)
	binary.BigEndian.PutUint32(eb, event)
	out = append(out, eb...)

	if streamID != "" {
		idb := []byte(streamID)
		lb := make([]byte, 4)
		binary.BigEndian.PutUint32(lb, uint32(len(idb)))
		out = append(out, lb...)
		out = append(out, idb...)
	}

	pb := make([]byte, 4)
	binary.BigEndian.PutUint32(pb, uint32(len(payload)))
	out = append(out, pb...)
	out = append(out, payload...)
	return out
}

func (c *DoubaoTTSClient) readServerFrame(conn *websocket.Conn, readTTL time.Duration) (*ttsServerFrame, error) {
	if err := conn.SetReadDeadline(time.Now().Add(readTTL)); err != nil {
		return nil, err
	}
	mt, msg, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt == websocket.TextMessage {
		return &ttsServerFrame{MessageType: ttsMsgTypeFullServer, Event: ttsEventTTSResponse, Payload: msg}, nil
	}
	if mt != websocket.BinaryMessage || len(msg) < 4 {
		return nil, fmt.Errorf("unexpected tts ws frame type=%d len=%d", mt, len(msg))
	}

	headerWords := msg[0] & 0x0F
	headerSize := int(headerWords) * 4
	if headerSize < 4 || len(msg) < headerSize {
		return nil, fmt.Errorf("invalid tts header size=%d", headerSize)
	}
	msgType := (msg[1] >> 4) & 0x0F
	flags := msg[1] & 0x0F
	compression := msg[2] & 0x0F

	idx := headerSize
	frame := &ttsServerFrame{MessageType: msgType}

	if msgType == ttsMsgTypeError {
		if len(msg) < idx+4 {
			return nil, fmt.Errorf("tts server error frame too short")
		}
		errCode := binary.BigEndian.Uint32(msg[idx : idx+4])
		idx += 4
		errPayload := msg[idx:]
		if compression == ttsCompressionGzip {
			if dec, dErr := gzipDecompress(errPayload); dErr == nil {
				errPayload = dec
			}
		}
		return nil, fmt.Errorf("tts server error code=%d: %s", errCode, compactPayloadString(errPayload))
	}

	if flags == ttsFlagWithEvent {
		if len(msg) < idx+4 {
			return nil, fmt.Errorf("tts frame missing event")
		}
		frame.Event = binary.BigEndian.Uint32(msg[idx : idx+4])
		idx += 4
	}

	if ttsEventCarriesStreamID(frame.Event) {
		if len(msg) < idx+4 {
			return nil, fmt.Errorf("tts frame missing stream id len")
		}
		idLen := int(binary.BigEndian.Uint32(msg[idx : idx+4]))
		idx += 4
		if idLen < 0 || len(msg) < idx+idLen {
			return nil, fmt.Errorf("invalid tts stream id len=%d", idLen)
		}
		frame.StreamID = string(msg[idx : idx+idLen])
		idx += idLen
	}

	if len(msg) < idx+4 {
		frame.Payload = nil
		return frame, nil
	}
	payloadLen := int(binary.BigEndian.Uint32(msg[idx : idx+4]))
	idx += 4
	if payloadLen < 0 || len(msg) < idx+payloadLen {
		return nil, fmt.Errorf("invalid tts payload len=%d", payloadLen)
	}
	payload := msg[idx : idx+payloadLen]
	if compression == ttsCompressionGzip {
		dec, dErr := gzipDecompress(payload)
		if dErr == nil {
			payload = dec
		}
	}
	frame.Payload = payload
	return frame, nil
}

func ttsEventCarriesStreamID(event uint32) bool {
	switch event {
	case ttsEventConnectionStarted, ttsEventConnectionFailed, ttsEventConnectionFinished,
		ttsEventSessionStarted, ttsEventSessionCanceled, ttsEventSessionFinished, ttsEventSessionFailed,
		ttsEventTTSSentenceHead, ttsEventTTSSentenceTail, ttsEventTTSResponse:
		return true
	default:
		return false
	}
}

func extractTTSAudio(frame *ttsServerFrame) []byte {
	if frame == nil || len(frame.Payload) == 0 {
		return nil
	}
	if frame.MessageType == ttsMsgTypeAudio {
		return frame.Payload
	}
	if frame.Payload[0] != '{' {
		return frame.Payload
	}
	m, err := unmarshalMap(frame.Payload)
	if err != nil {
		return nil
	}
	for _, key := range []string{"audio", "audio_base64", "data", "payload"} {
		if s := asString(m[key]); s != "" {
			if b, dErr := base64.StdEncoding.DecodeString(s); dErr == nil && len(b) > 0 {
				return b
			}
		}
	}
	if dataMap, ok := m["data"].(map[string]any); ok {
		for _, key := range []string{"audio", "audio_base64", "payload"} {
			if s := asString(dataMap[key]); s != "" {
				if b, dErr := base64.StdEncoding.DecodeString(s); dErr == nil && len(b) > 0 {
					return b
				}
			}
		}
	}
	return nil
}

func compactPayloadString(payload []byte) string {
	s := strings.TrimSpace(string(payload))
	if s == "" {
		return "<empty>"
	}
	if len(s) > 320 {
		return s[:320] + "..."
	}
	return s
}

func (c *DoubaoTTSClient) prepareDialTargets() []ttsDialTarget {
	resourceIDs := ttsResourceIDs(c.cfg.ResourceID)
	targets := make([]ttsDialTarget, 0, len(resourceIDs))
	u, err := url.Parse(strings.TrimSpace(c.cfg.WSURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	for _, rid := range resourceIDs {
		targets = append(targets, ttsDialTarget{
			wsURL:      u.String(),
			resourceID: rid,
			header:     buildTTSHeaders(c.cfg, rid),
		})
	}
	return targets
}

func ttsResourceIDs(configRID string) []string {
	rid := strings.TrimSpace(configRID)
	if rid != "" {
		return []string{rid}
	}
	return []string{"seed-tts-2.0", "seed-tts-1.0", "volc.service_type.10029"}
}

func buildTTSHeaders(cfg TTSConfig, resourceID string) http.Header {
	h := http.Header{}
	h.Set("X-Api-App-Key", cfg.AppID)
	h.Set("X-Api-Access-Key", cfg.AccessToken)
	h.Set("X-Api-Resource-Id", resourceID)
	h.Set("X-Api-Connect-Id", newRequestID())
	h.Set("X-Api-Request-Id", newRequestID())
	return h
}

func (c *DoubaoTTSClient) chatConfig() ChatPublicConfig {
	if c.runtimeConfig != nil {
		return c.runtimeConfig.Snapshot().Chat
	}
	return defaultPublicConfig().Chat
}
