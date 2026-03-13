package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db        *sql.DB
	userID    string
	projectID string
	threadID  string
}

func NewSQLiteStore(path string, userID string, projectID string, threadID string) (*SQLiteStore, error) {
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
		db:        db,
		userID:    strings.TrimSpace(userID),
		projectID: strings.TrimSpace(projectID),
		threadID:  strings.TrimSpace(threadID),
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

func (s *SQLiteStore) RuntimeUserID() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.userID)
}

func (s *SQLiteStore) RuntimeProjectID() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.projectID)
}

func (s *SQLiteStore) RuntimeThreadID() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.threadID)
}

func (s *SQLiteStore) init() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store not initialized")
	}
	baseStmts := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, stmt := range baseStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("sqlite init failed: %w", err)
		}
	}

	reset, err := s.needsSchemaReset()
	if err != nil {
		return err
	}
	if reset {
		if err := s.resetSchema(); err != nil {
			return err
		}
	}

	schemaStmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			user_id TEXT PRIMARY KEY,
			username TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL DEFAULT '',
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
			FOREIGN KEY(user_id) REFERENCES users(user_id)
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
			FOREIGN KEY(user_id) REFERENCES users(user_id),
			FOREIGN KEY(project_id) REFERENCES projects(project_id)
		)`,
		`CREATE TABLE IF NOT EXISTS surfaces (
			surface_id TEXT PRIMARY KEY,
			surface_type TEXT NOT NULL,
			pkg_path TEXT NOT NULL,
			manifest_json TEXT NOT NULL,
			manifest_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			scanned_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_surfaces (
			user_id TEXT NOT NULL,
			surface_id TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			PRIMARY KEY(user_id, surface_id),
			FOREIGN KEY(surface_id) REFERENCES surfaces(surface_id)
		)`,
		`CREATE TABLE IF NOT EXISTS thread_summaries (
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			summary_version TEXT NOT NULL DEFAULT '1',
			content TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL,
			PRIMARY KEY(user_id, thread_id)
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			memory_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			thread_id TEXT NOT NULL,
			memory_version TEXT NOT NULL DEFAULT '1',
			content TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			created_at_ms INTEGER NOT NULL,
			source_message_uid TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			file_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			uri_or_path TEXT NOT NULL,
			sha256 TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS file_refs (
			ref_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			file_id TEXT NOT NULL,
			ref_type TEXT NOT NULL,
			ref_key TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL,
			FOREIGN KEY(file_id) REFERENCES files(file_id)
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
		`CREATE INDEX IF NOT EXISTS idx_messages_thread_id ON messages(user_id, project_id, thread_id, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_anchor_id ON messages(user_id, project_id, thread_id, category, type, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_surfaces_status ON surfaces(status, surface_type, surface_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_surfaces_user_enabled ON user_surfaces(user_id, enabled, surface_id)`,
		`DROP TABLE IF EXISTS surface_states`,
	}
	for _, stmt := range schemaStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("sqlite schema init failed: %w", err)
		}
	}

	if err := s.migrateUsersTable(); err != nil {
		return err
	}

	return s.initDefaultIDs()
}

