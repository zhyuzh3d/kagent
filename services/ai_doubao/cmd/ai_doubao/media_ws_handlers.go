package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	app "kagent/services/ai_doubao/internal/app"

	"github.com/gorilla/websocket"
)

func handleASRWS(w http.ResponseWriter, r *http.Request, cfg *app.ModelConfig, upgrader websocket.Upgrader) {
	reqID := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Request-ID")), "svc-"+app.NewRequestID())
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "upgrade failed", http.StatusBadRequest)
		return
	}
	defer conn.Close()
	app.Debugf("[ai_doubao] asr stream open request_id=%s", reqID)

	mt, payload, err := conn.ReadMessage()
	if err != nil || mt != websocket.TextMessage {
		_ = conn.WriteJSON(app.AIServiceASREvent{Type: "error", Error: "missing start request"})
		return
	}
	var startReq app.AIServiceASRStart
	if err := json.Unmarshal(payload, &startReq); err != nil || strings.TrimSpace(startReq.Type) != "start" {
		_ = conn.WriteJSON(app.AIServiceASREvent{Type: "error", Error: "invalid start request"})
		return
	}

	asr := app.NewDoubaoASRClient(cfg.ASR, nil)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	audioCh := make(chan []byte, 64)
	events := make(chan app.ASREvent, 64)
	sendDone := make(chan struct{})

	go func() {
		defer close(sendDone)
		for evt := range events {
			out := app.AIServiceASREvent{
				Type: string(evt.Type),
				Text: evt.Text,
			}
			if err := conn.WriteJSON(out); err != nil {
				cancel()
				return
			}
		}
	}()
	go func() {
		defer close(audioCh)
		for {
			mt, raw, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if len(raw) == 0 {
					continue
				}
				frame := append([]byte(nil), raw...)
				select {
				case audioCh <- frame:
				case <-ctx.Done():
					return
				}
			case websocket.TextMessage:
				var ctrl app.AIServiceASRControl
				if err := json.Unmarshal(raw, &ctrl); err != nil {
					continue
				}
				if strings.EqualFold(strings.TrimSpace(ctrl.Type), "finish") {
					asr.Finish()
				}
			}
		}
	}()

	runErr := asr.Run(ctx, audioCh, events, startReq.History)
	close(events)
	<-sendDone
	if runErr != nil && ctx.Err() == nil {
		_ = conn.WriteJSON(app.AIServiceASREvent{Type: "error", Error: runErr.Error()})
		app.Warnf("[ai_doubao] asr stream failed request_id=%s turn_id=%d err=%v", firstNonEmpty(startReq.RequestID, reqID), startReq.TurnID, runErr)
		return
	}
	app.Debugf("[ai_doubao] asr stream closed request_id=%s turn_id=%d", firstNonEmpty(startReq.RequestID, reqID), startReq.TurnID)
}

func handleLLMWS(w http.ResponseWriter, r *http.Request, cfg *app.ModelConfig, upgrader websocket.Upgrader) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "upgrade failed", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	_, payload, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var req app.AIServiceLLMStreamRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		_ = conn.WriteJSON(app.AIServiceLLMStreamEvent{Type: "error", Error: "invalid request"})
		return
	}
	reqID := firstNonEmpty(req.RequestID, "svc-"+app.NewRequestID())
	app.Debugf("[ai_doubao] llm ws request_id=%s turn_id=%d", reqID, req.TurnID)

	llm := app.NewDoubaoLLMClient(cfg.ActiveChat(), nil)
	var writeMu sync.Mutex
	push := func(evt app.AIServiceLLMStreamEvent) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(evt)
	}

	finalText, err := llm.Stream(r.Context(), req.Input, req.History, func(delta string) {
		if strings.TrimSpace(delta) == "" {
			return
		}
		_ = push(app.AIServiceLLMStreamEvent{Type: "delta", Text: delta})
	})
	if err != nil {
		_ = push(app.AIServiceLLMStreamEvent{Type: "error", Error: err.Error()})
		app.Warnf("[ai_doubao] llm ws failed request_id=%s turn_id=%d err=%v", reqID, req.TurnID, err)
		return
	}
	_ = push(app.AIServiceLLMStreamEvent{Type: "final", Text: finalText})
	app.Debugf("[ai_doubao] llm ws finished request_id=%s turn_id=%d", reqID, req.TurnID)
}

func synthesizeTTS(ctx context.Context, cfg *app.ModelConfig, text string) ([]byte, string, error) {
	tts := app.NewDoubaoTTSClient(cfg.TTS, nil)
	return tts.Synthesize(ctx, strings.TrimSpace(text))
}
