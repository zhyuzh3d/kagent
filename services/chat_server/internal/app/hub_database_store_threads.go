package app

import (
	"fmt"
	"strings"
)

func (s *HubDatabaseStore) ListThreadsForProject(userID string, projectID string) ([]Thread, error) {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.RuntimeUserID()))
	cleanProjectID := strings.TrimSpace(firstNonEmpty(projectID, s.RuntimeProjectID()))
	if cleanProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	rows, err := s.query(s.baseCtx, `
		SELECT thread_id, user_id, project_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar, order_index
		FROM threads
		WHERE user_id=? AND project_id=?
		ORDER BY order_index ASC, created_at_ms ASC
	`, cleanUserID, cleanProjectID)
	if err != nil {
		return nil, err
	}
	out := make([]Thread, 0, len(rows))
	for _, row := range rows {
		out = append(out, Thread{
			ThreadID:              asStringValue(row, "thread_id"),
			UserID:                asStringValue(row, "user_id"),
			ProjectID:             asStringValue(row, "project_id"),
			Title:                 asStringValue(row, "title"),
			CreatedAtMS:           asInt64Value(row, "created_at_ms"),
			LastActiveAtMS:        asInt64Value(row, "last_active_at_ms"),
			CreatedAtLocalWeekday: asStringValue(row, "created_at_local_weekday"),
			CreatedAtLocalLunar:   asStringValue(row, "created_at_local_lunar"),
			OrderIndex:            asIntValue(row, "order_index"),
		})
	}
	return out, nil
}

func (s *HubDatabaseStore) CreateThread(userID string, projectID string, title string) (string, error) {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.RuntimeUserID()))
	cleanProjectID := strings.TrimSpace(firstNonEmpty(projectID, s.RuntimeProjectID()))
	if cleanUserID == "" || cleanProjectID == "" {
		return "", fmt.Errorf("user_id and project_id are required")
	}
	if err := s.ensureUserLocked(s.baseCtx, cleanUserID); err != nil {
		return "", err
	}
	now := nowMS()
	rows, err := s.query(s.baseCtx, `SELECT COALESCE(MAX(order_index), -1) + 1 AS next_order FROM threads WHERE user_id=? AND project_id=?`, cleanUserID, cleanProjectID)
	if err != nil {
		return "", err
	}
	orderIndex := asIntValue(firstRow(rows), "next_order")
	threadID := "thd-" + newRequestID()
	timeFields := buildSemanticTimeFields(now)
	if err := s.execute(s.baseCtx, `
		INSERT INTO threads (
			thread_id, user_id, project_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar, order_index
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, threadID, cleanUserID, cleanProjectID, firstNonEmpty(strings.TrimSpace(title), "新会话"), now, now, timeFields.LocalWeekday, timeFields.LocalLunar, orderIndex); err != nil {
		return "", err
	}
	return threadID, nil
}

func (s *HubDatabaseStore) UpdateThread(threadID string, title string, orderIndex int, projectID string) error {
	cleanThreadID := strings.TrimSpace(threadID)
	if cleanThreadID == "" {
		return fmt.Errorf("thread_id is required")
	}
	cleanProjectID := strings.TrimSpace(projectID)
	ctx := s.baseCtx
	if cleanProjectID != "" {
		return s.execute(ctx, `
			UPDATE threads SET title=?, order_index=?, project_id=?, last_active_at_ms=? WHERE thread_id=?
		`, firstNonEmpty(strings.TrimSpace(title), "未命名会话"), orderIndex, cleanProjectID, nowMS(), cleanThreadID)
	}
	return s.execute(ctx, `
		UPDATE threads SET title=?, order_index=?, last_active_at_ms=? WHERE thread_id=?
	`, firstNonEmpty(strings.TrimSpace(title), "未命名会话"), orderIndex, nowMS(), cleanThreadID)
}

func (s *HubDatabaseStore) DeleteThread(threadID string) error {
	cleanThreadID := strings.TrimSpace(threadID)
	if cleanThreadID == "" {
		return fmt.Errorf("thread_id is required")
	}
	ctx := s.baseCtx
	if err := s.execute(ctx, `DELETE FROM messages WHERE thread_id=?`, cleanThreadID); err != nil {
		return err
	}
	return s.execute(ctx, `DELETE FROM threads WHERE thread_id=?`, cleanThreadID)
}
