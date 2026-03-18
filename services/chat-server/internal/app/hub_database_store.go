package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type Project struct {
	ProjectID             string `json:"project_id"`
	UserID                string `json:"user_id"`
	Title                 string `json:"title"`
	CreatedAtMS           int64  `json:"created_at_ms"`
	LastActiveAtMS        int64  `json:"last_active_at_ms"`
	CreatedAtLocalWeekday string `json:"created_at_local_weekday"`
	CreatedAtLocalLunar   string `json:"created_at_local_lunar"`
	OrderIndex            int    `json:"order_index"`
}

type Thread struct {
	ThreadID              string `json:"thread_id"`
	UserID                string `json:"user_id"`
	ProjectID             string `json:"project_id"`
	Title                 string `json:"title"`
	CreatedAtMS           int64  `json:"created_at_ms"`
	LastActiveAtMS        int64  `json:"last_active_at_ms"`
	CreatedAtLocalWeekday string `json:"created_at_local_weekday"`
	CreatedAtLocalLunar   string `json:"created_at_local_lunar"`
	OrderIndex            int    `json:"order_index"`
}

type ChatStore interface {
	Close() error
	RuntimeUserID() string
	RuntimeProjectID() string
	RuntimeThreadID() string
	AppendMessage(msg ChatMessage) (ChatMessage, error)
	LoadSessionWindow(anchorLimit int, totalLimit int) ([]ChatMessage, error)
	LoadContextBeforeWithMode(beforeID int64, limit int, includeAllRoles bool) ([]ChatMessage, bool, error)
	ListProjectsForUser(userID string) ([]Project, error)
	CreateProject(userID string, title string) (string, error)
	UpdateProject(projectID string, title string, orderIndex int) error
	DeleteProject(projectID string) error
	ListThreadsForProject(userID string, projectID string) ([]Thread, error)
	CreateThread(userID string, projectID string, title string) (string, error)
	UpdateThread(threadID string, title string, orderIndex int, projectID string) error
	DeleteThread(threadID string) error
}

type HubDatabaseStore struct {
	client *HubToolClient

	mu        sync.Mutex
	userID    string
	projectID string
	threadID  string
}

func NewHubDatabaseStore(client *HubToolClient, userID string, projectID string, threadID string) (*HubDatabaseStore, error) {
	s := &HubDatabaseStore{
		client:    client,
		userID:    strings.TrimSpace(userID),
		projectID: strings.TrimSpace(projectID),
		threadID:  strings.TrimSpace(threadID),
	}
	if err := s.init(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *HubDatabaseStore) Close() error {
	return nil
}

func (s *HubDatabaseStore) RuntimeUserID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.userID)
}

func (s *HubDatabaseStore) RuntimeProjectID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.projectID)
}

func (s *HubDatabaseStore) RuntimeThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.threadID)
}

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
	return s.initDefaultIDs(ctx)
}

