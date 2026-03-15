package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type ServiceProviderFactory struct {
	baseURL        string
	requestTimeout time.Duration
	observer       ServiceCallObserver
}

func NewServiceProviderFactory(cfg AIServiceConfig, observer ServiceCallObserver) *ServiceProviderFactory {
	effective := cfg
	if effective.RequestTimeoutMS <= 0 {
		effective.RequestTimeoutMS = 70000
	}
	return &ServiceProviderFactory{
		baseURL:        strings.TrimRight(strings.TrimSpace(effective.BaseURL), "/"),
		requestTimeout: time.Duration(effective.RequestTimeoutMS) * time.Millisecond,
		observer:       observer,
	}
}

func (f *ServiceProviderFactory) Name() string {
	return "service"
}

func (f *ServiceProviderFactory) NewASRClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) ASRClient {
	return &ServiceASRClient{
		baseURL:  f.baseURL,
		dialer:   &websocket.Dialer{HandshakeTimeout: 8 * time.Second},
		observer: f.observer,
		fallback: NewDoubaoASRClient(cfg.ASR, runtimeConfig),
	}
}

func (f *ServiceProviderFactory) NewLLMClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) LLMClient {
	return &ServiceLLMClient{
		baseURL:        f.baseURL,
		requestTimeout: f.requestTimeout,
		fallback:       NewDoubaoLLMClient(cfg.ActiveChat(), runtimeConfig),
		observer:       f.observer,
	}
}

func (f *ServiceProviderFactory) NewTTSClient(cfg *ModelConfig, runtimeConfig *RuntimeConfigManager) TTSClient {
	return &ServiceTTSClient{
		baseURL:        f.baseURL,
		requestTimeout: f.requestTimeout,
		fallback:       NewDoubaoTTSClient(cfg.TTS, runtimeConfig),
		observer:       f.observer,
	}
}

type ServiceASRClient struct {
	baseURL  string
	dialer   *websocket.Dialer
	finishCh chan struct{}
	observer ServiceCallObserver
	fallback ASRClient
}

func (c *ServiceASRClient) Finish() {
	if c.finishCh == nil {
		if c.fallback != nil {
			c.fallback.Finish()
		}
		return
	}
	select {
	case c.finishCh <- struct{}{}:
	default:
	}
	if c.fallback != nil {
		c.fallback.Finish()
	}
}

func (c *ServiceASRClient) Run(ctx context.Context, audio <-chan []byte, events chan<- ASREvent, history []ChatMessage) error {
	if c.finishCh == nil {
		c.finishCh = make(chan struct{}, 1)
	}
	select {
	case <-c.finishCh:
	default:
	}
	wsURL, err := buildServiceWSURL(c.baseURL, "/v1/asr/stream")
	if err != nil {
		return err
	}
	startReq := AIServiceASRStart{
		Type:      "start",
		RequestID: "svc-" + newRequestID(),
		TurnID:    TurnIDFromContext(ctx),
		History:   history,
	}
	startBytes, _ := json.Marshal(startReq)

	startAt := time.Now()
	header := http.Header{}
	header.Set("X-Request-ID", startReq.RequestID)
	conn, _, err := c.dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		callErr := fmt.Errorf("dial ai service asr: %w", err)
		if c.fallback != nil {
			Warnf("service asr dial failed, fallback to local: %v", callErr)
			if fbErr := c.fallback.Run(ctx, audio, events, history); fbErr == nil {
				c.observeCall(startReq.RequestID, "asr.stream", startReq.TurnID, startAt, "fallback_ok", callErr)
				return nil
			} else {
				callErr = fmt.Errorf("%v; fallback failed: %v", callErr, fbErr)
			}
		}
		c.observeCall(startReq.RequestID, "asr.stream", startReq.TurnID, startAt, "error", callErr)
		return callErr
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, startBytes); err != nil {
		callErr := fmt.Errorf("send asr start to service: %w", err)
		if c.fallback != nil {
			Warnf("service asr start failed, fallback to local: %v", callErr)
			if fbErr := c.fallback.Run(ctx, audio, events, history); fbErr == nil {
				c.observeCall(startReq.RequestID, "asr.stream", startReq.TurnID, startAt, "fallback_ok", callErr)
				return nil
			} else {
				callErr = fmt.Errorf("%v; fallback failed: %v", callErr, fbErr)
			}
		}
		c.observeCall(startReq.RequestID, "asr.stream", startReq.TurnID, startAt, "error", callErr)
		return callErr
	}

	var finishRequested atomic.Bool
	sendFinish := func() error {
		finishRequested.Store(true)
		ctrl := AIServiceASRControl{Type: "finish"}
		b, _ := json.Marshal(ctrl)
		return conn.WriteMessage(websocket.TextMessage, b)
	}

	errCh := make(chan error, 2)
	go func() {
		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			case <-c.finishCh:
				if err := sendFinish(); err != nil {
					errCh <- fmt.Errorf("send asr finish to service: %w", err)
					return
				}
			case frame, ok := <-audio:
				if !ok {
					if err := sendFinish(); err != nil {
						errCh <- fmt.Errorf("send asr finish on audio close: %w", err)
						return
					}
					errCh <- nil
					return
				}
				if len(frame) == 0 {
					continue
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
					errCh <- fmt.Errorf("write asr audio to service: %w", err)
					return
				}
			}
		}
	}()

	go func() {
		defer func() {
			_ = conn.Close()
		}()
		for {
			mt, payload, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					errCh <- nil
					return
				}
				if finishRequested.Load() && websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					errCh <- nil
					return
				}
				errCh <- fmt.Errorf("read asr event from service: %w", err)
				return
			}
			if mt != websocket.TextMessage {
				continue
			}
			var evt AIServiceASREvent
			if err := json.Unmarshal(payload, &evt); err != nil {
				continue
			}
			switch evt.Type {
			case string(ASREventPartial):
				events <- ASREvent{Type: ASREventPartial, Text: evt.Text}
			case string(ASREventFinal):
				events <- ASREvent{Type: ASREventFinal, Text: evt.Text}
			case string(ASREventEndpoint):
				events <- ASREvent{Type: ASREventEndpoint}
			case "error":
				errCh <- fmt.Errorf("service asr error: %s", strings.TrimSpace(evt.Error))
				return
			}
		}
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		e := <-errCh
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	if firstErr == nil {
		Debugf("service asr stream finished in %v", time.Since(startAt))
	}
	result := "ok"
	if firstErr != nil {
		result = "error"
	}
	c.observeCall(startReq.RequestID, "asr.stream", startReq.TurnID, startAt, result, firstErr)
	return firstErr
}

