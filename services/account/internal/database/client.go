package database

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

const (
	defaultDBName      = "account.db"
	defaultToolTimeout = 8 * time.Second
)

var ErrUsernameExists = errors.New("username already exists")

type SigningKey struct {
	KID         string
	Alg         string
	PublicKey   string
	PrivateKey  ed25519.PrivateKey
	CreatedAtMS int64
}

type UserRecord struct {
	UserID       string
	Username     string
	PasswordHash string
}

type Store interface {
	EnsureSchema(ctx context.Context) error
	GetOrCreateSigningKey(ctx context.Context) (SigningKey, error)
	CreateUser(ctx context.Context, username string, passwordHash string) (string, error)
	GetUserByUsername(ctx context.Context, username string) (UserRecord, bool, error)
	GetUserByID(ctx context.Context, userID string) (UserRecord, bool, error)
	UpdatePasswordHash(ctx context.Context, userID string, passwordHash string) error
	SetActiveSession(ctx context.Context, userID string, sid string) error
	ClearActiveSession(ctx context.Context, userID string) error
	ListActiveSessions(ctx context.Context) ([]toolproto.AccountActiveSession, error)
	ListPublicKeys(ctx context.Context) ([]toolproto.AccountPublicKey, error)
}

type Client struct {
	baseURL     string
	serviceAuth hubsvc.BootstrapSecret
	httpClient  *http.Client
	dbName      string
}

func NewClient(baseURL string, serviceAuth hubsvc.BootstrapSecret, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultToolTimeout
	}
	return &Client{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		serviceAuth: serviceAuth,
		httpClient:  &http.Client{Timeout: timeout},
		dbName:      defaultDBName,
	}
}