func (s *HubDatabaseStore) initDefaultIDs(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(s.userID) == "" {
		s.userID = "default"
	}
	now := nowMS()
	if err := s.execute(ctx, `INSERT OR IGNORE INTO users(user_id, created_at_ms) VALUES(?, ?)`, s.userID, now); err != nil {
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

func (s *HubDatabaseStore) AppendMessage(msg ChatMessage) (ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()
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

func (s *HubDatabaseStore) ListProjectsForUser(userID string) ([]Project, error) {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.RuntimeUserID()))
	rows, err := s.query(context.Background(), `
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
	if err := s.execute(context.Background(), `INSERT OR IGNORE INTO users(user_id, created_at_ms) VALUES(?, ?)`, cleanUserID, now); err != nil {
		return "", err
	}
	rows, err := s.query(context.Background(), `SELECT COALESCE(MAX(order_index), -1) + 1 AS next_order FROM projects WHERE user_id=?`, cleanUserID)
	if err != nil {
		return "", err
	}
	orderIndex := asIntValue(firstRow(rows), "next_order")
	projectID := "prj-" + newRequestID()
	timeFields := buildSemanticTimeFields(now)
	if err := s.execute(context.Background(), `
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
	return s.execute(context.Background(), `
		UPDATE projects SET title=?, order_index=?, last_active_at_ms=? WHERE project_id=?
	`, firstNonEmpty(strings.TrimSpace(title), "未命名项目"), orderIndex, nowMS(), cleanProjectID)
}

func (s *HubDatabaseStore) DeleteProject(projectID string) error {
	cleanProjectID := strings.TrimSpace(projectID)
	if cleanProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	ctx := context.Background()
	if err := s.execute(ctx, `DELETE FROM messages WHERE project_id=?`, cleanProjectID); err != nil {
		return err
	}
	if err := s.execute(ctx, `DELETE FROM threads WHERE project_id=?`, cleanProjectID); err != nil {
		return err
	}
	return s.execute(ctx, `DELETE FROM projects WHERE project_id=?`, cleanProjectID)
}

func (s *HubDatabaseStore) ListThreadsForProject(userID string, projectID string) ([]Thread, error) {
	cleanUserID := strings.TrimSpace(firstNonEmpty(userID, s.RuntimeUserID()))
	cleanProjectID := strings.TrimSpace(firstNonEmpty(projectID, s.RuntimeProjectID()))
	if cleanProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	rows, err := s.query(context.Background(), `
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
	now := nowMS()
	rows, err := s.query(context.Background(), `SELECT COALESCE(MAX(order_index), -1) + 1 AS next_order FROM threads WHERE user_id=? AND project_id=?`, cleanUserID, cleanProjectID)
	if err != nil {
		return "", err
	}
	orderIndex := asIntValue(firstRow(rows), "next_order")
	threadID := "thd-" + newRequestID()
	timeFields := buildSemanticTimeFields(now)
	if err := s.execute(context.Background(), `
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
	ctx := context.Background()
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
	ctx := context.Background()
	if err := s.execute(ctx, `DELETE FROM messages WHERE thread_id=?`, cleanThreadID); err != nil {
		return err
	}
	return s.execute(ctx, `DELETE FROM threads WHERE thread_id=?`, cleanThreadID)
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

func firstRow(rows []map[string]any) map[string]any {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

func asStringValue(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	raw, ok := row[key]
	if !ok || raw == nil {
		return ""
	}
	switch tv := raw.(type) {
	case string:
		return strings.TrimSpace(tv)
	default:
		return strings.TrimSpace(fmt.Sprint(tv))
	}
}

func asInt64Value(row map[string]any, key string) int64 {
	if row == nil {
		return 0
	}
	raw, ok := row[key]
	if !ok || raw == nil {
		return 0
	}
	switch tv := raw.(type) {
	case int:
		return int64(tv)
	case int32:
		return int64(tv)
	case int64:
		return tv
	case float32:
		return int64(tv)
	case float64:
		return int64(tv)
	case json.Number:
		i, _ := tv.Int64()
		return i
	case string:
		var out int64
		_, _ = fmt.Sscan(strings.TrimSpace(tv), &out)
		return out
	default:
		return 0
	}
}

func asIntValue(row map[string]any, key string) int {
	return int(asInt64Value(row, key))
}

func (s *HubDatabaseStore) query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	result, err := s.client.Call(ctx, "storage.database.query", map[string]any{
		"query": strings.TrimSpace(query),
		"args":  args,
	}, 60000)
	if err != nil {
		return nil, err
	}
	rawRows, ok := result["rows"]
	if !ok || rawRows == nil {
		return []map[string]any{}, nil
	}
	list, ok := rawRows.([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *HubDatabaseStore) execute(ctx context.Context, query string, args ...any) error {
	_, err := s.client.Call(ctx, "storage.database.execute", map[string]any{
		"query": strings.TrimSpace(query),
		"args":  args,
	}, 60000)
	return err
}
