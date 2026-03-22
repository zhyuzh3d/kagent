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

func cloneAnyMapSlice(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		out = append(out, cloneAnyMap(item))
	}
	return out
}

func defaultObserverSource(category string, messageType string, payload map[string]any) (string, string, string) {
	surfaceID := strings.TrimSpace(asTrimmedString(payload["surface_id"]))
	if surfaceID == "" {
		runtime := anyMap(payload["runtime_context"])
		surfaceID = strings.TrimSpace(asTrimmedString(runtime["surface_id"]))
	}
	if surfaceID == "" {
		surfaceID = strings.TrimSpace(asTrimmedString(payload["active_surface_id"]))
	}
	surfaceLabel := firstNonEmpty(
		strings.TrimSpace(asTrimmedString(payload["surface_title"])),
		strings.TrimSpace(asTrimmedString(payload["surface_name"])),
	)
	if surfaceLabel == "" {
		runtime := anyMap(payload["runtime_context"])
		surfaceLabel = firstNonEmpty(
			strings.TrimSpace(asTrimmedString(runtime["title"])),
			strings.TrimSpace(asTrimmedString(runtime["catalog_name"])),
			strings.TrimSpace(asTrimmedString(runtime["surface_id"])),
		)
	}
	if surfaceLabel == "" {
		surfaceLabel = surfaceID
	}

	if category == CategorySurfaceContext {
		switch messageType {
		case TypeSurfaceRegistrySync, TypeSurfaceActiveChange:
			return ObserverSourceKindPage, ObserverSourceIDPageChat, ObserverSourceLabelPage
		}
	}
	if (category == CategorySurface || category == CategorySurfaceContext) && surfaceID != "" {
		return ObserverSourceKindSurface, surfaceID, firstNonEmpty(surfaceLabel, surfaceID)
	}
	if category == CategorySurfaceContext {
		return ObserverSourceKindPage, ObserverSourceIDPageChat, ObserverSourceLabelPage
	}
	return ObserverSourceKindSystem, "chat_server", "Chat Server"
}

func attachObserverSource(category string, messageType string, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	kind, sourceID, label := defaultObserverSource(category, messageType, payload)
	if strings.TrimSpace(asTrimmedString(payload["source_kind"])) == "" {
		payload["source_kind"] = kind
	}
	if strings.TrimSpace(asTrimmedString(payload["source_id"])) == "" {
		payload["source_id"] = sourceID
	}
	if strings.TrimSpace(asTrimmedString(payload["source_label"])) == "" {
		payload["source_label"] = label
	}
	return payload
}

func (s *Session) isDuplicateObserverPayload(category string, messageType string, payloadJSON string) bool {
	if strings.TrimSpace(payloadJSON) == "" {
		return false
	}
	history := s.getHistory()
	limit := 80
	for i := len(history) - 1; i >= 0 && limit > 0; i-- {
		limit--
		msg := history[i]
		if msg.Role != RoleObserver {
			continue
		}
		if msg.Category != category || msg.MessageType != messageType {
			continue
		}
		if strings.TrimSpace(msg.PayloadJSON) == payloadJSON {
			return true
		}
	}
	return false
}

func (s *Session) appendObserverMessage(turnID uint64, category string, messageType string, payload map[string]any, actionPayload map[string]any, createdAtMS int64, aggregate bool) {
	payload = attachObserverSource(category, messageType, payload)
	payloadJSON := mustJSON(payload)
	if s.isDuplicateObserverPayload(category, messageType, payloadJSON) {
		return
	}
	rawData := ""
	if category == CategorySurface || category == CategorySurfaceContext {
		rawData = payloadJSON
	}
	entry := ChatMessage{
		TurnID:      turnID,
		Role:        RoleObserver,
		Category:    category,
		MessageType: messageType,
		ActionJSON:  strings.TrimSpace(mustJSON(actionPayload)),
		RawData:     rawData,
		PayloadJSON: payloadJSON,
		CreatedAtMS: createdAtMS,
	}
	if actionPayload == nil {
		entry.ActionJSON = ""
	}
	persisted, ok := s.appendHistoryMessageEntry(entry)
	if !ok {
		return
	}
	s.enqueueFollowupMessage(ChatMessage{
		Role:        RoleObserver,
		Category:    category,
		MessageType: messageType,
		PayloadJSON: payloadJSON,
		ActionJSON:  entry.ActionJSON,
	}, aggregate)
	if shouldEmitLiveObserverMessage(category, messageType, payload) {
		liveMessage := persisted
		_ = s.sendEvent(EventMessage{
			Type:            "message_append",
			TsMS:            nowMS(),
			TurnID:          turnID,
			ObserverMessage: &liveMessage,
		})
	}
}

