package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
	Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent, history []ChatMessage) error
	Finish()
}

type DoubaoASRClient struct {
	cfg           ASRConfig
	runtimeConfig *RuntimeConfigManager
	dialer        *websocket.Dialer
	writeTTL      time.Duration
	readTTL       time.Duration
	finishCh      chan struct{}
}

func NewDoubaoASRClient(cfg ASRConfig, runtimeConfig *RuntimeConfigManager) *DoubaoASRClient {
	return &DoubaoASRClient{
		cfg:           cfg,
		runtimeConfig: runtimeConfig,
		dialer: &websocket.Dialer{
			HandshakeTimeout: 8 * time.Second,
		},
		writeTTL: 6 * time.Second,
		readTTL:  60 * time.Second,
		finishCh: make(chan struct{}, 1),
	}
}

// Finish forcefully tells the ASR server that the audio stream is complete.
// This is critical when frontend applies aggressive silence filtering and starves the stream.
func (c *DoubaoASRClient) Finish() {
	select {
	case c.finishCh <- struct{}{}:
	default:
	}
}

func (c *DoubaoASRClient) Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent, history []ChatMessage) error {
	chatCfg := c.chatConfig()
	writeTTL := durationFromMS(chatCfg.ASR.WriteTimeoutMs, c.writeTTL)
	readTTL := durationFromMS(chatCfg.ASR.ReadTimeoutMs, c.readTTL)
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
	var finishRequested atomic.Bool

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
		_ = conn.SetWriteDeadline(time.Now().Add(writeTTL))
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
		_ = conn.SetWriteDeadline(time.Now().Add(writeTTL))
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
	}

	// Drain any pending finish signals from a previous run
	select {
	case <-c.finishCh:
	default:
	}

	// Send start frame
	{
		payload := c.buildStartPayload(target.resourceID, history)
		body, _ := json.Marshal(payload)
		frame, err := buildASRClientFrame(asrMsgTypeFullClient, asrFlagNoSequence, asrSerializationJSON, asrCompressionGzip, body)
		if err != nil {
			return err
		}
		writeMu.Lock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeTTL))
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
			case <-c.finishCh:
				finishRequested.Store(true)
				Debugf("Finish signal received, sending ending frame to ASR server")
				writeStop()
				// Drain remaining audio and wait for context cancellation.
				// The read goroutine will receive ASREventEndpoint from the server.
				for {
					select {
					case <-ctx.Done():
						errCh <- nil
						return
					case <-audio:
						// drain
					}
				}
			}
		}
	}()

	// Read goroutine: reads server frames
	go func() {
		for {
			if err := conn.SetReadDeadline(time.Now().Add(readTTL)); err != nil {
				errCh <- fmt.Errorf("set asr read deadline: %w", err)
				return
			}
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					errCh <- nil
					return
				}
				if finishRequested.Load() && isExpectedASRFinishClose(err) {
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

func isExpectedASRFinishClose(err error) bool {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseNoStatusReceived, websocket.CloseGoingAway) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "finish last sequence") || strings.Contains(msg, "close 1000") || strings.Contains(msg, "close 1005")
}

func (c *DoubaoASRClient) chatConfig() ChatPublicConfig {
	if c.runtimeConfig != nil {
		return c.runtimeConfig.Snapshot().Chat
	}
	return defaultPublicConfig().Chat
}
