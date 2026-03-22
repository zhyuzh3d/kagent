package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Session) setUserTurnActive(active bool) {
	s.actionMu.Lock()
	s.userTurnActive = active
	s.actionMu.Unlock()
}

func (s *Session) clearPendingFollowupsForUserTurn() {
	s.actionMu.Lock()
	s.pendingFollowups = nil
	s.followupReplyRequested = false
	if s.followupFlushTimer != nil {
		s.followupFlushTimer.Stop()
		s.followupFlushTimer = nil
	}
	s.actionMu.Unlock()
}

func (s *Session) enqueueFollowupMessage(message ChatMessage, aggregate bool) {
	s.actionMu.Lock()
	s.pendingFollowups = append(s.pendingFollowups, message)
	if aggregate {
		if s.followupFlushTimer != nil {
			s.followupFlushTimer.Stop()
		}
		s.followupFlushTimer = time.AfterFunc(1*time.Second, func() {
			s.actionMu.Lock()
			s.followupFlushTimer = nil
			requested := s.followupReplyRequested
			s.actionMu.Unlock()
			if requested {
				s.tryStartContinuation()
			}
		})
		s.actionMu.Unlock()
		return
	}
	requested := s.followupReplyRequested
	hasTimer := s.followupFlushTimer != nil
	s.actionMu.Unlock()
	if requested && !hasTimer {
		s.tryStartContinuation()
	}
}

func (s *Session) requestFollowupReply() {
	s.actionMu.Lock()
	s.followupReplyRequested = true
	s.actionMu.Unlock()
	s.tryStartContinuation()
}

func (s *Session) tryStartContinuation() {
	if s.rootCtx == nil || s.rootCtx.Err() != nil {
		return
	}
	s.actionMu.Lock()
	if s.userTurnActive || s.continuationRunning || len(s.pendingFollowups) == 0 || !s.started.Load() || s.followupFlushTimer != nil || !s.followupReplyRequested {
		s.actionMu.Unlock()
		return
	}
	s.pendingFollowups = nil
	s.followupReplyRequested = false
	s.continuationRunning = true
	s.continuationSeq++
	continuationSeq := s.continuationSeq
	s.actionMu.Unlock()

	turnID := s.turnID.Add(1)
	s.startContinuationTurn(turnID, continuationSeq)
}

func concurrentActionHint(result map[string]any) int {
	if len(result) == 0 {
		return 0
	}
	raw, ok := result["concurrent_actions"]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		if v < 0 {
			return 0
		}
		return int(v)
	default:
		return 0
	}
}

func (s *Session) startContinuationTurn(turnID uint64, continuationSeq uint64) {
	history := s.getHistory()
	ctx, cancel := context.WithCancel(s.rootCtx)

	s.turnMu.Lock()
	s.turnCancel = cancel
	s.lastStartedTurnID = turnID
	s.turnMu.Unlock()
	s.setTurnState(turnID, StateThinking, fmt.Sprintf("continuation #%d", continuationSeq))

	go func() {
		defer func() {
			s.turnMu.Lock()
			if s.lastStartedTurnID == turnID {
				s.turnCancel = nil
			}
			s.turnMu.Unlock()
			s.actionMu.Lock()
			s.continuationRunning = false
			s.actionMu.Unlock()
			s.tryStartContinuation()
		}()
		err := s.pipeline.RunTurn(ctx, turnID, "", history)
		if errors.Is(err, context.Canceled) {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusInterrupted, s.consumeTurnInterrupt(turnID), 0)
			return
		}
		if err != nil {
			s.finalizeAssistantMessage(turnID, "", CompletionStatusError, InterruptOther, 0)
			Errorf("[Turn:%d] continuation failed: %v", turnID, err)
			s.emitError(turnID, "continuation_failed", err.Error(), true)
			s.setTurnState(turnID, StateError, "continuation failed")
			return
		}
		if ctx.Err() == nil {
			s.setTurnState(turnID, StateListening, "continuation finished")
		}
	}()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(b)
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func jsonOrEmptyMap(raw string) map[string]any {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(clean), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func actionIDFromJSON(actionJSON string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(actionJSON)), &payload); err != nil {
		return ""
	}
	return firstNonEmpty(asTrimmedString(payload["id"]), asTrimmedString(payload["action_id"]))
}

func reportStateFromStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "complete", "completed":
		return "success"
	case "pending", "blocked":
		return "pending"
	default:
		return "fail"
	}
}

func (s *Session) bindActionCallRef(actionID string, messageID string) {
	cleanActionID := strings.TrimSpace(actionID)
	cleanMessageID := strings.TrimSpace(messageID)
	if cleanActionID == "" || cleanMessageID == "" {
		return
	}
	s.actionRefMu.Lock()
	if s.actionCallRefIDs == nil {
		s.actionCallRefIDs = map[string]string{}
	}
	s.actionCallRefIDs[cleanActionID] = cleanMessageID
	s.actionRefMu.Unlock()
}

func (s *Session) resolveActionCallRef(actionID string, actionName string) string {
	cleanActionID := strings.TrimSpace(actionID)
	if cleanActionID != "" {
		s.actionRefMu.Lock()
		if messageID := strings.TrimSpace(s.actionCallRefIDs[cleanActionID]); messageID != "" {
			s.actionRefMu.Unlock()
			return messageID
		}
		s.actionRefMu.Unlock()
	}
	targetName := strings.TrimSpace(actionName)
	if targetName == "" {
		return ""
	}
	history := s.getHistory()
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != RoleAssistant {
			continue
		}
		action := jsonOrEmptyMap(msg.ActionJSON)
		if strings.ToLower(asTrimmedString(action["type"])) != TypeActionCall {
			continue
		}
		path := firstNonEmpty(asTrimmedString(action["path"]), asTrimmedString(action["name"]))
		if path != targetName {
			continue
		}
		if msg.MessageID != "" {
			return msg.MessageID
		}
	}
	return ""
}

// getHistory returns a snapshot of the current conversation history.
