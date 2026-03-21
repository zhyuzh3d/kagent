package app

import (
	"encoding/json"
	"strings"
)

func (s *Session) handleFetchHistory(ctrl ControlMessage) {
	if s.chatStore == nil {
		Warnf("chatStore is nil, ignoring fetch_history")
		return
	}
	limit := ctrl.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	beforeID := ctrl.BeforeID
	if beforeID <= 0 {
		beforeID = ctrl.Cursor
	}

	Debugf("received fetch_history: before_id=%d cursor=%d limit=%d", beforeID, ctrl.Cursor, limit)
	history, hasMore, err := s.chatStore.LoadContextBeforeWithMode(beforeID, limit, ctrl.ShowMore)
	if err != nil {
		Errorf("fetch history failed: %v", err)
		return
	}

	Debugf("fetch_history returning %d messages, hasMore=%v", len(history), hasMore)

	evt := EventMessage{
		Type:     "history_sync",
		TsMS:     nowMS(),
		TurnID:   s.turnID.Load(),
		Messages: history,
		HasMore:  hasMore,
	}
	if err := s.sendEvent(evt); err != nil {
		Errorf("send history_sync failed: %v", err)
	}
}

func (s *Session) consumeLastASRText() string {
	s.lastASRTextMu.Lock()
	defer s.lastASRTextMu.Unlock()
	out := strings.TrimSpace(s.lastASRText)
	s.lastASRText = ""
	s.committedASRText = ""
	s.committedTurnID = 0
	return out
}

func (s *Session) sessionAnchorLimit() int {
	limit := s.publicConfig().Chat.Session.MaxHistoryMessages
	if limit <= 0 {
		limit = defaultPublicConfig().Chat.Session.MaxHistoryMessages
	}
	if limit <= 0 {
		limit = 20
	}
	return limit
}

func (s *Session) sessionMessageCap() int {
	return maxInt(s.sessionAnchorLimit()*4, 64)
}

// applyHistoryWindowLocked keeps the in-memory history aligned with the sliding anchor window.
// Must be called with historyMu held.
func (s *Session) applyHistoryWindowLocked() {
	anchorLimit := s.sessionAnchorLimit()
	if anchorLimit > 0 {
		anchorSeen := 0
		anchorIdx := -1
		for i := len(s.chatHistory) - 1; i >= 0; i-- {
			if !isAnchorMessage(s.chatHistory[i]) {
				continue
			}
			anchorSeen++
			if anchorSeen == anchorLimit {
				anchorIdx = i
				break
			}
		}
		if anchorIdx > 0 {
			s.chatHistory = append([]ChatMessage(nil), s.chatHistory[anchorIdx:]...)
		}
	}
	if capLimit := s.sessionMessageCap(); len(s.chatHistory) > capLimit {
		s.chatHistory = append([]ChatMessage(nil), s.chatHistory[len(s.chatHistory)-capLimit:]...)
	}
}

