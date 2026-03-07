package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ASREventType string

const (
	ASREventPartial  ASREventType = "partial"
	ASREventFinal    ASREventType = "final"
	ASREventEndpoint ASREventType = "endpoint"
)

const (
	asrProtocolVersion byte = 0x1
	asrHeaderSizeWords byte = 0x1

	asrMsgTypeFullClient byte = 0x1
	asrMsgTypeAudioOnly  byte = 0x2
	asrMsgTypeFullServer byte = 0x9
	asrMsgTypeError      byte = 0xF

	asrFlagNoSequence  byte = 0x0
	asrFlagPosSequence byte = 0x1
	asrFlagLastNoSeq   byte = 0x2
	asrFlagLastNegSeq  byte = 0x3

	asrSerializationNone byte = 0x0
	asrSerializationJSON byte = 0x1

	asrCompressionNone byte = 0x0
	asrCompressionGzip byte = 0x1
)

type ASREvent struct {
	Type ASREventType
	Text string
}

type ASRClient interface {
	Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent) error
}

type DoubaoASRClient struct {
	cfg      ASRConfig
	dialer   *websocket.Dialer
	writeTTL time.Duration
	readTTL  time.Duration
}

type asrDialTarget struct {
	wsURL      string
	header     http.Header
	resourceID string
}

type asrServerFrame struct {
	MessageType byte
	Flags       byte
	Sequence    int32
	Payload     []byte
	ErrorCode   uint32
	ErrorMsg    string
}

func NewDoubaoASRClient(cfg ASRConfig) *DoubaoASRClient {
	return &DoubaoASRClient{
		cfg: cfg,
		dialer: &websocket.Dialer{
			HandshakeTimeout: 8 * time.Second,
		},
		writeTTL: 6 * time.Second,
		readTTL:  60 * time.Second,
	}
}

func (c *DoubaoASRClient) Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent) error {
	targets := c.prepareDialTargets()
	var conn *websocket.Conn
	var target asrDialTarget
	var lastErr error
	for _, t := range targets {
		cn, resp, err := c.dialer.DialContext(ctx, t.wsURL, t.header)
		if err != nil {
			lastErr = wrapWSDialError("dial asr websocket", err, resp)
			continue
		}
		conn = cn
		target = t
		break
	}
	if conn == nil {
		if lastErr == nil {
			return fmt.Errorf("dial asr websocket: no valid dial target for wsUrl=%q", c.cfg.WSURL)
		}
		return lastErr
	}
	defer conn.Close()

	var writeMu sync.Mutex

	writeAudio := func(pcm []byte, last bool) error {
		flag := asrFlagNoSequence
		if last {
			flag = asrFlagLastNoSeq
		}
		frame, err := buildASRClientFrame(asrMsgTypeAudioOnly, flag, asrSerializationNone, asrCompressionGzip, pcm)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(c.writeTTL))
		return conn.WriteMessage(websocket.BinaryMessage, frame)
	}

	writeStop := func() {
		writeMu.Lock()
		defer writeMu.Unlock()
		frame, err := buildASRClientFrame(asrMsgTypeAudioOnly, asrFlagLastNoSeq, asrSerializationNone, asrCompressionGzip, nil)
		if err != nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "asr stop"), time.Now().Add(500*time.Millisecond))
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(c.writeTTL))
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
	}

	// Send start frame
	{
		payload := c.buildStartPayload(target.resourceID)
		body, _ := json.Marshal(payload)
		frame, err := buildASRClientFrame(asrMsgTypeFullClient, asrFlagNoSequence, asrSerializationJSON, asrCompressionGzip, body)
		if err != nil {
			return err
		}
		writeMu.Lock()
		_ = conn.SetWriteDeadline(time.Now().Add(c.writeTTL))
		err = conn.WriteMessage(websocket.BinaryMessage, frame)
		writeMu.Unlock()
		if err != nil {
			return fmt.Errorf("send asr start: %w", err)
		}
	}

	errCh := make(chan error, 2)

	// Write goroutine: sends audio frames. Does NOT call sendStop.
	go func() {
		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			case frame, ok := <-audio:
				if !ok {
					errCh <- nil
					return
				}
				if len(frame) == 0 {
					continue
				}
				if err := writeAudio(frame, false); err != nil {
					errCh <- fmt.Errorf("write asr audio frame: %w", err)
					return
				}
			}
		}
	}()

	// Read goroutine: reads server frames
	go func() {
		for {
			if err := conn.SetReadDeadline(time.Now().Add(c.readTTL)); err != nil {
				errCh <- fmt.Errorf("set asr read deadline: %w", err)
				return
			}
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					errCh <- nil
					return
				}
				errCh <- fmt.Errorf("read asr message: %w", err)
				return
			}
			if mt != websocket.BinaryMessage {
				continue
			}
			frame, err := parseASRServerFrame(msg)
			if err != nil {
				continue
			}
			if frame.MessageType == asrMsgTypeError {
				errCh <- fmt.Errorf("asr server error code=%d message=%s", frame.ErrorCode, strings.TrimSpace(frame.ErrorMsg))
				return
			}
			if frame.MessageType != asrMsgTypeFullServer {
				continue
			}
			finalHint := frame.Flags == asrFlagLastNegSeq || frame.Flags == asrFlagLastNoSeq
			for _, evt := range parseASRPayload(frame.Payload, finalHint) {
				select {
				case events <- evt:
				case <-ctx.Done():
					errCh <- nil
					return
				}
			}
		}
	}()

	// Wait for first goroutine to exit, then cleanup
	select {
	case <-ctx.Done():
		writeStop()
		return nil
	case err := <-errCh:
		writeStop()
		return err
	}
}

