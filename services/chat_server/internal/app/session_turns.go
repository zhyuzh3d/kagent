package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Session) handleControl(ctrl ControlMessage) {
	typ := strings.ToLower(strings.TrimSpace(ctrl.Type))
	switch typ {
	case "start":
		if s.started.Load() {
			s.setState(StateListening, "already started")
			return
		}
		s.started.Store(true)
		s.setUserTurnActive(false)
		s.appendHistoryMessage(ChatMessage{
			TurnID:      ctrl.TurnID,
			Role:        RoleSystem,
			Category:    CategoryPhase,
			MessageType: TypeConvoStart,
			PayloadJSON: `{"reason":"start"}`,
			CreatedAtMS: nowMS(),
		})

		tid := s.adoptTurnID(ctrl.TurnID)
		s.startASRTurn(tid) // Start the initial ASR connection for this session

		s.setState(StateListening, "microphone streaming")
	case "stop":
		s.cancelASR()
		s.appendHistoryMessage(ChatMessage{
			TurnID:      ctrl.TurnID,
			Role:        RoleSystem,
			Category:    CategoryPhase,
			MessageType: TypeConvoStop,
			PayloadJSON: `{"reason":"stop"}`,
			CreatedAtMS: nowMS(),
		})
		s.stopAll()
		s.setUserTurnActive(false)
		s.setState(StateIdle, "stopped")
	case "interrupt":
		tid := s.adoptTurnID(ctrl.TurnID)
		if tid != s.lastInterruptTurnID {
			Infof("[Turn:%d] -> VAD interrupt received (reason=%s)", tid, ctrl.Reason)
			s.lastInterruptTurnID = tid
		}

		s.interruptTurnWithReason(InterruptVAD)

		s.setState(StateInterrupted, "interrupted")
		s.setState(StateListening, "ready for next utterance")
	case "trigger_llm":
		// The new unified trigger from Client-Driven Architecture.
		tid := s.adoptTurnID(ctrl.TurnID)

		s.asr.Finish()
		select {
		case <-s.asrFinalCh:
			Debugf("[Turn:%d] ASR final received for trigger_llm", tid)
		case <-time.After(s.triggerLLMWaitFinal()):
			Warnf("[Turn:%d] ASR final wait timed out for trigger_llm; falling back", tid)
		}

		// Always consume backend ASR text to prevent stale text leaking to next turn.
		lastSpeech := s.consumeLastASRText()
		// Prefer backend final/partial text if present; fall back to frontend text snapshot.
		text := lastSpeech
		if text == "" {
			text = ctrl.Text
		}

		if text != "" {
			Infof("[Turn:%d] %q -> LLM Triggered", tid, Snippet(text))
		} else {
			Debugf("[Turn:%d] \"\" -> LLM Trigger (skipped, empty text)", tid)
		}

		s.cancelASR()
		s.setUserTurnActive(false)

		if text != "" {
			s.startTurn(text, tid)
			return
		}
		_ = s.sendEvent(EventMessage{Type: "turn_nack", TsMS: nowMS(), TurnID: tid})
		Infof("[Turn:%d] turn_nack sent to frontend, skipped DB persistence", tid)
		s.setState(StateListening, "no speech detected")
	case "call_ai_reply":
		if strings.TrimSpace(ctrl.Reason) != "" {
			Infof("[Turn:%d] call_ai_reply requested (%s)", ctrl.TurnID, strings.TrimSpace(ctrl.Reason))
		} else {
			Infof("[Turn:%d] call_ai_reply requested", ctrl.TurnID)
		}
		s.requestFollowupReply()
	case "start_listen":
		// Explicit signal from frontend that a new turn is starting voice input
		tid := s.adoptTurnID(ctrl.TurnID)
		Infof("[Turn:%d] -> VAD listening started", tid)
		s.setUserTurnActive(true)
		s.cancelASR()
		s.startASRTurn(tid)
		s.setState(StateListening, "listening to user")
	case "action_result":
		s.handleActionResult(ctrl)
	case "state_change":
		s.handleStateChange(ctrl)
	case "page_close":
		s.appendHistoryMessage(ChatMessage{
			TurnID:      ctrl.TurnID,
			Role:        RoleSystem,
			Category:    CategoryPhase,
			MessageType: TypePageClose,
			PayloadJSON: fmt.Sprintf(`{"reason":%q}`, strings.TrimSpace(ctrl.Reason)),
			CreatedAtMS: nowMS(),
		})
	case "config_change":
		s.handleConfigChange(ctrl)
	case "fetch_history":
		s.handleFetchHistory(ctrl)
	default:
		s.emitError(s.turnID.Load(), "unsupported_control", "unsupported control type: "+typ, true)
	}
}

