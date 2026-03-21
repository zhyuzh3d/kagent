package app

import "strings"

func (s *Session) setState(state string, detail string) {
	s.setTurnState(s.turnID.Load(), state, detail)
}

func (s *Session) setTurnState(turnID uint64, state string, detail string) {
	s.stateMu.Lock()
	s.state = state
	s.stateMu.Unlock()
	if err := s.sendEvent(NewStatusEvent(turnID, state, detail)); err != nil {
		Errorf("send status failed: %v", err)
	}
}

func (s *Session) getState() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.state
}

func (s *Session) handleStateChange(ctrl ControlMessage) {
	surfaceID := strings.TrimSpace(ctrl.SurfaceID)
	if surfaceID == "" {
		return
	}
	state := SurfaceState{
		SurfaceID:      surfaceID,
		SurfaceType:    firstNonEmpty(strings.TrimSpace(ctrl.SurfaceType), "app"),
		SurfaceVersion: firstNonEmpty(strings.TrimSpace(ctrl.SurfaceVersion), "1"),
		EventType:      strings.TrimSpace(ctrl.EventType),
		BusinessState:  cloneAnyMap(ctrl.BusinessState),
		VisibleText:    strings.TrimSpace(ctrl.VisibleText),
		Status:         strings.TrimSpace(ctrl.Status),
		StateVersion:   ctrl.StateVersion,
		UpdatedAtMS:    ctrl.UpdatedAtMS,
	}
	if state.UpdatedAtMS <= 0 {
		state.UpdatedAtMS = nowMS()
	}
	statePayload := map[string]any{
		"surface_id":      state.SurfaceID,
		"surface_type":    state.SurfaceType,
		"surface_version": state.SurfaceVersion,
		"state":           cloneAnyMap(state.BusinessState),
		"business_state":  cloneAnyMap(state.BusinessState),
		"visible_text":    state.VisibleText,
		"status":          state.Status,
		"state_version":   state.StateVersion,
		"updated_at_ms":   state.UpdatedAtMS,
		"event_type":      firstNonEmpty(state.EventType, "state_change"),
	}
	eventType := firstNonEmpty(state.EventType, "state_change")
	actionPayload := map[string]any{
		"type":                  TypeActionState,
		"surface_id":            state.SurfaceID,
		"surface_type":          state.SurfaceType,
		"surface_version":       state.SurfaceVersion,
		"surface_instance_name": firstNonEmpty(state.SurfaceID, state.VisibleText),
		"event_type":            eventType,
		"delta_or_state":        cloneAnyMap(state.BusinessState),
		"state_version":         state.StateVersion,
		"status":                state.Status,
		"visible_text":          state.VisibleText,
		"updated_at_ms":         state.UpdatedAtMS,
	}
	entry := ChatMessage{
		TurnID:      ctrl.TurnID,
		Role:        RoleObserver,
		Category:    CategorySurface,
		MessageType: TypeActionState,
		Say:         "",
		ActionJSON:  mustJSON(actionPayload),
		PayloadJSON: mustJSON(statePayload),
		CreatedAtMS: state.UpdatedAtMS,
	}
	if eventType == "surface_open" {
		entry.MessageType = TypeSurfaceOpen
	}
	s.appendHistoryMessage(entry)
	s.enqueueFollowupMessage(ChatMessage{Role: RoleObserver, ActionJSON: entry.ActionJSON}, true)
	_ = s.sendEvent(EventMessage{
		Type:           "state_change",
		TsMS:           nowMS(),
		TurnID:         ctrl.TurnID,
		SurfaceID:      state.SurfaceID,
		SurfaceType:    state.SurfaceType,
		SurfaceVersion: state.SurfaceVersion,
		StateVersion:   state.StateVersion,
		BusinessState:  cloneAnyMap(state.BusinessState),
		Detail:         firstNonEmpty(state.EventType, "state_change"),
	})
}

func (s *Session) handleConfigChange(ctrl ControlMessage) {
	if s.runtimeConfig != nil && len(ctrl.ConfigSnapshot) > 0 {
		if err := s.runtimeConfig.ApplySnapshot(ctrl.ConfigSnapshot); err != nil {
			Warnf("apply runtime config snapshot failed: %v", err)
		}
	}
	payload := map[string]any{
		"source":        firstNonEmpty(strings.TrimSpace(ctrl.ConfigSource), "config_drawer"),
		"changed_paths": append([]string(nil), ctrl.ConfigChangedPaths...),
		"config":        cloneAnyMap(ctrl.ConfigSnapshot),
	}
	s.appendHistoryMessage(ChatMessage{
		TurnID:      ctrl.TurnID,
		Role:        RoleSystem,
		Category:    CategoryConfig,
		MessageType: TypeConfigChange,
		PayloadJSON: mustJSON(payload),
		CreatedAtMS: nowMS(),
	})
}

func (s *Session) recordTurnInterrupt(turnID uint64, reason string) {
	if turnID == 0 {
		return
	}
	reason = firstNonEmpty(normalizeInterrupt(reason), InterruptOther)
	s.interruptMu.Lock()
	if s.turnInterrupt == nil {
		s.turnInterrupt = map[uint64]string{}
	}
	s.turnInterrupt[turnID] = reason
	s.interruptMu.Unlock()
}

func (s *Session) consumeTurnInterrupt(turnID uint64) string {
	if turnID == 0 {
		return InterruptOther
	}
	s.interruptMu.Lock()
	defer s.interruptMu.Unlock()
	reason := firstNonEmpty(s.turnInterrupt[turnID], InterruptOther)
	delete(s.turnInterrupt, turnID)
	return reason
}

func (s *Session) clearTurnInterrupt(turnID uint64) {
	if turnID == 0 {
		return
	}
	s.interruptMu.Lock()
	delete(s.turnInterrupt, turnID)
	s.interruptMu.Unlock()
}