func (c *DoubaoASRClient) prepareDialTargets() []asrDialTarget {
	urls := candidateASRURLs(c.cfg.WSURL)
	resourceIDs := uniqueStrings(
		c.cfg.ResourceID,
		strings.Replace(strings.TrimSpace(c.cfg.ResourceID), "seedasr", "bigasr", 1),
		"volc.seedasr.sauc.duration",
		"volc.bigasr.sauc.duration",
	)
	targets := make([]asrDialTarget, 0, len(urls)*len(resourceIDs))
	for _, wsURL := range urls {
		for _, rid := range resourceIDs {
			targets = append(targets, asrDialTarget{
				wsURL:      wsURL,
				resourceID: rid,
				header:     buildASRHeaders(c.cfg, rid),
			})
		}
	}
	return targets
}

func buildASRHeaders(cfg ASRConfig, resourceID string) http.Header {
	h := http.Header{}
	h.Set("X-Api-App-Key", cfg.AppID)
	h.Set("X-Api-Access-Key", cfg.AccessToken)
	h.Set("X-Api-Resource-Id", resourceID)
	h.Set("X-Api-Request-Id", newRequestID())
	h.Set("X-Api-Connect-Id", newRequestID())
	h.Set("Authorization", "Bearer "+cfg.AccessToken)
	// Compatibility headers for older gateway variants.
	h.Set("X-Appid", cfg.AppID)
	h.Set("X-Resource-Id", resourceID)
	h.Set("X-Access-Token", cfg.AccessToken)
	return h
}

func candidateASRURLs(raw string) []string {
	base := strings.TrimSpace(raw)
	candidates := uniqueStrings(
		base,
		strings.ReplaceAll(base, "/api/v3/sauc/bigmodel_async", "/api/v3/sauc/bigmodel"),
		strings.ReplaceAll(base, "/api/v3/sauc/bigmodel_nostream", "/api/v3/sauc/bigmodel"),
	)
	if strings.HasSuffix(base, "/api/v3/sauc/bigmodel") {
		candidates = append(candidates, base+"_async")
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		u, err := url.Parse(c)
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		out = append(out, u.String())
	}
	return out
}

func wrapWSDialError(prefix string, err error, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_ = resp.Body.Close()
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("%s: %w (status=%d)", prefix, err, resp.StatusCode)
	}
	return fmt.Errorf("%s: %w (status=%d body=%s)", prefix, err, resp.StatusCode, msg)
}

func (c *DoubaoASRClient) buildStartPayload(resourceID string) map[string]any {
	return map[string]any{
		"user": map[string]any{
			"uid": "kagent",
		},
		"audio": map[string]any{
			"format":  "pcm",
			"codec":   "raw",
			"rate":    16000,
			"bits":    16,
			"channel": 1,
		},
		"request": map[string]any{
			"model_name":      "bigmodel",
			"show_utterances": true,
			"result_type":     "single",
			"enable_itn":      true,
			"enable_punc":     true,
			"end_window_size": 800,
		},
		"resource_id": resourceID,
	}
}