func (c *Client) EnsureSchema(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("database client is nil")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			user_id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_account_users_username ON users(username)`,
		`CREATE TABLE IF NOT EXISTS active_sessions (
			user_id TEXT PRIMARY KEY,
			sid TEXT NOT NULL,
			updated_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS signing_keys (
			kid TEXT PRIMARY KEY,
			alg TEXT NOT NULL,
			private_key TEXT NOT NULL,
			public_key TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, _, err := c.execute(ctx, stmt, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) GetOrCreateSigningKey(ctx context.Context) (SigningKey, error) {
	rows, err := c.query(ctx, `
		SELECT kid, alg, private_key, public_key, created_at_ms
		FROM signing_keys
		ORDER BY created_at_ms DESC
		LIMIT 1
	`, nil)
	if err != nil {
		return SigningKey{}, err
	}
	if len(rows) > 0 {
		key, err := decodeSigningKey(rows[0])
		if err == nil {
			return key, nil
		}
	}
	key, err := c.createSigningKey(ctx)
	if err == nil {
		return key, nil
	}
	rows, queryErr := c.query(ctx, `
		SELECT kid, alg, private_key, public_key, created_at_ms
		FROM signing_keys
		ORDER BY created_at_ms DESC
		LIMIT 1
	`, nil)
	if queryErr == nil && len(rows) > 0 {
		if key, decodeErr := decodeSigningKey(rows[0]); decodeErr == nil {
			return key, nil
		}
	}
	return SigningKey{}, err
}

func (c *Client) CreateUser(ctx context.Context, username string, passwordHash string) (string, error) {
	cleanUsername := strings.TrimSpace(username)
	cleanHash := strings.TrimSpace(passwordHash)
	if cleanUsername == "" || cleanHash == "" {
		return "", fmt.Errorf("username and password_hash are required")
	}
	now := time.Now().UnixMilli()
	userID := "usr-" + newID()
	_, _, err := c.execute(ctx, `
		INSERT INTO users(user_id, username, password_hash, created_at_ms, updated_at_ms)
		VALUES(?, ?, ?, ?, ?)
	`, []any{userID, cleanUsername, cleanHash, now, now})
	if err != nil {
		if isUniqueUsernameError(err) {
			return "", ErrUsernameExists
		}
		return "", err
	}
	return userID, nil
}

func (c *Client) GetUserByUsername(ctx context.Context, username string) (UserRecord, bool, error) {
	clean := strings.TrimSpace(username)
	if clean == "" {
		return UserRecord{}, false, nil
	}
	rows, err := c.query(ctx, `
		SELECT user_id, username, password_hash
		FROM users
		WHERE username = ?
		LIMIT 1
	`, []any{clean})
	if err != nil {
		return UserRecord{}, false, err
	}
	if len(rows) == 0 {
		return UserRecord{}, false, nil
	}
	return decodeUserRecord(rows[0])
}

func (c *Client) GetUserByID(ctx context.Context, userID string) (UserRecord, bool, error) {
	clean := strings.TrimSpace(userID)
	if clean == "" {
		return UserRecord{}, false, nil
	}
	rows, err := c.query(ctx, `
		SELECT user_id, username, password_hash
		FROM users
		WHERE user_id = ?
		LIMIT 1
	`, []any{clean})
	if err != nil {
		return UserRecord{}, false, err
	}
	if len(rows) == 0 {
		return UserRecord{}, false, nil
	}
	return decodeUserRecord(rows[0])
}

func (c *Client) UpdatePasswordHash(ctx context.Context, userID string, passwordHash string) error {
	uid := strings.TrimSpace(userID)
	hash := strings.TrimSpace(passwordHash)
	if uid == "" || hash == "" {
		return fmt.Errorf("user_id and password_hash are required")
	}
	_, _, err := c.execute(ctx, `
		UPDATE users
		SET password_hash = ?, updated_at_ms = ?
		WHERE user_id = ?
	`, []any{hash, time.Now().UnixMilli(), uid})
	return err
}

func (c *Client) SetActiveSession(ctx context.Context, userID string, sid string) error {
	uid := strings.TrimSpace(userID)
	sessionID := strings.TrimSpace(sid)
	if uid == "" || sessionID == "" {
		return fmt.Errorf("user_id and sid are required")
	}
	_, _, err := c.execute(ctx, `
		INSERT INTO active_sessions(user_id, sid, updated_at_ms)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			sid=excluded.sid,
			updated_at_ms=excluded.updated_at_ms
	`, []any{uid, sessionID, time.Now().UnixMilli()})
	return err
}

func (c *Client) ClearActiveSession(ctx context.Context, userID string) error {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil
	}
	_, _, err := c.execute(ctx, `DELETE FROM active_sessions WHERE user_id = ?`, []any{uid})
	return err
}

func (c *Client) ListActiveSessions(ctx context.Context) ([]toolproto.AccountActiveSession, error) {
	rows, err := c.query(ctx, `SELECT user_id, sid FROM active_sessions ORDER BY updated_at_ms DESC`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]toolproto.AccountActiveSession, 0, len(rows))
	for _, row := range rows {
		userID := strings.TrimSpace(asString(row["user_id"]))
		sid := strings.TrimSpace(asString(row["sid"]))
		if userID == "" || sid == "" {
			continue
		}
		out = append(out, toolproto.AccountActiveSession{UserID: userID, SID: sid})
	}
	return out, nil
}

func (c *Client) ListPublicKeys(ctx context.Context) ([]toolproto.AccountPublicKey, error) {
	rows, err := c.query(ctx, `
		SELECT kid, alg, public_key
		FROM signing_keys
		ORDER BY created_at_ms DESC
	`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]toolproto.AccountPublicKey, 0, len(rows))
	for _, row := range rows {
		kid := strings.TrimSpace(asString(row["kid"]))
		alg := strings.TrimSpace(asString(row["alg"]))
		publicKey := strings.TrimSpace(asString(row["public_key"]))
		if kid == "" || publicKey == "" {
			continue
		}
		out = append(out, toolproto.AccountPublicKey{
			KID:       kid,
			Alg:       alg,
			PublicKey: publicKey,
		})
	}
	return out, nil
}

func (c *Client) createSigningKey(ctx context.Context) (SigningKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SigningKey{}, err
	}
	next := SigningKey{
		KID:         "kid-" + newID(),
		Alg:         "ed25519",
		PublicKey:   base64.RawURLEncoding.EncodeToString(publicKey),
		PrivateKey:  privateKey,
		CreatedAtMS: time.Now().UnixMilli(),
	}
	_, _, err = c.execute(ctx, `
		INSERT INTO signing_keys(kid, alg, private_key, public_key, created_at_ms)
		VALUES(?, ?, ?, ?, ?)
	`, []any{next.KID, next.Alg, base64.RawURLEncoding.EncodeToString(privateKey), next.PublicKey, next.CreatedAtMS})
	if err != nil {
		return SigningKey{}, err
	}
	return next, nil
}