func shouldEmitLiveObserverMessage(category string, messageType string, payload map[string]any) bool {
	if category == CategorySurfaceContext {
		switch messageType {
		case TypeSurfaceRegistrySync, TypeSurfaceActiveChange:
			return true
		case TypeSurfaceRuntimeContext:
			if strings.TrimSpace(asTrimmedString(payload["reason"])) != "runtime_actions_change" {
				return false
			}
			runtime := anyMap(payload["runtime_context"])
			if raw, ok := runtime["open"].(bool); ok {
				return raw
			}
			return strings.TrimSpace(asTrimmedString(runtime["mode"])) != "closed"
		default:
			return false
		}
	}
	if category == CategorySurface {
		switch strings.TrimSpace(asTrimmedString(payload["event_type"])) {
		case "surface_open", "surface_close", "surface_closed":
			return true
		default:
			return false
		}
	}
	return false
}

func (s *Session) handleSurfaceContext(ctrl ControlMessage) {
	messageType := strings.ToLower(strings.TrimSpace(ctrl.Type))
	if messageType == "" {
		return
	}
	updatedAtMS := ctrl.UpdatedAtMS
	if updatedAtMS <= 0 {
		updatedAtMS = nowMS()
	}
	payload := map[string]any{
		"context_version":   ctrl.ContextVersion,
		"updated_at_ms":     updatedAtMS,
		"reason":            strings.TrimSpace(ctrl.Reason),
		"active_surface_id": strings.TrimSpace(ctrl.ActiveSurfaceID),
	}
	switch messageType {
	case TypeSurfaceRegistrySync:
		payload["registry"] = cloneAnyMapSlice(ctrl.Registry)
	case TypeSurfaceActiveChange:
		// active surface already captured in base payload
	case TypeSurfaceRuntimeContext:
		surfaceID := firstNonEmpty(strings.TrimSpace(ctrl.SurfaceID), asTrimmedString(ctrl.RuntimeContext["surface_id"]))
		if surfaceID == "" {
			return
		}
		payload["surface_id"] = surfaceID
		payload["surface_name"] = strings.TrimSpace(ctrl.SurfaceName)
		payload["surface_title"] = strings.TrimSpace(ctrl.SurfaceTitle)
		payload["runtime_context"] = cloneAnyMap(ctrl.RuntimeContext)
		if len(ctrl.SurfaceRegister) > 0 {
			payload["surface_register"] = cloneAnyMap(ctrl.SurfaceRegister)
		}
		if len(ctrl.SurfaceRegistration) > 0 {
			payload["surface_registration"] = cloneAnyMap(ctrl.SurfaceRegistration)
		}
		if len(ctrl.SurfaceActions) > 0 {
			payload["surface_actions"] = cloneAnyMapSlice(ctrl.SurfaceActions)
		}
	default:
		return
	}
	s.appendObserverMessage(ctrl.TurnID, CategorySurfaceContext, messageType, payload, nil, updatedAtMS, true)
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
		"surface_name":    strings.TrimSpace(ctrl.SurfaceName),
		"surface_title":   strings.TrimSpace(ctrl.SurfaceTitle),
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
	messageType := TypeActionState
	if eventType == "surface_open" {
		messageType = TypeSurfaceOpen
	}
	s.appendObserverMessage(ctrl.TurnID, CategorySurface, messageType, statePayload, actionPayload, state.UpdatedAtMS, true)
	_ = s.sendEvent(EventMessage{
		Type:           "state_change",
		TsMS:           nowMS(),
		TurnID:         ctrl.TurnID,
		SurfaceID:      state.SurfaceID,
		SurfaceType:    state.SurfaceType,
		SurfaceVersion: state.SurfaceVersion,
		SurfaceName:    strings.TrimSpace(ctrl.SurfaceName),
		SurfaceTitle:   strings.TrimSpace(ctrl.SurfaceTitle),
		Status:         state.Status,
		VisibleText:    state.VisibleText,
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
