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
}

func NewServiceProviderFactory(cfg AIServiceConfig) *ServiceProviderFactory {
	effective := cfg
	if effective.RequestTimeoutMS <= 0 {
		effective.RequestTimeoutMS = 70000
	}
	return &ServiceProviderFactory{
		baseURL:        strings.TrimRight(strings.TrimSpace(effective.BaseURL), "/"),
		requestTimeout: time.Duration(effective.RequestTimeoutMS) * time.Millisecond,
	}
}

func (f *ServiceProviderFactory) Name() string {
	return "service"
}

func (f *ServiceProviderFactory) NewASRClient(_ *ModelConfig, _ *RuntimeConfigManager) ASRClient {
	return &ServiceASRClient{
		baseURL: f.baseURL,
		dialer:  &websocket.Dialer{HandshakeTimeout: 8 * time.Second},
	}
}

func (f *ServiceProviderFactory) NewLLMClient(_ *ModelConfig, _ *RuntimeConfigManager) LLMClient {
	return &ServiceLLMClient{
		baseURL:        f.baseURL,
		requestTimeout: f.requestTimeout,
	}
}

func (f *ServiceProviderFactory) NewTTSClient(_ *ModelConfig, _ *RuntimeConfigManager) TTSClient {
	return &ServiceTTSClient{
		baseURL:        f.baseURL,
		requestTimeout: f.requestTimeout,
	}
}

type ServiceASRClient struct {
	baseURL  string
	dialer   *websocket.Dialer
	finishCh chan struct{}
}

func (c *ServiceASRClient) Finish() {
	if c.finishCh == nil {
		return
	}
	select {
	case c.finishCh <- struct{}{}:
	default:
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
		Type:    "start",
		History: history,
	}
	startBytes, _ := json.Marshal(startReq)

	startAt := time.Now()
	conn, _, err := c.dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial ai service asr: %w", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, startBytes); err != nil {
		return fmt.Errorf("send asr start to service: %w", err)
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
	return firstErr
}

type ServiceLLMClient struct {
	baseURL        string
	requestTimeout time.Duration
}

func (c *ServiceLLMClient) Stream(ctx context.Context, input string, history []ChatMessage, onDelta func(string)) (string, error) {
	startAt := time.Now()
	reqBody := AIServiceLLMStreamRequest{
		Input:   input,
		History: history,
	}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/v1/llm/stream", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: c.requestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call llm stream from service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("service llm status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
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
			return "", fmt.Errorf("service llm error: %s", strings.TrimSpace(evt.Error))
		}
		if evt.Type == "final" {
			final := strings.TrimSpace(evt.Text)
			if final == "" {
				final = strings.TrimSpace(full.String())
			}
			Debugf("service llm stream finished in %v", time.Since(startAt))
			return final, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(full.String()), nil
}

type ServiceTTSClient struct {
	baseURL        string
	requestTimeout time.Duration
}

func (c *ServiceTTSClient) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	startAt := time.Now()
	reqBody := AIServiceTTSSynthesizeRequest{Text: text}
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/v1/tts/synthesize", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: c.requestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call tts from service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("service tts status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ttsResp AIServiceTTSSynthesizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&ttsResp); err != nil {
		return nil, "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ttsResp.AudioBase64))
	if err != nil {
		return nil, "", fmt.Errorf("decode service tts audio: %w", err)
	}
	if ttsResp.Format == "" {
		ttsResp.Format = "audio/mpeg"
	}
	Debugf("service tts synth finished in %v", time.Since(startAt))
	return raw, ttsResp.Format, nil
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