func buildASRClientFrame(msgType byte, flags byte, serialization byte, compression byte, payload []byte) ([]byte, error) {
	header := []byte{
		(asrProtocolVersion << 4) | asrHeaderSizeWords,
		(msgType << 4) | (flags & 0x0F),
		(serialization << 4) | (compression & 0x0F),
		0x00,
	}
	compressed := payload
	if compression == asrCompressionGzip {
		var err error
		compressed, err = gzipCompress(payload)
		if err != nil {
			return nil, err
		}
	}
	out := make([]byte, 0, 8+len(compressed))
	out = append(out, header...)
	sizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(sizeBytes, uint32(len(compressed)))
	out = append(out, sizeBytes...)
	out = append(out, compressed...)
	return out, nil
}

func parseASRServerFrame(raw []byte) (*asrServerFrame, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("asr frame too short: %d", len(raw))
	}
	headerWords := raw[0] & 0x0F
	headerSize := int(headerWords) * 4
	if headerSize < 4 || len(raw) < headerSize {
		return nil, fmt.Errorf("invalid header size: %d", headerSize)
	}
	msgType := (raw[1] >> 4) & 0x0F
	flags := raw[1] & 0x0F
	compression := raw[2] & 0x0F

	idx := headerSize
	frame := &asrServerFrame{MessageType: msgType, Flags: flags}

	if msgType == asrMsgTypeError {
		if len(raw) < idx+8 {
			return nil, fmt.Errorf("invalid asr error frame")
		}
		frame.ErrorCode = binary.BigEndian.Uint32(raw[idx : idx+4])
		msgSize := int(binary.BigEndian.Uint32(raw[idx+4 : idx+8]))
		idx += 8
		if len(raw) < idx+msgSize {
			return nil, fmt.Errorf("invalid asr error payload size")
		}
		frame.ErrorMsg = string(raw[idx : idx+msgSize])
		return frame, nil
	}

	if flags == asrFlagPosSequence || flags == asrFlagLastNegSeq {
		if len(raw) < idx+4 {
			return nil, fmt.Errorf("invalid asr frame sequence")
		}
		frame.Sequence = int32(binary.BigEndian.Uint32(raw[idx : idx+4]))
		idx += 4
	}
	if len(raw) < idx+4 {
		return nil, fmt.Errorf("invalid asr frame payload size")
	}
	payloadSize := int(binary.BigEndian.Uint32(raw[idx : idx+4]))
	idx += 4
	if payloadSize < 0 || len(raw) < idx+payloadSize {
		return nil, fmt.Errorf("invalid asr frame payload length")
	}
	payload := raw[idx : idx+payloadSize]
	if compression == asrCompressionGzip {
		unzipped, err := gzipDecompress(payload)
		if err != nil {
			return nil, fmt.Errorf("gzip decode asr payload: %w", err)
		}
		payload = unzipped
	}
	frame.Payload = payload
	return frame, nil
}

func parseASRPayload(raw []byte, finalHint bool) []ASREvent {
	m, err := unmarshalMap(raw)
	if err != nil {
		return nil
	}
	text := extractASRText(m)
	if text == "" {
		return nil
	}
	isFinal := finalHint || asBool(m["is_final"]) || asBool(m["final"]) || hasDefiniteUtterance(m)
	if isFinal {
		return []ASREvent{{Type: ASREventFinal, Text: text}}
	}
	return []ASREvent{{Type: ASREventPartial, Text: text}}
}

func extractASRText(m map[string]any) string {
	if result, ok := m["result"].(map[string]any); ok {
		if t := asString(result["text"]); t != "" {
			return t
		}
	}
	keys := map[string]struct{}{
		"text":       {},
		"result":     {},
		"transcript": {},
		"utterance":  {},
		"sentence":   {},
		"content":    {},
	}
	items := make([]string, 0, 8)
	collectStringsByKeys(m, keys, &items)
	uniq := uniqueNonEmpty(items)
	if len(uniq) == 0 {
		return ""
	}
	return uniq[0]
}

func hasDefiniteUtterance(m map[string]any) bool {
	result, ok := m["result"].(map[string]any)
	if !ok {
		return false
	}
	utterances, ok := result["utterances"].([]any)
	if !ok {
		return false
	}
	for _, item := range utterances {
		u, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if asBool(u["definite"]) {
			return true
		}
	}
	return false
}

func asString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func asBool(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func gzipCompress(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipDecompress(payload []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