func (c *ServiceASRClient) observeCall(requestID string, capability string, turnID uint64, startedAt time.Time, result string, err error) {
	if c == nil || c.observer == nil {
		return
	}
	rec := ServiceCallRecord{
		RequestID:   requestID,
		ServiceID:   "ai-doubao",
		Capability:  capability,
		TurnID:      turnID,
		StartedAtMS: startedAt.UnixMilli(),
		LatencyMS:   time.Since(startedAt).Milliseconds(),
		Result:      result,
	}
	if err != nil {
		rec.Error = err.Error()
	}
	c.observer.RecordCall(rec)
}

type ServiceLLMClient struct {
	baseURL        string
	requestTimeout time.Duration
	fallback       LLMClient
	observer       ServiceCallObserver
}

func (c *ServiceLLMClient) Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error) {
	startAt := time.Now()
	requestID := "svc-" + newRequestID()
	turnID := TurnIDFromContext(ctx)
	reqBody := AIServiceLLMStreamRequest{
		RequestID: requestID,
		TurnID:    turnID,
		Input:     input,
		History:   history,
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/v1/llm/stream", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	client := &http.Client{Timeout: c.requestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		if c.fallback != nil {
			Warnf("service llm failed, fallback to local: %v", err)
			final, fbErr := c.fallback.Stream(ctx, input, history, onDelta)
			if fbErr == nil {
				c.observeCall(requestID, "llm.stream", turnID, startAt, "fallback_ok", err)
				return final, nil
			}
			c.observeCall(requestID, "llm.stream", turnID, startAt, "error", fbErr)
			return "", fmt.Errorf("call llm stream from service: %w; fallback failed: %v", err, fbErr)
		}
		c.observeCall(requestID, "llm.stream", turnID, startAt, "error", err)
		return "", fmt.Errorf("call llm stream from service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		svcErr := fmt.Errorf("service llm status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		if c.fallback != nil {
			Warnf("service llm status error, fallback to local: %v", svcErr)
			final, fbErr := c.fallback.Stream(ctx, input, history, onDelta)
			if fbErr == nil {
				c.observeCall(requestID, "llm.stream", turnID, startAt, "fallback_ok", svcErr)
				return final, nil
			}
			c.observeCall(requestID, "llm.stream", turnID, startAt, "error", fbErr)
			return "", fmt.Errorf("%v; fallback failed: %v", svcErr, fbErr)
		}
		c.observeCall(requestID, "llm.stream", turnID, startAt, "error", svcErr)
		return "", svcErr
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var evt AIServiceLLMStreamEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}
		if evt.Type == "delta" {
			full.WriteString(evt.Text)
			if onDelta != nil {
				onDelta(evt.Text)
			}
			continue
		}
		if evt.Type == "error" {
			svcErr := fmt.Errorf("service llm error: %s", strings.TrimSpace(evt.Error))
			if c.fallback != nil {
				final, fbErr := c.fallback.Stream(ctx, input, history, onDelta)
				if fbErr == nil {
					c.observeCall(requestID, "llm.stream", turnID, startAt, "fallback_ok", svcErr)
					return final, nil
				}
				c.observeCall(requestID, "llm.stream", turnID, startAt, "error", fbErr)
				return "", fmt.Errorf("%v; fallback failed: %v", svcErr, fbErr)
			}
			c.observeCall(requestID, "llm.stream", turnID, startAt, "error", svcErr)
			return "", svcErr
		}
		if evt.Type == "final" {
			final := strings.TrimSpace(evt.Text)
			if final == "" {
				final = strings.TrimSpace(full.String())
			}
			Debugf("service llm stream finished in %v", time.Since(startAt))
			c.observeCall(requestID, "llm.stream", turnID, startAt, "ok", nil)
			return final, nil
		}
	}
	if err := scanner.Err(); err != nil {
		c.observeCall(requestID, "llm.stream", turnID, startAt, "error", err)
		return "", err
	}
	c.observeCall(requestID, "llm.stream", turnID, startAt, "ok", nil)
	return strings.TrimSpace(full.String()), nil
}