func (s *SQLiteStore) initDefaultIDs() error {
	if s.userID == "" {
		var uid string
		err := s.db.QueryRow(`SELECT user_id FROM users LIMIT 1`).Scan(&uid)
		if err != nil {
			if err == sql.ErrNoRows {
				s.userID = "usr-" + newRequestID()
			} else {
				return err
			}
		} else {
			s.userID = uid
		}
	}

	if s.projectID == "" {
		var pid string
		err := s.db.QueryRow(`SELECT project_id FROM projects WHERE user_id=? LIMIT 1`, s.userID).Scan(&pid)
		if err != nil {
			if err == sql.ErrNoRows {
				s.projectID = "prj-" + newRequestID()
			} else {
				return err
			}
		} else {
			s.projectID = pid
		}
	}

	if s.threadID == "" {
		var tid string
		err := s.db.QueryRow(`SELECT thread_id FROM threads WHERE user_id=? AND project_id=? LIMIT 1`, s.userID, s.projectID).Scan(&tid)
		if err != nil {
			if err == sql.ErrNoRows {
				s.threadID = "thd-" + newRequestID()
			} else {
				return err
			}
		} else {
			s.threadID = tid
		}
	}

	now := nowMS()
	semFields := buildSemanticTimeFields(now)

	if _, err := s.db.Exec(`INSERT OR IGNORE INTO users(user_id, created_at_ms) VALUES(?, ?)`, s.userID, now); err != nil {
		return fmt.Errorf("insert default user failed: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO projects(project_id, user_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, s.projectID, s.userID, "default", now, now, semFields.LocalWeekday, semFields.LocalLunar); err != nil {
		return fmt.Errorf("insert default project failed: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO threads(thread_id, user_id, project_id, title, created_at_ms, last_active_at_ms, created_at_local_weekday, created_at_local_lunar)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, s.threadID, s.userID, s.projectID, "chat-default", now, now, semFields.LocalWeekday, semFields.LocalLunar); err != nil {
		return fmt.Errorf("insert default thread failed: %w", err)
	}
	return nil
}

func (s *SQLiteStore) needsSchemaReset() (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	chatsExists, err := s.tableExists("chats")
	if err != nil {
		return false, err
	}
	if chatsExists {
		return true, nil
	}

	msgExists, msgCols, err := s.tableColumns("messages")
	if err != nil {
		return false, err
	}
	if msgExists {
		required := []string{"id", "message_uid", "project_id", "thread_id"}
		if msgCols["chat_id"] {
			return true, nil
		}
		for _, name := range required {
			if !msgCols[name] {
				return true, nil
			}
		}
	}

	projExists, projCols, err := s.tableColumns("projects")
	if err != nil {
		return false, err
	}
	if projExists && !projCols["created_at_local_lunar"] {
		return true, nil
	}

	return false, nil
}

func (s *SQLiteStore) resetSchema() error {
	if s == nil || s.db == nil {
		return nil
	}
	dropStmts := []string{
		`DROP TABLE IF EXISTS messages`,
		`DROP TABLE IF EXISTS surface_states`,
		`DROP TABLE IF EXISTS threads`,
		`DROP TABLE IF EXISTS projects`,
		`DROP TABLE IF EXISTS chats`,
		`DROP TABLE IF EXISTS users`,
	}
	for _, stmt := range dropStmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("drop legacy schema failed: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) tableExists(name string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	var actual string
	err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&actual)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query sqlite_master: %w", err)
	}
	return true, nil
}

func (s *SQLiteStore) tableColumns(name string) (bool, map[string]bool, error) {
	exists, err := s.tableExists(name)
	if err != nil {
		return false, nil, err
	}
	if !exists {
		return false, nil, nil
	}
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, name))
	if err != nil {
		return true, nil, fmt.Errorf("pragma table_info(%s): %w", name, err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			colName   string
			colType   string
			notNull   int
			defaultV  sql.NullString
			primaryID int
		)
		if err := rows.Scan(&cid, &colName, &colType, &notNull, &defaultV, &primaryID); err != nil {
			return true, nil, fmt.Errorf("scan table_info(%s): %w", name, err)
		}
		cols[colName] = true
	}
	return true, cols, nil
}

func (s *SQLiteStore) UpsertSurfaceState(state SurfaceState) error {
	_ = state
	// Surface state has moved to "surface self file storage + message events".
	// Keep this API as no-op for backward compatibility.
	return nil
}

func (s *SQLiteStore) LoadSurfaceState(surfaceID string) (SurfaceState, bool, error) {
	_ = surfaceID
	// Surface state has moved to "surface self file storage + message events".
	// Keep this API as no-op for backward compatibility.
	return SurfaceState{}, false, nil
}

