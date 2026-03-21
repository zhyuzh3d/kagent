package app

import (
	"fmt"
	"strings"
)

func (s *HubDatabaseStore) ListProjectsForUser(userID string) ([]Project, error) {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.RuntimeUserID()))
	rows, err := s.query(s.baseCtx, `
		SELECT project_id, user_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar, order_index
		FROM projects
		WHERE user_id=?
		ORDER BY order_index ASC, created_at_ms ASC
	`, cleanUserID)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(rows))
	for _, row := range rows {
		out = append(out, Project{
			ProjectID:             asStringValue(row, "project_id"),
			UserID:                asStringValue(row, "user_id"),
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

func (s *HubDatabaseStore) CreateProject(userID string, title string) (string, error) {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.RuntimeUserID()))
	if cleanUserID == "" {
		return "", fmt.Errorf("user_id is required")
	}
	now := nowMS()
	if err := s.ensureUserLocked(s.baseCtx, cleanUserID); err != nil {
		return "", err
	}
	rows, err := s.query(s.baseCtx, `SELECT COALESCE(MAX(order_index), -1) + 1 AS next_order FROM projects WHERE user_id=?`, cleanUserID)
	if err != nil {
		return "", err
	}
	orderIndex := asIntValue(firstRow(rows), "next_order")
	projectID := "prj-" + newRequestID()
	timeFields := buildSemanticTimeFields(now)
	if err := s.execute(s.baseCtx, `
		INSERT INTO projects (
			project_id, user_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar, order_index
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, projectID, cleanUserID, firstNonEmpty(strings.TrimSpace(title), "新项目"), now, now, timeFields.LocalWeekday, timeFields.LocalLunar, orderIndex); err != nil {
		return "", err
	}
	return projectID, nil
}

func (s *HubDatabaseStore) UpdateProject(projectID string, title string, orderIndex int) error {
	cleanProjectID := strings.TrimSpace(projectID)
	if cleanProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	return s.execute(s.baseCtx, `
		UPDATE projects SET title=?, order_index=?, last_active_at_ms=? WHERE project_id=?
	`, firstNonEmpty(strings.TrimSpace(title), "未命名项目"), orderIndex, nowMS(), cleanProjectID)
}

func (s *HubDatabaseStore) DeleteProject(projectID string) error {
	cleanProjectID := strings.TrimSpace(projectID)
	if cleanProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	ctx := s.baseCtx
	if err := s.execute(ctx, `DELETE FROM messages WHERE project_id=?`, cleanProjectID); err != nil {
		return err
	}
	if err := s.execute(ctx, `DELETE FROM threads WHERE project_id=?`, cleanProjectID); err != nil {
		return err
	}
	return s.execute(ctx, `DELETE FROM projects WHERE project_id=?`, cleanProjectID)
}
