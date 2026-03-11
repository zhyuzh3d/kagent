package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db     *sql.DB
	userID string
	chatID string
}

func NewSQLiteStore(path string, userID string, chatID string) (*SQLiteStore, error) {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &SQLiteStore{
		db:     db,
		userID: firstNonEmpty(userID, "default"),
		chatID: firstNonEmpty(chatID, "chat-default"),
	}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) init() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store not initialized")
	}
	stmts := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		`CREATE TABLE IF NOT EXISTS users (
			user_id TEXT PRIMARY KEY,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chats (
			chat_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			message_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			turn_id INTEGER NOT NULL,
			role_internal TEXT NOT NULL,
			role_provider TEXT NOT NULL,
			message_type TEXT NOT NULL,
			content_text TEXT NOT NULL,
			visibility TEXT NOT NULL,
			meta_json TEXT NOT NULL DEFAULT '{}',
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_created
			ON messages(user_id, chat_id, created_at_ms)`,
		`CREATE TABLE IF NOT EXISTS action_calls (
			action_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			turn_id INTEGER NOT NULL,
			surface_id TEXT NOT NULL,
			action_name TEXT NOT NULL,
			followup TEXT NOT NULL,
			args_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL,
			manual_confirm TEXT NOT NULL DEFAULT '',
			block_reason TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_action_calls_chat_created
			ON action_calls(user_id, chat_id, created_at_ms)`,
		`CREATE TABLE IF NOT EXISTS action_reports (
			report_id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			action_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			turn_id INTEGER NOT NULL,
			origin TEXT NOT NULL,
			surface_id TEXT NOT NULL,
			result_summary TEXT NOT NULL,
			effect_summary TEXT NOT NULL,
			business_state_json TEXT NOT NULL DEFAULT '{}',
			followup TEXT NOT NULL,
			status TEXT NOT NULL,
			manual_confirm TEXT NOT NULL DEFAULT '',
			block_reason TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_action_reports_chat_created
			ON action_reports(user_id, chat_id, created_at_ms)`,
		`CREATE TABLE IF NOT EXISTS surface_states (
			user_id TEXT NOT NULL,
			chat_id TEXT NOT NULL,
			surface_id TEXT NOT NULL,
			state_version INTEGER NOT NULL,
			business_state_json TEXT NOT NULL DEFAULT '{}',
			visible_text TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL,
			PRIMARY KEY(user_id, chat_id, surface_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("sqlite init failed: %w", err)
		}
	}
	now := time.Now().UnixMilli()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO users(user_id, created_at_ms) VALUES(?, ?)`, s.userID, now); err != nil {
		return fmt.Errorf("insert default user failed: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO chats(chat_id, user_id, created_at_ms) VALUES(?, ?, ?)`, s.chatID, s.userID, now); err != nil {
		return fmt.Errorf("insert default chat failed: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpsertSurfaceState(state SurfaceState) error {
	if s == nil || s.db == nil {
		return nil
	}
	surfaceID := strings.TrimSpace(state.SurfaceID)
	if surfaceID == "" {
		return fmt.Errorf("surface_id is empty")
	}
	if state.UpdatedAtMS <= 0 {
		state.UpdatedAtMS = time.Now().UnixMilli()
	}
	bsJSON, err := json.Marshal(nonNilMap(state.BusinessState))
	if err != nil {
		return fmt.Errorf("marshal business_state: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO surface_states(
			user_id, chat_id, surface_id, state_version, business_state_json, visible_text, status, updated_at_ms
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, chat_id, surface_id) DO UPDATE SET
			state_version = excluded.state_version,
			business_state_json = excluded.business_state_json,
			visible_text = excluded.visible_text,
			status = excluded.status,
			updated_at_ms = excluded.updated_at_ms
	`, s.userID, s.chatID, surfaceID, state.StateVersion, string(bsJSON), strings.TrimSpace(state.VisibleText), strings.TrimSpace(state.Status), state.UpdatedAtMS)
	if err != nil {
		return fmt.Errorf("upsert surface state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LoadSurfaceState(surfaceID string) (SurfaceState, bool, error) {
	if s == nil || s.db == nil {
		return SurfaceState{}, false, nil
	}
	var (
		state     SurfaceState
		stateJSON string
	)
	err := s.db.QueryRow(`
		SELECT surface_id, state_version, business_state_json, visible_text, status, updated_at_ms
		FROM surface_states
		WHERE user_id=? AND chat_id=? AND surface_id=?
	`, s.userID, s.chatID, strings.TrimSpace(surfaceID)).Scan(&state.SurfaceID, &state.StateVersion, &stateJSON, &state.VisibleText, &state.Status, &state.UpdatedAtMS)
	if err == sql.ErrNoRows {
		return SurfaceState{}, false, nil
	}
	if err != nil {
		return SurfaceState{}, false, fmt.Errorf("load surface state: %w", err)
	}
	_ = json.Unmarshal([]byte(stateJSON), &state.BusinessState)
	return state, true, nil
}

func (s *SQLiteStore) AppendMessage(msg ChatMessage, turnID uint64, roleInternal string, messageType string, visibility string, meta map[string]any) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return "", nil
	}
	messageID := firstNonEmpty(strings.TrimSpace(msg.MessageID), "msg-"+newRequestID())
	metaJSON, err := json.Marshal(nonNilMap(meta))
	if err != nil {
		return "", fmt.Errorf("marshal message meta: %w", err)
	}
	createdAt := msg.CreatedAtMS
	if createdAt <= 0 {
		createdAt = time.Now().UnixMilli()
	}
	role := firstNonEmpty(roleInternal, msg.Role, "assistant")
	if _, err := s.db.Exec(`
		INSERT OR REPLACE INTO messages(
			message_id, user_id, chat_id, turn_id, role_internal, role_provider,
			message_type, content_text, visibility, meta_json, created_at_ms
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, messageID, s.userID, s.chatID, turnID, role, mapProviderRole(role), firstNonEmpty(messageType, "chat"), content, firstNonEmpty(visibility, "visible"), string(metaJSON), createdAt); err != nil {
		return "", fmt.Errorf("append message: %w", err)
	}
	return messageID, nil
}

func (s *SQLiteStore) AppendActionCall(call ActionCall, status string, manualConfirm string, blockReason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	actionID := firstNonEmpty(call.ActionID, "act-"+newRequestID())
	argsJSON, err := json.Marshal(nonNilMap(call.Args))
	if err != nil {
		return fmt.Errorf("marshal action args: %w", err)
	}
	createdAt := call.RequestedAt
	if createdAt <= 0 {
		createdAt = time.Now().UnixMilli()
	}
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO action_calls(
			action_id, user_id, chat_id, turn_id, surface_id, action_name,
			followup, args_json, status, manual_confirm, block_reason, created_at_ms
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, actionID, s.userID, s.chatID, call.TurnID, strings.TrimSpace(call.SurfaceID), strings.TrimSpace(call.ActionName), normalizeFollowup(call.Followup), string(argsJSON), firstNonEmpty(status, "unknown"), strings.TrimSpace(manualConfirm), strings.TrimSpace(blockReason), createdAt)
	if err != nil {
		return fmt.Errorf("append action call: %w", err)
	}
	return nil
}

func (s *SQLiteStore) AppendActionReport(report ActionReport, messageID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	reportID := firstNonEmpty(strings.TrimSpace(report.ReportID), "rep-"+newRequestID())
	msgID := firstNonEmpty(strings.TrimSpace(messageID), "msg-"+newRequestID())
	createdAt := report.CreatedAtMS
	if createdAt <= 0 {
		createdAt = time.Now().UnixMilli()
	}
	stateJSON, err := json.Marshal(nonNilMap(report.BusinessState))
	if err != nil {
		return fmt.Errorf("marshal report business_state: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO action_reports(
			report_id, message_id, action_id, user_id, chat_id, turn_id,
			origin, surface_id, result_summary, effect_summary, business_state_json,
			followup, status, manual_confirm, block_reason, created_at_ms
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, reportID, msgID, strings.TrimSpace(report.ActionID), s.userID, s.chatID, report.TurnID, firstNonEmpty(report.Origin, "action_callback"), strings.TrimSpace(report.SurfaceID), strings.TrimSpace(report.ResultSummary), strings.TrimSpace(report.EffectSummary), string(stateJSON), normalizeFollowup(report.Followup), firstNonEmpty(report.Status, "unknown"), strings.TrimSpace(report.ManualConfirm), strings.TrimSpace(report.BlockReason), createdAt)
	if err != nil {
		return fmt.Errorf("append action report: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LoadRecentContext(limit int) ([]ChatMessage, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT message_id, role_internal, content_text, message_type, visibility, created_at_ms
		FROM messages
		WHERE user_id=? AND chat_id=?
		ORDER BY created_at_ms DESC
		LIMIT ?
	`, s.userID, s.chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("load context: %w", err)
	}
	defer rows.Close()
	out := make([]ChatMessage, 0, limit)
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.MessageID, &m.Role, &m.Content, &m.MessageType, &m.Visibility, &m.CreatedAtMS); err != nil {
			return nil, fmt.Errorf("scan context: %w", err)
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *SQLiteStore) LoadContextBefore(cursor int64, limit int) ([]ChatMessage, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var rows *sql.Rows
	var err error
	if cursor <= 0 {
		rows, err = s.db.Query(`
			SELECT message_id, role_internal, content_text, message_type, visibility, created_at_ms
			FROM messages
			WHERE user_id=? AND chat_id=?
			ORDER BY created_at_ms DESC
			LIMIT ?
		`, s.userID, s.chatID, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT message_id, role_internal, content_text, message_type, visibility, created_at_ms
			FROM messages
			WHERE user_id=? AND chat_id=? AND created_at_ms < ?
			ORDER BY created_at_ms DESC
			LIMIT ?
		`, s.userID, s.chatID, cursor, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("load context before: %w", err)
	}
	defer rows.Close()
	out := make([]ChatMessage, 0, limit)
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.MessageID, &m.Role, &m.Content, &m.MessageType, &m.Visibility, &m.CreatedAtMS); err != nil {
			return nil, fmt.Errorf("scan context before: %w", err)
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func mapProviderRole(roleInternal string) string {
	switch strings.ToLower(strings.TrimSpace(roleInternal)) {
	case "observer":
		return "assistant"
	case "system_internal":
		return "system"
	case "user", "assistant", "system":
		return strings.ToLower(strings.TrimSpace(roleInternal))
	default:
		return "assistant"
	}
}

func nonNilMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	return in
}

func normalizeFollowup(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "report") {
		return "report"
	}
	return "none"
}