func (s *SQLiteStore) migrateUsersTable() error {
	exists, cols, err := s.tableColumns("users")
	if err != nil || !exists {
		return err
	}
	if !cols["username"] {
		if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN username TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate users add username: %w", err)
		}
	}
	if !cols["password_hash"] {
		if _, err := s.db.Exec(`ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("migrate users add password_hash: %w", err)
		}
	}
	return nil
}

// CreateUser creates a new user with a username and password hash.
// Returns the generated user_id.
func (s *SQLiteStore) CreateUser(username string, passwordHash string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("sqlite store not initialized")
	}
	clean := strings.TrimSpace(username)
	if clean == "" {
		return "", fmt.Errorf("username is empty")
	}
	// Check uniqueness
	var existing string
	err := s.db.QueryRow(`SELECT user_id FROM users WHERE username=?`, clean).Scan(&existing)
	if err == nil {
		return "", fmt.Errorf("username already exists")
	}
	if err != sql.ErrNoRows {
		return "", fmt.Errorf("check username: %w", err)
	}
	userID := "usr-" + newRequestID()
	now := nowMS()
	if _, err := s.db.Exec(
		`INSERT INTO users(user_id, username, password_hash, created_at_ms) VALUES(?, ?, ?, ?)`,
		userID, clean, strings.TrimSpace(passwordHash), now,
	); err != nil {
		return "", fmt.Errorf("insert user: %w", err)
	}
	return userID, nil
}

// GetUserByUsername looks up a user by username.
func (s *SQLiteStore) GetUserByUsername(username string) (userID string, passwordHash string, exists bool, err error) {
	if s == nil || s.db == nil {
		return "", "", false, fmt.Errorf("sqlite store not initialized")
	}
	clean := strings.TrimSpace(username)
	if clean == "" {
		return "", "", false, nil
	}
	err = s.db.QueryRow(`SELECT user_id, password_hash FROM users WHERE username=?`, clean).Scan(&userID, &passwordHash)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("query user by username: %w", err)
	}
	return userID, passwordHash, true, nil
}

// GetUserByID looks up a user by user_id.
func (s *SQLiteStore) GetUserByID(userID string) (username string, exists bool, err error) {
	if s == nil || s.db == nil {
		return "", false, fmt.Errorf("sqlite store not initialized")
	}
	err = s.db.QueryRow(`SELECT username FROM users WHERE user_id=?`, strings.TrimSpace(userID)).Scan(&username)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query user by id: %w", err)
	}
	return username, true, nil
}

func (s *SQLiteStore) AppendMessage(msg ChatMessage) (ChatMessage, error) {
	if s == nil || s.db == nil {
		return msg, nil
	}
	payload := map[string]any{}
	if strings.TrimSpace(msg.PayloadJSON) != "" {
		_ = json.Unmarshal([]byte(msg.PayloadJSON), &payload)
	}
	entry, err := BuildMessage(MessageWrite{
		MessageID:            msg.MessageID,
		TurnID:               msg.TurnID,
		Seq:                  msg.Seq,
		Role:                 msg.Role,
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
		return ChatMessage{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ChatMessage{}, fmt.Errorf("begin append message: %w", err)
	}
	defer tx.Rollback()
	entry.Seq, err = s.nextSeq(tx)
	if err != nil {
		return ChatMessage{}, err
	}
	res, err := tx.Exec(`
		INSERT INTO messages(
			message_uid, user_id, project_id, thread_id, turn_id, seq,
			created_at_ms, created_at_iso, created_at_local_ymdhms, created_at_local_weekday, created_at_local_lunar,
			role, category, type, content, payload_schema_version, payload_json,
			completion_status, interrupt, interrupt_at_ms, partial_text
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.MessageID,
		s.userID,
		s.projectID,
		s.threadID,
		entry.TurnID,
		entry.Seq,
		entry.CreatedAtMS,
		entry.CreatedAtISO,
		entry.CreatedAtLocalYMDHMS,
		entry.CreatedAtLocalWeekday,
		entry.CreatedAtLocalLunar,
		entry.Role,
		entry.Category,
		entry.MessageType,
		entry.Content,
		entry.PayloadSchemaVersion,
		entry.PayloadJSON,
		entry.CompletionStatus,
		entry.Interrupt,
		entry.InterruptAtMS,
		entry.PartialText,
	)
	if err != nil {
		return ChatMessage{}, fmt.Errorf("insert message failed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ChatMessage{}, fmt.Errorf("commit append message: %w", err)
	}
	storeID, _ := res.LastInsertId()
	entry.StoreID = storeID
	entry.ProjectID = s.projectID
	entry.ThreadID = s.threadID
	return entry, nil
}

func (s *SQLiteStore) AppendActionCall(call ActionCall, status string, manualConfirm string, blockReason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.AppendMessage(ChatMessage{
		TurnID:      call.TurnID,
		Role:        RoleObserver,
		Category:    CategoryAIAction,
		MessageType: TypeActionCall,
		PayloadJSON: mustJSON(map[string]any{
			"action_id":      firstNonEmpty(call.ActionID, "act-"+newRequestID()),
			"action_name":    call.ActionName,
			"name":           call.ActionName,
			"surface_id":     call.SurfaceID,
			"followup":       normalizeFollowup(call.Followup),
			"args":           clonePayloadMap(call.Args),
			"status":         firstNonEmpty(status, "unknown"),
			"manual_confirm": strings.TrimSpace(manualConfirm),
			"block_reason":   strings.TrimSpace(blockReason),
		}),
		CreatedAtMS: call.RequestedAt,
	})
	return err
}

func (s *SQLiteStore) AppendActionReport(report ActionReport, _ string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.AppendMessage(ChatMessage{
		TurnID:      report.TurnID,
		Role:        RoleObserver,
		Category:    CategoryAIAction,
		MessageType: TypeActionReport,
		Content:     formatActionReportText(report),
		PayloadJSON: mustJSON(map[string]any{
			"report_id":       report.ReportID,
			"origin":          report.Origin,
			"action_id":       report.ActionID,
			"action_name":     report.ActionName,
			"name":            report.ActionName,
			"surface_id":      report.SurfaceID,
			"surface_type":    report.SurfaceType,
			"surface_version": report.SurfaceVersion,
			"followup":        normalizeFollowup(report.Followup),
			"status":          report.Status,
			"result_summary":  report.ResultSummary,
			"effect_summary":  report.EffectSummary,
			"business_state":  clonePayloadMap(report.BusinessState),
			"manual_confirm":  report.ManualConfirm,
			"block_reason":    report.BlockReason,
		}),
		CreatedAtMS: report.CreatedAtMS,
	})
	return err
}

func (s *SQLiteStore) nextSeq(tx *sql.Tx) (int64, error) {
	var seq int64
	if err := tx.QueryRow(`
		SELECT COALESCE(MAX(seq), 0) + 1
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=?
	`, s.userID, s.projectID, s.threadID).Scan(&seq); err != nil {
		return 0, fmt.Errorf("query next seq failed: %w", err)
	}
	return seq, nil
}

func (s *SQLiteStore) LoadSessionWindow(anchorLimit int, totalLimit int) ([]ChatMessage, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if anchorLimit <= 0 {
		anchorLimit = 20
	}
	if totalLimit <= 0 {
		totalLimit = maxInt(anchorLimit*4, 64)
	}

	anchors := make([]int64, 0, anchorLimit)
	rows, err := s.db.Query(`
		SELECT id
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=? AND category=? AND type IN (?, ?)
		ORDER BY id DESC
		LIMIT ?
	`, s.userID, s.projectID, s.threadID, CategoryChat, TypeUserMessage, TypeAssistantMessage, anchorLimit)
	if err != nil {
		return nil, fmt.Errorf("load anchor window failed: %w", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan anchor window failed: %w", err)
		}
		anchors = append(anchors, id)
	}
	rows.Close()
	if len(anchors) == 0 {
		return nil, nil
	}
	anchorID := anchors[len(anchors)-1]
	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(1)
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=? AND id >= ?
	`, s.userID, s.projectID, s.threadID, anchorID).Scan(&count); err != nil {
		return nil, fmt.Errorf("count session window failed: %w", err)
	}
	if count > totalLimit {
		return s.loadByQuery(`
			SELECT id, message_uid, project_id, thread_id, turn_id, seq, role, category, type, content, payload_schema_version, payload_json,
				created_at_ms, created_at_iso, created_at_local_ymdhms, created_at_local_weekday, created_at_local_lunar,
				completion_status, interrupt, interrupt_at_ms, partial_text
			FROM messages
			WHERE user_id=? AND project_id=? AND thread_id=?
			ORDER BY id DESC
			LIMIT ?
		`, []any{s.userID, s.projectID, s.threadID, totalLimit}, true)
	}
	return s.loadByQuery(`
		SELECT id, message_uid, project_id, thread_id, turn_id, seq, role, category, type, content, payload_schema_version, payload_json,
			created_at_ms, created_at_iso, created_at_local_ymdhms, created_at_local_weekday, created_at_local_lunar,
			completion_status, interrupt, interrupt_at_ms, partial_text
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=? AND id >= ?
		ORDER BY id ASC
	`, []any{s.userID, s.projectID, s.threadID, anchorID}, false)
}

func (s *SQLiteStore) LoadRecentContext(limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.LoadSessionWindow(limit, maxInt(limit*4, 64))
}

func (s *SQLiteStore) LoadContextBefore(beforeID int64, limit int) ([]ChatMessage, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = 20
	}
	args := []any{s.userID, s.projectID, s.threadID, CategoryChat, TypeUserMessage, TypeAssistantMessage, limit + 1}
	query := `
		SELECT id, message_uid, project_id, thread_id, turn_id, seq, role, category, type, content, payload_schema_version, payload_json,
			created_at_ms, created_at_iso, created_at_local_ymdhms, created_at_local_weekday, created_at_local_lunar,
			completion_status, interrupt, interrupt_at_ms, partial_text
		FROM messages
		WHERE user_id=? AND project_id=? AND thread_id=? AND category=? AND type IN (?, ?)
	`
	if beforeID > 0 {
		query += ` AND id < ?`
		args = []any{s.userID, s.projectID, s.threadID, CategoryChat, TypeUserMessage, TypeAssistantMessage, beforeID, limit + 1}
	}
	query += ` ORDER BY id DESC LIMIT ?`
	messages, err := s.loadByQuery(query, args, true)
	if err != nil {
		return nil, false, err
	}
	hasMore := false
	if len(messages) > limit {
		hasMore = true
		messages = messages[len(messages)-limit:]
	}
	return messages, hasMore, nil
}

func (s *SQLiteStore) loadByQuery(query string, args []any, reverse bool) ([]ChatMessage, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("load messages failed: %w", err)
	}
	defer rows.Close()
	out := make([]ChatMessage, 0, 32)
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(
			&msg.StoreID,
			&msg.MessageID,
			&msg.ProjectID,
			&msg.ThreadID,
			&msg.TurnID,
			&msg.Seq,
			&msg.Role,
			&msg.Category,
			&msg.MessageType,
			&msg.Content,
			&msg.PayloadSchemaVersion,
			&msg.PayloadJSON,
			&msg.CreatedAtMS,
			&msg.CreatedAtISO,
			&msg.CreatedAtLocalYMDHMS,
			&msg.CreatedAtLocalWeekday,
			&msg.CreatedAtLocalLunar,
			&msg.CompletionStatus,
			&msg.Interrupt,
			&msg.InterruptAtMS,
			&msg.PartialText,
		); err != nil {
			return nil, fmt.Errorf("scan messages failed: %w", err)
		}
		msg.Visibility = messageVisibility(msg.Role, msg.Category, msg.MessageType)
		out = append(out, msg)
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, nil
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

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
