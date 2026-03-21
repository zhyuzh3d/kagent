package app

import (
	"context"
	"strings"
)

func (s *HubDatabaseStore) AppendMessage(msg ChatMessage) (ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := s.baseCtx
	if err := s.ensureUserLocked(ctx, s.userID); err != nil {
		return ChatMessage{}, err
	}
	if err := s.ensureProjectLocked(ctx, s.projectID, "Default Project", 0); err != nil {
		return ChatMessage{}, err
	}
	if err := s.ensureThreadLocked(ctx, s.threadID, s.projectID, "Default Thread", 0); err != nil {
		return ChatMessage{}, err
	}

	entry := msg
	if strings.TrimSpace(entry.MessageID) == "" {
		entry.MessageID = "msg-" + newRequestID()
	}
	if entry.Seq <= 0 {
		rows, err := s.query(ctx, `
			SELECT COALESCE(MAX(seq), 0) + 1 AS next_seq
			FROM messages
			WHERE user_id=? AND project_id=? AND thread_id=?
		`, s.userID, s.projectID, s.threadID)
		if err != nil {
			return ChatMessage{}, err
		}
		entry.Seq = asInt64Value(firstRow(rows), "next_seq")
		if entry.Seq <= 0 {
			entry.Seq = 1
		}
	}
	if entry.CreatedAtMS <= 0 {
		entry.CreatedAtMS = nowMS()
	}
	if entry.PayloadSchemaVersion <= 0 {
		entry.PayloadSchemaVersion = PayloadSchemaVersion1
	}
	timeFields := buildSemanticTimeFields(entry.CreatedAtMS)
	entry.CreatedAtISO = timeFields.ISO
	entry.CreatedAtLocalYMDHMS = timeFields.LocalYMDHMS
	entry.CreatedAtLocalWeekday = timeFields.LocalWeekday
	entry.CreatedAtLocalLunar = timeFields.LocalLunar
	entry.ProjectID = s.projectID
	entry.ThreadID = s.threadID

	if err := s.execute(ctx, `
		INSERT INTO messages (
			message_uid, user_id, project_id, thread_id, turn_id, seq, created_at_ms, created_at_iso, created_at_local_ymdhms,
			created_at_local_weekday, created_at_local_lunar, role, say, aside, action_json, ref_message_id, ref_action_slot,
			raw_data, parse_error, category, type, content, payload_schema_version, payload_json, completion_status, interrupt,
			interrupt_at_ms, partial_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.MessageID, s.userID, s.projectID, s.threadID, int64(entry.TurnID), entry.Seq, entry.CreatedAtMS, entry.CreatedAtISO, entry.CreatedAtLocalYMDHMS,
		entry.CreatedAtLocalWeekday, entry.CreatedAtLocalLunar, entry.Role, entry.Say, entry.Aside, entry.ActionJSON, entry.RefMessageID, entry.RefActionSlot,
		entry.RawData, entry.ParseError, entry.Category, entry.MessageType, entry.Content, entry.PayloadSchemaVersion, entry.PayloadJSON, entry.CompletionStatus, entry.Interrupt,
		entry.InterruptAtMS, entry.PartialText); err != nil {
		return ChatMessage{}, err
	}
	if err := s.execute(ctx, `UPDATE threads SET last_active_at_ms=? WHERE thread_id=?`, entry.CreatedAtMS, s.threadID); err != nil {
		Warnf("update thread last_active failed: %v", err)
	}
	if err := s.execute(ctx, `UPDATE projects SET last_active_at_ms=? WHERE project_id=?`, entry.CreatedAtMS, s.projectID); err != nil {
		Warnf("update project last_active failed: %v", err)
	}
	rows, err := s.query(ctx, `SELECT id FROM messages WHERE message_uid=? LIMIT 1`, entry.MessageID)
	if err == nil && len(rows) > 0 {
		entry.StoreID = asInt64Value(rows[0], "id")
	}
	return entry, nil
}

func (s *HubDatabaseStore) LoadSessionWindow(anchorLimit int, totalLimit int) ([]ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := s.baseCtx
	if anchorLimit <= 0 {
		anchorLimit = 20
	}
	if totalLimit <= 0 {
		totalLimit = 80
	}
	anchorRows, err := s.query(ctx, `
		SELECT id
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=? AND role IN (?, ?)
		ORDER BY id DESC
		LIMIT ?
	`, s.userID, s.projectID, s.threadID, RoleUser, RoleAssistant, anchorLimit)
	if err != nil {
		return nil, err
	}
	if len(anchorRows) == 0 {
		return s.loadByQuery(ctx, `
			SELECT *
			FROM messages
			WHERE user_id=? AND project_id=? AND thread_id=?
			ORDER BY id DESC
			LIMIT ?
		`, []any{s.userID, s.projectID, s.threadID, totalLimit}, true)
	}
	anchorID := int64(0)
	for _, row := range anchorRows {
		id := asInt64Value(row, "id")
		if anchorID == 0 || id < anchorID {
			anchorID = id
		}
	}
	if anchorID <= 0 {
		return []ChatMessage{}, nil
	}
	return s.loadByQuery(ctx, `
		SELECT *
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=? AND id>=?
		ORDER BY id ASC
	`, []any{s.userID, s.projectID, s.threadID, anchorID}, false)
}

func (s *HubDatabaseStore) LoadContextBeforeWithMode(beforeID int64, limit int, includeAllRoles bool) ([]ChatMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := s.baseCtx
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	query := `
		SELECT *
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=?
	`
	args := []any{s.userID, s.projectID, s.threadID}
	if !includeAllRoles {
		query += " AND role IN (?, ?)"
		args = append(args, RoleUser, RoleAssistant)
	}
	if beforeID > 0 {
		query += " AND id < ?"
		args = append(args, beforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	list, err := s.loadByQuery(ctx, query, args, false)
	if err != nil {
		return nil, false, err
	}
	hasMore := false
	if len(list) > limit {
		hasMore = true
		list = list[:limit]
	}
	reverseMessages(list)
	return list, hasMore, nil
}

func (s *HubDatabaseStore) loadByQuery(ctx context.Context, query string, args []any, reverse bool) ([]ChatMessage, error) {
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := make([]ChatMessage, 0, len(rows))
	for _, row := range rows {
		entry := ChatMessage{
			StoreID:               asInt64Value(row, "id"),
			MessageID:             asStringValue(row, "message_uid"),
			ProjectID:             asStringValue(row, "project_id"),
			ThreadID:              asStringValue(row, "thread_id"),
			TurnID:                uint64(asInt64Value(row, "turn_id")),
			Seq:                   asInt64Value(row, "seq"),
			Role:                  asStringValue(row, "role"),
			Say:                   asStringValue(row, "say"),
			Aside:                 asStringValue(row, "aside"),
			ActionJSON:            asStringValue(row, "action_json"),
			RefMessageID:          asStringValue(row, "ref_message_id"),
			RefActionSlot:         asIntValue(row, "ref_action_slot"),
			RawData:               asStringValue(row, "raw_data"),
			ParseError:            asStringValue(row, "parse_error"),
			Category:              asStringValue(row, "category"),
			MessageType:           asStringValue(row, "type"),
			Content:               asStringValue(row, "content"),
			PayloadSchemaVersion:  asIntValue(row, "payload_schema_version"),
			PayloadJSON:           asStringValue(row, "payload_json"),
			CreatedAtMS:           asInt64Value(row, "created_at_ms"),
			CreatedAtISO:          asStringValue(row, "created_at_iso"),
			CreatedAtLocalYMDHMS:  asStringValue(row, "created_at_local_ymdhms"),
			CreatedAtLocalWeekday: asStringValue(row, "created_at_local_weekday"),
			CreatedAtLocalLunar:   asStringValue(row, "created_at_local_lunar"),
			CompletionStatus:      asStringValue(row, "completion_status"),
			Interrupt:             asStringValue(row, "interrupt"),
			InterruptAtMS:         asInt64Value(row, "interrupt_at_ms"),
			PartialText:           asStringValue(row, "partial_text"),
		}
		out = append(out, entry)
	}
	if reverse {
		reverseMessages(out)
	}
	return out, nil
}

func reverseMessages(items []ChatMessage) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
