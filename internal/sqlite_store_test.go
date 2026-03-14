package app

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteStoreModernFlow(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "kagent.db"), "default", "project-default", "chat-default")
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	userMsg, err := store.AppendMessage(ChatMessage{
		TurnID:      1,
		Role:        RoleUser,
		Category:    CategoryChat,
		MessageType: TypeUserMessage,
		Content:     "你好",
		PayloadJSON: `{"text":"你好"}`,
		CreatedAtMS: 1710000001000,
	})
	if err != nil {
		t.Fatalf("AppendMessage(user) failed: %v", err)
	}
	if userMsg.MessageID == "" || userMsg.Seq == 0 || userMsg.StoreID <= 0 {
		t.Fatalf("unexpected user message: %#v", userMsg)
	}
	if userMsg.Say != "你好" {
		t.Fatalf("expected say persisted, got %#v", userMsg)
	}

	if err := store.AppendActionCall(ActionCall{
		ActionID:    "act-1",
		ActionName:  "surface.call.counter.set_count",
		SurfaceID:   "counter",
		TurnID:      1,
		Followup:    "report",
		Args:        map[string]any{"count": 9},
		RequestedAt: 1710000002000,
	}, "ok", "", ""); err != nil {
		t.Fatalf("AppendActionCall failed: %v", err)
	}

	if err := store.AppendActionReport(ActionReport{
		ReportID:       "rep-1",
		Origin:         "action_callback",
		ActionID:       "act-1",
		ActionName:     "surface.call.counter.set_count",
		SurfaceID:      "counter",
		SurfaceType:    "app",
		SurfaceVersion: "1",
		TurnID:         1,
		Followup:       "report",
		Status:         "ok",
		ResultSummary:  `{"queued":false}`,
		EffectSummary:  `{"count":9}`,
		BusinessState:  map[string]any{"count": 9},
		CreatedAtMS:    1710000003000,
	}, "msg-1"); err != nil {
		t.Fatalf("AppendActionReport failed: %v", err)
	}

	assistantMsg, err := store.AppendMessage(ChatMessage{
		TurnID:           1,
		Role:             RoleAssistant,
		Category:         CategoryChat,
		MessageType:      TypeAssistantMessage,
		Content:          "已把数字改成 9。",
		PayloadJSON:      `{"text":"已把数字改成 9。"}`,
		CreatedAtMS:      1710000004000,
		CompletionStatus: CompletionStatusComplete,
		Interrupt:        InterruptNone,
	})
	if err != nil {
		t.Fatalf("AppendMessage(assistant) failed: %v", err)
	}
	if assistantMsg.Seq <= userMsg.Seq {
		t.Fatalf("assistant seq should advance: user=%d assistant=%d", userMsg.Seq, assistantMsg.Seq)
	}
	if assistantMsg.StoreID <= userMsg.StoreID {
		t.Fatalf("assistant store id should advance: user=%d assistant=%d", userMsg.StoreID, assistantMsg.StoreID)
	}

	history, err := store.LoadSessionWindow(2, 10)
	if err != nil {
		t.Fatalf("LoadSessionWindow failed: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("expected 4 messages in session window, got %d", len(history))
	}
	if history[1].Category != CategoryAIAction || history[2].Category != CategoryAIAction {
		t.Fatalf("expected action messages to stay in session window: %#v", history)
	}
	if history[2].ActionJSON == "" {
		t.Fatalf("expected action_json in action report: %#v", history[2])
	}

	visible, hasMore, err := store.LoadContextBefore(0, 10)
	if err != nil {
		t.Fatalf("LoadContextBefore failed: %v", err)
	}
	if hasMore {
		t.Fatalf("did not expect hasMore for small history")
	}
	if len(visible) != 2 {
		t.Fatalf("expected only visible chat messages, got %d", len(visible))
	}
	if visible[0].Role != RoleUser || visible[1].Role != RoleAssistant {
		t.Fatalf("unexpected visible history: %#v", visible)
	}
	if visible[0].StoreID <= 0 || visible[1].StoreID <= 0 {
		t.Fatalf("visible history should expose store_id: %#v", visible)
	}

	older, hasMore, err := store.LoadContextBefore(visible[1].StoreID, 1)
	if err != nil {
		t.Fatalf("LoadContextBefore(before_id) failed: %v", err)
	}
	if hasMore {
		t.Fatalf("did not expect hasMore in single-page check")
	}
	if len(older) != 1 || older[0].Role != RoleUser {
		t.Fatalf("unexpected older page: %#v", older)
	}
}

func TestSQLiteStoreResetsLegacySchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE users (user_id TEXT PRIMARY KEY, created_at_ms INTEGER NOT NULL)`,
		`CREATE TABLE chats (chat_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, created_at_ms INTEGER NOT NULL)`,
		`CREATE TABLE messages (
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
		`INSERT INTO users(user_id, created_at_ms) VALUES('default', 1)`,
		`INSERT INTO chats(chat_id, user_id, created_at_ms) VALUES('chat-default', 'default', 1)`,
		`INSERT INTO messages(message_id, user_id, chat_id, turn_id, role_internal, role_provider, message_type, content_text, visibility, meta_json, created_at_ms)
		 VALUES('msg-1', 'default', 'chat-default', 1, 'user', 'user', 'chat', '你好', 'visible', '{"origin":"legacy"}', 1710000001000)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy setup failed: %v", err)
		}
	}
	_ = db.Close()

	store, err := NewSQLiteStore(dbPath, "default", "project-default", "chat-default")
	if err != nil {
		t.Fatalf("NewSQLiteStore reset failed: %v", err)
	}
	defer store.Close()

	exists, err := store.tableExists("chats")
	if err != nil {
		t.Fatalf("tableExists(chats) failed: %v", err)
	}
	if exists {
		t.Fatalf("legacy chats table should be dropped after reset")
	}

	visible, hasMore, err := store.LoadContextBefore(0, 10)
	if err != nil {
		t.Fatalf("LoadContextBefore failed: %v", err)
	}
	if hasMore {
		t.Fatalf("unexpected hasMore for reset db")
	}
	if len(visible) != 0 {
		t.Fatalf("legacy messages should not be migrated in reset mode, got %d", len(visible))
	}

	msg, err := store.AppendMessage(ChatMessage{
		TurnID:      1,
		Role:        RoleUser,
		Category:    CategoryChat,
		MessageType: TypeUserMessage,
		Content:     "reset-ok",
		PayloadJSON: `{"text":"reset-ok"}`,
		CreatedAtMS: 1710000010000,
	})
	if err != nil {
		t.Fatalf("AppendMessage after reset failed: %v", err)
	}
	if msg.StoreID <= 0 {
		t.Fatalf("expected store id after reset append, got %#v", msg)
	}
}

func TestUserDataStrictIsolation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "iso.db")

	// User A opens store and sends a message
	storeA, err := NewSQLiteStore(dbPath, "user-a", "prj-a", "thd-a")
	if err != nil {
		t.Fatalf("StoreA init failed: %v", err)
	}
	_, err = storeA.AppendMessage(ChatMessage{
		TurnID: 1, Role: RoleUser, Category: CategoryChat, MessageType: TypeUserMessage,
		Content: "Secret Message A", CreatedAtMS: 1000,
	})
	if err != nil {
		t.Fatalf("StoreA append failed: %v", err)
	}
	storeA.Close()

	// User B opens the SAME database
	storeB, err := NewSQLiteStore(dbPath, "user-b", "prj-b", "thd-b")
	if err != nil {
		t.Fatalf("StoreB init failed: %v", err)
	}
	defer storeB.Close()

	if storeB.RuntimeUserID() != "user-b" {
		t.Fatalf("StoreB identity hijacked: expected user-b, got %s", storeB.RuntimeUserID())
	}

	// User B should NOT see User A's history
	history, err := storeB.LoadSessionWindow(10, 50)
	if err != nil {
		t.Fatalf("StoreB load history failed: %v", err)
	}
	for _, m := range history {
		if m.Content == "Secret Message A" {
			t.Fatal("Data Leakage! User B saw User A's secret message")
		}
	}

	if len(history) != 0 {
		// Note: Depending on implementation, history might contain system messages,
		// but shouldn't contain ChatMessage from other users.
		// In our current implementation, it should be empty for a new user/thread.
		t.Fatalf("Expected empty history for new user-b, got %d messages", len(history))
	}
}