func (c *ServiceLLMClient) observeCall(requestID string, capability string, turnID uint64, startedAt time.Time, result string, err error) {
	if c == nil || c.observer == nil {
		return
	}
	rec := ServiceCallRecord{
		RequestID:   requestID,
		ServiceID:   "ai-doubao",
		Capability:  capability,
		TurnID:      turnID,
		StartedAtMS: startedAt.UnixMilli(),
		LatencyMS:   time.Since(startedAt).Milliseconds(),
		Result:      result,
	}
	if err != nil {
		rec.Error = err.Error()
	}
	c.observer.RecordCall(rec)
}

type ServiceTTSClient struct {
	baseURL        string
	requestTimeout time.Duration
	fallback       TTSClient
	observer       ServiceCallObserver
}

func (c *ServiceTTSClient) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	startAt := time.Now()
	requestID := "svc-" + newRequestID()
	turnID := TurnIDFromContext(ctx)
	reqBody := AIServiceTTSSynthesizeRequest{RequestID: requestID, TurnID: turnID, Text: text}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/v1/tts/synthesize", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	client := &http.Client{Timeout: c.requestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		if c.fallback != nil {
			audio, format, fbErr := c.fallback.Synthesize(ctx, text)
			if fbErr == nil {
				c.observeCall(requestID, "tts.synthesize", turnID, startAt, "fallback_ok", err)
				return audio, format, nil
			}
			c.observeCall(requestID, "tts.synthesize", turnID, startAt, "error", fbErr)
			return nil, "", fmt.Errorf("call tts from service: %w; fallback failed: %v", err, fbErr)
		}
		c.observeCall(requestID, "tts.synthesize", turnID, startAt, "error", err)
		return nil, "", fmt.Errorf("call tts from service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		svcErr := fmt.Errorf("service tts status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		if c.fallback != nil {
			audio, format, fbErr := c.fallback.Synthesize(ctx, text)
			if fbErr == nil {
				c.observeCall(requestID, "tts.synthesize", turnID, startAt, "fallback_ok", svcErr)
				return audio, format, nil
			}
			c.observeCall(requestID, "tts.synthesize", turnID, startAt, "error", fbErr)
			return nil, "", fmt.Errorf("%v; fallback failed: %v", svcErr, fbErr)
		}
		c.observeCall(requestID, "tts.synthesize", turnID, startAt, "error", svcErr)
		return nil, "", svcErr
	}
	var ttsResp AIServiceTTSSynthesizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ttsResp); err != nil {
		c.observeCall(requestID, "tts.synthesize", turnID, startAt, "error", err)
		return nil, "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ttsResp.AudioBase64))
	if err != nil {
		c.observeCall(requestID, "tts.synthesize", turnID, startAt, "error", err)
		return nil, "", fmt.Errorf("decode service tts audio: %w", err)
	}
	if ttsResp.Format == "" {
		ttsResp.Format = "audio/mpeg"
	}
	Debugf("service tts synth finished in %v", time.Since(startAt))
	c.observeCall(requestID, "tts.synthesize", turnID, startAt, "ok", nil)
	return raw, ttsResp.Format, nil
}

func (c *ServiceTTSClient) observeCall(requestID string, capability string, turnID uint64, startedAt time.Time, result string, err error) {
	if c == nil || c.observer == nil {
		return
	}
	rec := ServiceCallRecord{
		RequestID:   requestID,
		ServiceID:   "ai-doubao",
		Capability:  capability,
		TurnID:      turnID,
		StartedAtMS: startedAt.UnixMilli(),
		LatencyMS:   time.Since(startedAt).Milliseconds(),
		Result:      result,
	}
	if err != nil {
		rec.Error = err.Error()
	}
	c.observer.RecordCall(rec)
}

func buildServiceWSURL(base string, path string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// keep as-is
	default:
		return "", fmt.Errorf("unsupported base url scheme: %s", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String(), nil
}