func (c *Client) query(ctx context.Context, query string, args []any) ([]map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := c.call(ctx, "storage.database.query", map[string]any{
		"db_name": c.dbName,
		"query":   strings.TrimSpace(query),
		"args":    args,
	}, 8*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, callResponseError(resp)
	}
	rows, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected query result type")
	}
	raw, err := json.Marshal(rows["rows"])
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) execute(ctx context.Context, query string, args []any) (int64, int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := c.call(ctx, "storage.database.execute", map[string]any{
		"db_name": c.dbName,
		"query":   strings.TrimSpace(query),
		"args":    args,
	}, 8*time.Second)
	if err != nil {
		return 0, 0, err
	}
	if !resp.Ok {
		return 0, 0, callResponseError(resp)
	}
	payload, ok := resp.Result.(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("unexpected execute result type")
	}
	return asInt64(payload["rows_affected"]), asInt64(payload["last_insert_id"]), nil
}

func (c *Client) call(ctx context.Context, toolID string, args map[string]any, timeout time.Duration) (toolproto.CallResponse, error) {
	if c == nil {
		return toolproto.CallResponse{}, fmt.Errorf("database client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callReq := toolproto.CallRequest{
		ToolID: toolID,
		Args:   args,
		Context: &toolproto.Context{
			RequestID: "req-" + newID(),
			TraceID:   "tr-" + newID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: "account",
			},
		},
	}
	raw, err := json.Marshal(callReq)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(raw))
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(req.Header, c.serviceAuth)
	client := *c.httpClient
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	defer resp.Body.Close()
	var out toolproto.CallResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return toolproto.CallResponse{}, fmt.Errorf("decode tool response: %w", err)
	}
	if resp.StatusCode >= 300 && out.Error == nil {
		out.Error = &toolproto.Error{
			Code:    toolproto.ErrorCodeToolExecError,
			Message: fmt.Sprintf("database tool request failed: status=%d", resp.StatusCode),
		}
	}
	return out, nil
}

func decodeSigningKey(row map[string]any) (SigningKey, error) {
	privateRaw := strings.TrimSpace(asString(row["private_key"]))
	privateKey, err := base64.RawURLEncoding.DecodeString(privateRaw)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return SigningKey{}, fmt.Errorf("invalid stored private key")
	}
	return SigningKey{
		KID:         strings.TrimSpace(asString(row["kid"])),
		Alg:         strings.TrimSpace(asString(row["alg"])),
		PublicKey:   strings.TrimSpace(asString(row["public_key"])),
		PrivateKey:  ed25519.PrivateKey(privateKey),
		CreatedAtMS: asInt64(row["created_at_ms"]),
	}, nil
}

func decodeUserRecord(row map[string]any) (UserRecord, bool, error) {
	out := UserRecord{
		UserID:       strings.TrimSpace(asString(row["user_id"])),
		Username:     strings.TrimSpace(asString(row["username"])),
		PasswordHash: strings.TrimSpace(asString(row["password_hash"])),
	}
	if out.UserID == "" || out.Username == "" {
		return UserRecord{}, false, nil
	}
	return out, true, nil
}

func isUniqueUsernameError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "users.username")
}

func asString(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	case []byte:
		return string(tv)
	case fmt.Stringer:
		return tv.String()
	default:
		return ""
	}
}

func asInt64(v any) int64 {
	switch tv := v.(type) {
	case int:
		return int64(tv)
	case int32:
		return int64(tv)
	case int64:
		return tv
	case float64:
		return int64(tv)
	case json.Number:
		n, _ := tv.Int64()
		return n
	default:
		return 0
	}
}

func callResponseError(r toolproto.CallResponse) error {
	if r.Error == nil {
		return fmt.Errorf("tool call failed")
	}
	return fmt.Errorf("%s", strings.TrimSpace(r.Error.Message))
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