func (s *Session) handleASREvent(evt ASREvent, explicitTurnID uint64) {
	switch evt.Type {
	case ASREventPartial:
		s.lastASRTextMu.Lock()
		if s.endpointConsumed {
			// A new utterance has started after the previous one was consumed!
			// Interrupt any ongoing AI generation for the previous turn.
			s.interruptTurnLocked()
			// Increment turn ID so this new utterance gets a fresh turn.
			s.turnID.Add(1)
			s.endpointConsumed = false
			s.committedASRText = ""
		}
		fullText := strings.TrimSpace(s.committedASRText + " " + evt.Text)
		s.lastASRText = fullText
		s.lastASRTextMu.Unlock()

		s.maybeInterruptForRecognizedSpeech(explicitTurnID, fullText)

		s.setState(StateRecognizing, "receiving speech")
		_ = s.sendEvent(NewTextEvent("asr_partial", explicitTurnID, fullText))

	case ASREventFinal:
		s.lastASRTextMu.Lock()
		if !s.endpointConsumed {
			s.committedASRText = strings.TrimSpace(s.committedASRText + " " + evt.Text)
			s.lastASRText = s.committedASRText
		}
		fullText := s.lastASRText
		s.lastASRTextMu.Unlock()

		s.maybeInterruptForRecognizedSpeech(explicitTurnID, fullText)
		s.setState(StateRecognizing, "speech finalized")

		if text := strings.TrimSpace(evt.Text); text != "" {
			Infof("[Turn:%d] %q -> ASR final (segment), full=%q", explicitTurnID, Snippet(text), Snippet(fullText))
		}

		// Always send asr_final with the explicitly bound turn ID!
		_ = s.sendEvent(NewTextEvent("asr_final", explicitTurnID, fullText))
		select {
		case s.asrFinalCh <- struct{}{}:
		default:
		}

	case ASREventEndpoint:
		s.lastASRTextMu.Lock()
		text := strings.TrimSpace(s.lastASRText)
		s.endpointConsumed = true
		s.lastASRTextMu.Unlock()

		// In the pure Client-Driven architecture, ASR Endpoint MUST NOT securely trigger the LLM.
		// The LLM trigger authority belongs entirely to the Frontend via `trigger_llm`.
		// But during transition, we only log it.
		if text != "" {
			Debugf("[Turn:%d] %q -> ASR endpoint, await trigger_llm", explicitTurnID, Snippet(text))
		}
	}
}

// interruptTurnLocked performs interruption without needing s.lastASRTextMu since it might be held
func (s *Session) interruptTurnLocked() {
	s.turnMu.Lock()
	if s.turnCancel != nil {
		if s.lastStartedTurnID > 0 {
			s.recordTurnInterrupt(s.lastStartedTurnID, InterruptVAD)
		}
		s.turnCancel()
		s.turnCancel = nil
	}
	s.turnMu.Unlock()
}

func shouldInterruptForRecognizedSpeech(state string, activeGeneratedTurnID uint64, speechTurnID uint64, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if speechTurnID == 0 || activeGeneratedTurnID == 0 || speechTurnID <= activeGeneratedTurnID {
		return false
	}
	return state == StateThinking || state == StateSpeaking
}

func (s *Session) maybeInterruptForRecognizedSpeech(turnID uint64, text string) {
	// Backend-driven proactive interruption is disabled in favor of Client-Driven architecture.
	// The frontend now decides when to interrupt based on ASR text and local context.
	// We only log the event here for debugging purposes.
	activeState := s.getState()
	s.turnMu.Lock()
	activeGeneratedTurnID := s.lastStartedTurnID
	hasActiveTurn := s.turnCancel != nil
	s.turnMu.Unlock()

	if hasActiveTurn && shouldInterruptForRecognizedSpeech(activeState, activeGeneratedTurnID, turnID, text) {
		Debugf("[Turn:%d] %q -> Recognized speech detected; awaiting frontend interrupt command", turnID, Snippet(text))
	}
}

func (s *Session) adoptTurnID(proposed uint64) uint64 {
	current := s.turnID.Load()
	if proposed > current {
		s.turnID.Store(proposed)
		return proposed
	}
	return current
}

func (s *Session) cancelASR() {
	s.asrCancelMu.Lock()
	defer s.asrCancelMu.Unlock()
	if s.asrCancel != nil {
		s.asrCancel()
		s.asrCancel = nil
	}
}