func (s *Session) appendAssistantDraft(turnID uint64, delta string) {
	if turnID == 0 || strings.TrimSpace(delta) == "" {
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	if s.assistantDrafts == nil {
		s.assistantDrafts = map[uint64]string{}
	}
	if s.assistantFinalized == nil {
		s.assistantFinalized = map[uint64]struct{}{}
	}
	if _, finalized := s.assistantFinalized[turnID]; finalized {
		return
	}
	s.assistantDrafts[turnID] += delta
}

func (s *Session) finalizeAssistantMessage(turnID uint64, finalText string, status string, interrupt string, interruptAtMS int64) {
	if turnID == 0 {
		return
	}
	s.draftMu.Lock()
	if s.assistantDrafts == nil {
		s.assistantDrafts = map[uint64]string{}
	}
	if s.assistantFinalized == nil {
		s.assistantFinalized = map[uint64]struct{}{}
	}
	if _, finalized := s.assistantFinalized[turnID]; finalized {
		s.draftMu.Unlock()
		return
	}
	draft := strings.TrimSpace(s.assistantDrafts[turnID])
	delete(s.assistantDrafts, turnID)
	s.assistantFinalized[turnID] = struct{}{}
	s.draftMu.Unlock()

	rawText := firstNonEmpty(draft, strings.TrimSpace(finalText))
	if rawText == "" {
		return
	}
	env := parseAssistantEnvelope(rawText)
	say := firstNonEmpty(strings.TrimSpace(env.Say), strings.TrimSpace(finalText))
	aside := strings.TrimSpace(env.Aside)
	content := strings.TrimSpace(composeMessageContent(say, aside))
	if content == "" {
		content = strings.TrimSpace(finalText)
	}
	if content == "" {
		content = strings.TrimSpace(env.Say)
	}
	if content == "" {
		content = formatMalformedPreview(rawText)
	}
	normalizedStatus := normalizeCompletionStatus(status)
	if normalizedStatus == "" {
		normalizedStatus = CompletionStatusComplete
	}
	normalizedInterrupt := normalizeInterrupt(interrupt)
	if normalizedInterrupt == "" {
		normalizedInterrupt = InterruptNone
	}
	payload := map[string]any{
		"say":               say,
		"aside":             aside,
		"action":            jsonOrEmptyMap(env.ActionJSON),
		"raw_data":          jsonOrEmptyMap(env.RawData),
		"parse_error":       env.ParseError,
		"completion_status": normalizedStatus,
		"interrupt":         normalizedInterrupt,
	}
	if content != "" {
		payload["text"] = content
	}
	if draft != "" && draft != rawText {
		payload["partial_text"] = draft
	}
	entry := ChatMessage{
		TurnID:               turnID,
		Role:                 RoleAssistant,
		Category:             CategoryChat,
		MessageType:          TypeAssistantMessage,
		Say:                  say,
		Aside:                aside,
		ActionJSON:           env.ActionJSON,
		RawData:              env.RawData,
		ParseError:           env.ParseError,
		Content:              content,
		CreatedAtMS:          nowMS(),
		CompletionStatus:     normalizedStatus,
		Interrupt:            normalizedInterrupt,
		InterruptAtMS:        interruptAtMS,
		PartialText:          draft,
		PayloadSchemaVersion: PayloadSchemaVersion1,
	}
	if raw, err := json.Marshal(payload); err == nil {
		entry.PayloadJSON = string(raw)
	}
	msgID := s.appendHistoryMessage(entry)
	if msgID != "" {
		if actionID := actionIDFromJSON(env.ActionJSON); actionID != "" {
			s.bindActionCallRef(actionID, msgID)
		}
	}
	s.clearTurnInterrupt(turnID)
}

func (s *Session) appendHistoryMessage(msg ChatMessage) string {
	payload := map[string]any{}
	if strings.TrimSpace(msg.PayloadJSON) != "" {
		_ = json.Unmarshal([]byte(msg.PayloadJSON), &payload)
	}
	entry, err := BuildMessage(MessageWrite{
		MessageID:            msg.MessageID,
		TurnID:               msg.TurnID,
		Role:                 msg.Role,
		Say:                  msg.Say,
		Aside:                msg.Aside,
		ActionJSON:           msg.ActionJSON,
		RefMessageID:         msg.RefMessageID,
		RefActionSlot:        msg.RefActionSlot,
		RawData:              msg.RawData,
		ParseError:           msg.ParseError,
		Category:             msg.Category,
		MessageType:          msg.MessageType,
		Content:              msg.Content,
		PayloadSchemaVersion: msg.PayloadSchemaVersion,
		Payload:              payload,
		PayloadJSON:          msg.PayloadJSON,
		CreatedAtMS:          msg.CreatedAtMS,
		CompletionStatus:     msg.CompletionStatus,
		Interrupt:            msg.Interrupt,
		InterruptAtMS:        msg.InterruptAtMS,
		PartialText:          msg.PartialText,
	})
	if err != nil {
		Errorf("[Turn:%d] build message failed: %v", msg.TurnID, err)
		return ""
	}
	if s.chatStore != nil {
		persisted, err := s.chatStore.AppendMessage(entry)
		if err != nil {
			Errorf("[Turn:%d] persist message failed: %v", msg.TurnID, err)
		} else {
			entry = persisted
		}
	}
	s.historyMu.Lock()
	s.chatHistory = append(s.chatHistory, entry)
	s.applyHistoryWindowLocked()
	s.historyMu.Unlock()
	return entry.MessageID
}

func (s *Session) bootstrapHistoryFromStore() {
	if s.chatStore == nil {
		return
	}
	history, err := s.chatStore.LoadSessionWindow(s.sessionAnchorLimit(), s.sessionMessageCap())
	if err != nil {
		Warnf("load history failed: %v", err)
		return
	}
	if len(history) == 0 {
		return
	}
	s.historyMu.Lock()
	s.chatHistory = append([]ChatMessage(nil), history...)
	s.applyHistoryWindowLocked()
	s.historyMu.Unlock()
}
