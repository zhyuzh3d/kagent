package app

import (
	"context"
	"fmt"
	"strings"
)

func (s *HubDatabaseStore) init(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("store is not initialized")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			user_id TEXT PRIMARY KEY,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			project_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL,
			last_active_at_ms INTEGER NOT NULL,
			created_at_local_weekday TEXT NOT NULL DEFAULT '',
			created_at_local_lunar TEXT NOT NULL DEFAULT '',
			order_index INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS threads (
			thread_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL,
			last_active_at_ms INTEGER NOT NULL,
			created_at_local_weekday TEXT NOT NULL DEFAULT '',
			created_at_local_lunar TEXT NOT NULL DEFAULT '',
			order_index INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_uid TEXT NOT NULL UNIQUE,
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			turn_id INTEGER NOT NULL,
			seq INTEGER NOT NULL,
			created_at_ms INTEGER NOT NULL,
			created_at_iso TEXT NOT NULL,
			created_at_local_ymdhms TEXT NOT NULL,
			created_at_local_weekday TEXT NOT NULL,
			created_at_local_lunar TEXT NOT NULL,
			role TEXT NOT NULL,
			say TEXT NOT NULL DEFAULT '',
			aside TEXT NOT NULL DEFAULT '',
			action_json TEXT NOT NULL DEFAULT '',
			ref_message_id TEXT NOT NULL DEFAULT '',
			ref_action_slot INTEGER NOT NULL DEFAULT 0,
			raw_data TEXT NOT NULL DEFAULT '',
			parse_error TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL,
			type TEXT NOT NULL,
			content TEXT NOT NULL,
			payload_schema_version INTEGER NOT NULL,
			payload_json TEXT NOT NULL,
			completion_status TEXT NOT NULL DEFAULT '',
			interrupt TEXT NOT NULL DEFAULT '',
			interrupt_at_ms INTEGER NOT NULL DEFAULT 0,
			partial_text TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_scope ON messages(user_id, project_id, thread_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_projects_user ON projects(user_id, order_index, created_at_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_threads_project ON threads(project_id, user_id, order_index, created_at_ms)`,
	}
	for _, stmt := range stmts {
		if err := s.execute(ctx, stmt); err != nil {
			return err
		}
	}
	if !s.options.EnsureDefaults {
		return nil
	}
	return s.initDefaultIDs(ctx)
}

func (s *HubDatabaseStore) ensureUserLocked(ctx context.Context, userID string) error {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.userID))
	if cleanUserID == "" {
		return fmt.Errorf("user_id is required")
	}
	return s.execute(ctx, `INSERT OR IGNORE INTO users(user_id, created_at_ms) VALUES(?, ?)`, cleanUserID, nowMS())
}

func (s *HubDatabaseStore) initDefaultIDs(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(s.userID) == "" {
		s.userID = "default"
	}
	if err := s.ensureUserLocked(ctx, s.userID); err != nil {
		return err
	}

	if strings.TrimSpace(s.projectID) == "" {
		rows, err := s.query(ctx, `SELECT project_id FROM projects WHERE user_id=? ORDER BY order_index ASC, created_at_ms ASC LIMIT 1`, s.userID)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			s.projectID = asStringValue(rows[0], "project_id")
		}
		if strings.TrimSpace(s.projectID) == "" {
			s.projectID = "prj-" + newRequestID()
		}
	}
	if err := s.ensureProjectLocked(ctx, s.projectID, "Default Project", 0); err != nil {
		return err
	}

	if strings.TrimSpace(s.threadID) == "" {
		rows, err := s.query(ctx, `SELECT thread_id FROM threads WHERE user_id=? AND project_id=? ORDER BY order_index ASC, created_at_ms ASC LIMIT 1`, s.userID, s.projectID)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			s.threadID = asStringValue(rows[0], "thread_id")
		}
		if strings.TrimSpace(s.threadID) == "" {
			s.threadID = "thd-" + newRequestID()
		}
	}
	if err := s.ensureThreadLocked(ctx, s.threadID, s.projectID, "Default Thread", 0); err != nil {
		return err
	}
	return nil
}

func (s *HubDatabaseStore) ensureProjectLocked(ctx context.Context, projectID string, fallbackTitle string, orderIndex int) error {
	cleanProjectID := strings.TrimSpace(projectID)
	if cleanProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	rows, err := s.query(ctx, `SELECT project_id FROM projects WHERE project_id=? LIMIT 1`, cleanProjectID)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	now := nowMS()
	timeFields := buildSemanticTimeFields(now)
	return s.execute(ctx, `
		INSERT INTO projects (
			project_id, user_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar, order_index
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, cleanProjectID, s.userID, firstNonEmpty(strings.TrimSpace(fallbackTitle), "Default Project"), now, now, timeFields.LocalWeekday, timeFields.LocalLunar, orderIndex)
}

func (s *HubDatabaseStore) ensureThreadLocked(ctx context.Context, threadID string, projectID string, fallbackTitle string, orderIndex int) error {
	cleanThreadID := strings.TrimSpace(threadID)
	if cleanThreadID == "" {
		return fmt.Errorf("thread_id is required")
	}
	rows, err := s.query(ctx, `SELECT thread_id FROM threads WHERE thread_id=? LIMIT 1`, cleanThreadID)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	now := nowMS()
	timeFields := buildSemanticTimeFields(now)
	return s.execute(ctx, `
		INSERT INTO threads (
			thread_id, user_id, project_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar, order_index
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, cleanThreadID, s.userID, strings.TrimSpace(projectID), firstNonEmpty(strings.TrimSpace(fallbackTitle), "Default Thread"), now, now, timeFields.LocalWeekday, timeFields.LocalLunar, orderIndex)
}