// startASRTurn creates a new physically isolated ASR WebSocket connection exactly tied to one turn.
func (s *Session) startASRTurn(turnID uint64) {
	s.cancelASR() // Drop old connection if it exists

	s.lastASRTextMu.Lock()
	if s.committedTurnID == turnID {
		// Crucial: Promoted last known partial text to committed if we are restarting
		// ASR for the SAME turn (e.g. on barge-in interrupt).
		if s.lastASRText != "" {
			s.committedASRText = s.lastASRText
		}
	} else {
		// New turn (manually triggered or auto-incremented)
		s.committedTurnID = turnID
		s.committedASRText = ""
		s.lastASRText = ""
		s.endpointConsumed = false
	}
	s.lastASRTextMu.Unlock()
	select {
	case <-s.asrFinalCh:
	default:
	}

	ctx, cancel := context.WithCancel(s.rootCtx)
	ctx = WithTurnID(ctx, turnID)
	s.asrCancelMu.Lock()
	s.asrCancel = cancel
	s.asrCancelMu.Unlock()

	history := s.getHistory() // snapshot current history for this specific turn

	go func() {
		defer cancel() // auto clean if finished normally
		Debugf("[Turn:%d] Connecting dedicated ASR WebSocket", turnID)

		events := make(chan ASREvent, 64)
		stopCh := make(chan struct{})

		go func() {
			for {
				select {
				case <-ctx.Done():
					close(stopCh)
					return
				case evt, ok := <-events:
					if !ok {
						close(stopCh)
						return
					}
					s.handleASREvent(evt, turnID) // PERFECT TAGGING!
				}
			}
		}()

		err := s.asr.Run(ctx, s.audioIn, events, history)
		close(events)
		<-stopCh // wait for events to flush

		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil && s.rootCtx.Err() == nil {
			Errorf("[Turn:%d] asr run error: %v", turnID, err)
			s.emitError(turnID, "asr_failed", err.Error(), true)
		}
		Debugf("[Turn:%d] Dedicated ASR connection closed", turnID)
	}()
}

func (s *Session) startTurn(text string, targetTurnID uint64) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}
	s.clearPendingFollowupsForUserTurn()

	s.turnMu.Lock()
	if s.lastStartedTurnID == targetTurnID {
		remapped := s.turnID.Add(1)
		Warnf("[Turn:%d] startTurn turn_id collision detected; remap to turn=%d", targetTurnID, remapped)
		targetTurnID = remapped
	}
	s.lastStartedTurnID = targetTurnID
	s.turnMu.Unlock()

	s.interruptTurnWithReason(InterruptOther)
	// Removed s.turnID.Add(1) - TurnID is now incremented upon receiving the first ASREventPartial for a new utterance
	ctx, cancel := context.WithCancel(s.rootCtx)
	ctx = WithTurnID(ctx, targetTurnID)
	s.turnMu.Lock()
	s.turnCancel = cancel
	s.turnMu.Unlock()
	history := s.getHistory()

	s.appendHistoryMessage(ChatMessage{
		TurnID:      targetTurnID,
		Role:        RoleUser,
		Category:    CategoryChat,
		MessageType: TypeUserMessage,
		Content:     clean,
		PayloadJSON: fmt.Sprintf(`{"text":%q,"origin":"user_turn"}`, clean),
		CreatedAtMS: nowMS(),
	})

	s.setState(StateThinking, "ai is thinking")
	go func(turnID uint64, input string, hist []ChatMessage) {
		err := s.pipeline.RunTurn(ctx, turnID, input, hist)
		if errors.Is(err, context.Canceled) {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusInterrupted, s.consumeTurnInterrupt(turnID), 0)
			return
		}
		if err != nil {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusError, InterruptOther, 0)
			Errorf("[Turn:%d] turn failed: %v", turnID, err)
			s.emitError(turnID, "turn_failed", err.Error(), true)
			s.setTurnState(turnID, StateError, "turn failed")
			return
		}
		if ctx.Err() == nil {
			s.setTurnState(turnID, StateListening, "ready for next utterance")
			s.tryStartContinuation()
		}
	}(targetTurnID, clean, history)
}

func (s *Session) interruptTurnWithReason(reason string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.turnCancel != nil {
		if s.lastStartedTurnID > 0 {
			s.recordTurnInterrupt(s.lastStartedTurnID, reason)
		}
		s.turnCancel()
		s.turnCancel = nil
	}
	s.flushTTSQueue()
}

func (s *Session) stopAll() {
	s.interruptTurnWithReason(InterruptManual)
	s.started.Store(false)
	s.actionMu.Lock()
	s.userTurnActive = false
	s.continuationRunning = false
	s.pendingFollowups = nil
	s.followupReplyRequested = false
	if s.followupFlushTimer != nil {
		s.followupFlushTimer.Stop()
		s.followupFlushTimer = nil
	}
	s.actionMu.Unlock()
	s.actionRefMu.Lock()
	s.actionCallRefIDs = map[string]string{}
	s.actionRefMu.Unlock()
	s.asrCancelMu.Lock()
	if s.asrCancel != nil {
		s.asrCancel()
		s.asrCancel = nil
	}
	s.asrCancelMu.Unlock()
	s.flushAudioQueue()
}
