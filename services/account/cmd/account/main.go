package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	serviceID      = "account"
	serviceName    = "Account Service"
	serviceVersion = "1.0.0"

	passwordMinLen = 6
	tokenMaxAgeSec = 30 * 24 * 3600
)

type callerIdentity struct {
	Type      string
	UserID    string
	ServiceID string
	SurfaceID string
}

type accountClaims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username,omitempty"`
	SID      string `json:"sid"`
	IatMS    int64  `json:"iat_ms"`
	ExpMS    int64  `json:"exp_ms"`
	KID      string `json:"kid"`
}

type signingKey struct {
	KID     string
	Alg     string
	Public  string
	Private ed25519.PrivateKey
	Created int64
}

type userRecord struct {
	UserID       string
	Username     string
	PasswordHash string
}

type accountStore struct {
	db *sql.DB
}

var errUsernameExists = errors.New("username already exists")

func main() {
	addr := flag.String("addr", "127.0.0.1:18083", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	appRoot, err := detectAppRoot()
	if err != nil {
		log.Printf("warn: detect app root fallback: %v", err)
	}
	storePath := filepath.Join(appRoot, "data", "account", "account.db")
	store, err := newAccountStore(storePath)
	if err != nil {
		log.Printf("error: init account store failed: %v", err)
		os.Exit(1)
	}
	defer store.Close()

	key, err := store.getOrCreateSigningKey()
	if err != nil {
		log.Printf("error: init signing key failed: %v", err)
		os.Exit(1)
	}

	serviceSecretPath := filepath.Join(appRoot, "services", "account", "run", ".service_secret")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		log.Printf("error: load bootstrap secret failed: %v", err)
		os.Exit(1)
	}
	if strings.TrimSpace(serviceBootstrap.ServiceID) != serviceID {
		log.Printf("error: bootstrap service_id mismatch: expect=%s got=%s", serviceID, strings.TrimSpace(serviceBootstrap.ServiceID))
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(serviceBootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	instance := strings.TrimSpace(serviceBootstrap.InstanceID)
	if instance == "" {
		instance = strings.TrimSpace(*instanceID)
	}
	if instance == "" {
		instance = serviceID + "-" + newID()
	}

	if registerURL != "" {
		if err := registerToHub(registerURL, strings.TrimSpace(*addr), instance, serviceBootstrap); err != nil {
			log.Printf("error: register account to hub failed: %v", err)
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			log.Printf("warn: delete bootstrap secret failed: %v", err)
		}
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			log.Printf("warn: account service shutdown: %s", strings.TrimSpace(reason))
			if server != nil {
				_ = server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				_ = server.Shutdown(ctx)
				cancel()
			}
			time.Sleep(80 * time.Millisecond)
			os.Exit(0)
		})
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "timestamp_ms": time.Now().UnixMilli()})
	})
	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeToolResponse(w, http.StatusMethodNotAllowed, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "method not allowed",
				},
			})
			return
		}
		if err := hubsvc.VerifyHubAuthHeaders(r.Header, serviceID, instance, serviceBootstrap.H2SToken); err != nil {
			writeToolResponse(w, http.StatusForbidden, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeForbidden,
					Message: "invalid hub auth",
				},
			})
			return
		}
		var req toolproto.CallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "invalid request body",
				},
			})
			return
		}
		req, err = toolproto.NormalizeRequest(req)
		if err != nil {
			writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: err.Error(),
				},
			})
			return
		}
		if req.Context == nil {
			req.Context = &toolproto.Context{}
		}
		caller := resolveCaller(r, req.Context)
		req.Context.Caller = toolproto.Caller{
			Type:      caller.Type,
			UserID:    caller.UserID,
			ServiceID: caller.ServiceID,
			SurfaceID: caller.SurfaceID,
		}
		hubOnly := isHubOnlyContext(req.Context)

		meta := toolproto.Meta{
			RequestID:  strings.TrimSpace(req.Context.RequestID),
			TraceID:    strings.TrimSpace(req.Context.TraceID),
			ServiceID:  serviceID,
			InstanceID: instance,
		}
		startedAt := time.Now()
		resp := toolproto.CallResponse{
			Ok:    false,
			Meta:  meta,
			Error: &toolproto.Error{Code: toolproto.ErrorCodeToolExecError, Message: "tool execution failed"},
		}
		statusCode := http.StatusOK

		switch req.ToolID {
		case "account.auth.register":
			username := strings.TrimSpace(asString(req.Args["username"]))
			password := asString(req.Args["password"])
			if username == "" {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: "username is required"}
				statusCode = http.StatusBadRequest
				break
			}
			if len(password) < passwordMinLen {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: "password too short"}
				statusCode = http.StatusBadRequest
				break
			}
			hash, hashErr := hashPassword(password)
			if hashErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "hash password failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			userID, createErr := store.createUser(username, hash)
			if createErr != nil {
				if errors.Is(createErr, errUsernameExists) {
					resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeConflict, Message: "username already exists"}
					statusCode = http.StatusConflict
					break
				}
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "register failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			sid := newSessionID()
			if err := store.setActiveSID(userID, sid); err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "set active session failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			token, expMS, tokenErr := issueToken(key, userID, username, sid)
			if tokenErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "issue token failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = map[string]any{
				"user_id":  userID,
				"username": username,
				"sid":      sid,
				"exp_ms":   expMS,
			}
			resp.Effects = &toolproto.Effects{
				SetCookies: []toolproto.SetCookieEffect{{
					Name:      "token",
					Value:     token,
					MaxAgeSec: tokenMaxAgeSec,
				}},
			}
		case "account.auth.login":
			username := strings.TrimSpace(asString(req.Args["username"]))
			password := asString(req.Args["password"])
			if username == "" || password == "" {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: "username and password are required"}
				statusCode = http.StatusBadRequest
				break
			}
			user, ok, queryErr := store.getUserByUsername(username)
			if queryErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "login query failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			if !ok || !verifyPassword(password, user.PasswordHash) {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "invalid username or password"}
				statusCode = http.StatusUnauthorized
				break
			}
			sid := newSessionID()
			if err := store.setActiveSID(user.UserID, sid); err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "set active session failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			token, expMS, tokenErr := issueToken(key, user.UserID, user.Username, sid)
			if tokenErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "issue token failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = map[string]any{
				"user_id":  user.UserID,
				"username": user.Username,
				"sid":      sid,
				"exp_ms":   expMS,
			}
			resp.Effects = &toolproto.Effects{
				SetCookies: []toolproto.SetCookieEffect{{
					Name:      "token",
					Value:     token,
					MaxAgeSec: tokenMaxAgeSec,
				}},
			}
		case "account.auth.logout":
			if !strings.EqualFold(caller.Type, "user") || strings.TrimSpace(caller.UserID) == "" {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "login required"}
				statusCode = http.StatusUnauthorized
				break
			}
			if err := store.clearActiveSID(caller.UserID); err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "logout failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = map[string]any{
				"user_id": caller.UserID,
				"ok":      true,
			}
			resp.Effects = &toolproto.Effects{
				SetCookies: []toolproto.SetCookieEffect{{
					Name:      "token",
					Value:     "",
					MaxAgeSec: -1,
				}},
			}
		case "account.auth.me":
			if !strings.EqualFold(caller.Type, "user") || strings.TrimSpace(caller.UserID) == "" {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "login required"}
				statusCode = http.StatusUnauthorized
				break
			}
			user, ok, queryErr := store.getUserByID(caller.UserID)
			if queryErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "query user failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			if !ok {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "user not found"}
				statusCode = http.StatusUnauthorized
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = map[string]any{
				"user_id":  user.UserID,
				"username": user.Username,
			}
		case "account.auth.password_change":
			if !strings.EqualFold(caller.Type, "user") || strings.TrimSpace(caller.UserID) == "" {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "login required"}
				statusCode = http.StatusUnauthorized
				break
			}
			oldPassword := asString(req.Args["old_password"])
			newPassword := asString(req.Args["new_password"])
			if len(newPassword) < passwordMinLen {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeBadRequest, Message: "new password too short"}
				statusCode = http.StatusBadRequest
				break
			}
			user, ok, queryErr := store.getUserByID(caller.UserID)
			if queryErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "query user failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			if !ok {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "user not found"}
				statusCode = http.StatusUnauthorized
				break
			}
			if !verifyPassword(oldPassword, user.PasswordHash) {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeUnauthorized, Message: "old password mismatch"}
				statusCode = http.StatusUnauthorized
				break
			}
			newHash, hashErr := hashPassword(newPassword)
			if hashErr != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "hash password failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			if err := store.updatePasswordHash(user.UserID, newHash); err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "update password failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			sid := newSessionID()
			if err := store.setActiveSID(user.UserID, sid); err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "set active session failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = map[string]any{
				"user_id": user.UserID,
				"sid":     sid,
				"ok":      true,
			}
			resp.Effects = &toolproto.Effects{
				SetCookies: []toolproto.SetCookieEffect{{
					Name:      "token",
					Value:     "",
					MaxAgeSec: -1,
				}},
			}
		case "account.system.keys.get":
			if !hubOnly && !isHubServiceCaller(caller) {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeForbidden, Message: "forbidden"}
				statusCode = http.StatusForbidden
				break
			}
			keys, err := store.listPublicKeys()
			if err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "query keys failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = toolproto.AccountPublicKeysResult{Keys: keys}
		case "account.session.dump_active":
			if !hubOnly && !isHubServiceCaller(caller) {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeForbidden, Message: "forbidden"}
				statusCode = http.StatusForbidden
				break
			}
			items, err := store.dumpActiveSessions()
			if err != nil {
				resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeInternalError, Message: "dump sessions failed"}
				statusCode = http.StatusInternalServerError
				break
			}
			resp.Ok = true
			resp.Error = nil
			resp.Result = toolproto.AccountActiveSessionsResult{Items: items}
		default:
			resp.Error = &toolproto.Error{Code: toolproto.ErrorCodeToolNotFound, Message: "tool not found"}
			statusCode = http.StatusNotFound
		}

		resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
		if !resp.Ok && resp.Error != nil {
			statusCode = toolproto.HTTPStatusFromCode(resp.Error.Code)
		}
		writeToolResponse(w, statusCode, resp)
	})

	mux.HandleFunc("/admin/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "bad remote addr", http.StatusBadRequest)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "message": "shutting down"})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			shutdownNow("admin shutdown requested")
		}()
	})

	server = &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if hbURL := buildHubHeartbeatURL(registerURL); hbURL != "" {
		startHubHeartbeatGuard(hbURL, serviceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	log.Printf("info: account service listening=http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("error: server failed: %v", err)
		os.Exit(1)
	}
}

func registerToHub(registerURL string, addr string, instance string, bootstrap hubsvc.BootstrapSecret) error {
	healthy := true
	registerPayload := toolproto.SupervisorRegisterRequest{
		ServiceID:  serviceID,
		InstanceID: instance,
		Version:    serviceVersion,
		Transport:  "tcp",
		Endpoint: toolproto.Endpoint{
			TCPURL: "http://" + strings.TrimSpace(addr),
		},
		Tools:   supervisorTools(),
		Healthy: &healthy,
	}
	raw, _ := json.Marshal(registerPayload)
	req, _ := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(req.Header, bootstrap)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
	}
	if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
		return err
	}
	return nil
}

func supervisorTools() []toolproto.ServiceTool {
	return []toolproto.ServiceTool{
		{
			ToolID:             "account.auth.register",
			Version:            serviceVersion,
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"anonymous", "user"},
		},
		{
			ToolID:             "account.auth.login",
			Version:            serviceVersion,
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"anonymous", "user"},
		},
		{
			ToolID:             "account.auth.logout",
			Version:            serviceVersion,
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"user"},
		},
		{
			ToolID:             "account.auth.me",
			Version:            serviceVersion,
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"user"},
		},
		{
			ToolID:             "account.auth.password_change",
			Version:            serviceVersion,
			TimeoutMS:          5000,
			AllowedCallerTypes: []string{"user"},
		},
		{
			ToolID:             "account.system.keys.get",
			Version:            serviceVersion,
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
		{
			ToolID:             "account.session.dump_active",
			Version:            serviceVersion,
			TimeoutMS:          3000,
			AllowedCallerTypes: []string{"service"},
		},
	}
}

func newAccountStore(path string) (*accountStore, error) {
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
	store := &accountStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *accountStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *accountStore) init() error {
	stmts := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
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
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init account store failed: %w", err)
		}
	}
	return nil
}

func (s *accountStore) createUser(username string, passwordHash string) (string, error) {
	cleanUsername := strings.TrimSpace(username)
	cleanHash := strings.TrimSpace(passwordHash)
	if cleanUsername == "" || cleanHash == "" {
		return "", fmt.Errorf("username and password_hash are required")
	}
	now := time.Now().UnixMilli()
	userID := "usr-" + newID()
	_, err := s.db.Exec(`
		INSERT INTO users(user_id, username, password_hash, created_at_ms, updated_at_ms)
		VALUES(?, ?, ?, ?, ?)
	`, userID, cleanUsername, cleanHash, now, now)
	if err != nil {
		if isUniqueUsernameError(err) {
			return "", errUsernameExists
		}
		return "", err
	}
	return userID, nil
}

func (s *accountStore) getUserByUsername(username string) (userRecord, bool, error) {
	cleanUsername := strings.TrimSpace(username)
	if cleanUsername == "" {
		return userRecord{}, false, nil
	}
	var out userRecord
	err := s.db.QueryRow(`
		SELECT user_id, username, password_hash
		FROM users
		WHERE username=?
	`, cleanUsername).Scan(&out.UserID, &out.Username, &out.PasswordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return userRecord{}, false, nil
		}
		return userRecord{}, false, err
	}
	return out, true, nil
}

func (s *accountStore) getUserByID(userID string) (userRecord, bool, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return userRecord{}, false, nil
	}
	var out userRecord
	err := s.db.QueryRow(`
		SELECT user_id, username, password_hash
		FROM users
		WHERE user_id=?
	`, uid).Scan(&out.UserID, &out.Username, &out.PasswordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return userRecord{}, false, nil
		}
		return userRecord{}, false, err
	}
	return out, true, nil
}

func (s *accountStore) updatePasswordHash(userID string, passwordHash string) error {
	uid := strings.TrimSpace(userID)
	hash := strings.TrimSpace(passwordHash)
	if uid == "" || hash == "" {
		return fmt.Errorf("user_id and password_hash are required")
	}
	_, err := s.db.Exec(`
		UPDATE users
		SET password_hash=?, updated_at_ms=?
		WHERE user_id=?
	`, hash, time.Now().UnixMilli(), uid)
	return err
}

func (s *accountStore) setActiveSID(userID string, sid string) error {
	uid := strings.TrimSpace(userID)
	sessionID := strings.TrimSpace(sid)
	if uid == "" || sessionID == "" {
		return fmt.Errorf("user_id and sid are required")
	}
	_, err := s.db.Exec(`
		INSERT INTO active_sessions(user_id, sid, updated_at_ms)
		VALUES(?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			sid=excluded.sid,
			updated_at_ms=excluded.updated_at_ms
	`, uid, sessionID, time.Now().UnixMilli())
	return err
}

func (s *accountStore) clearActiveSID(userID string) error {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM active_sessions WHERE user_id=?`, uid)
	return err
}

func (s *accountStore) dumpActiveSessions() ([]toolproto.AccountActiveSession, error) {
	rows, err := s.db.Query(`SELECT user_id, sid FROM active_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]toolproto.AccountActiveSession, 0)
	for rows.Next() {
		var userID string
		var sid string
		if err := rows.Scan(&userID, &sid); err != nil {
			return nil, err
		}
		userID = strings.TrimSpace(userID)
		sid = strings.TrimSpace(sid)
		if userID == "" || sid == "" {
			continue
		}
		items = append(items, toolproto.AccountActiveSession{
			UserID: userID,
			SID:    sid,
		})
	}
	return items, nil
}

func (s *accountStore) getOrCreateSigningKey() (signingKey, error) {
	row := s.db.QueryRow(`
		SELECT kid, alg, private_key, public_key, created_at_ms
		FROM signing_keys
		ORDER BY created_at_ms DESC
		LIMIT 1
	`)
	var kid string
	var alg string
	var privateRaw string
	var publicRaw string
	var createdAt int64
	err := row.Scan(&kid, &alg, &privateRaw, &publicRaw, &createdAt)
	if err == nil {
		privateKey, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(privateRaw))
		if decodeErr != nil || len(privateKey) != ed25519.PrivateKeySize {
			return signingKey{}, fmt.Errorf("invalid stored private key")
		}
		return signingKey{
			KID:     strings.TrimSpace(kid),
			Alg:     strings.TrimSpace(alg),
			Public:  strings.TrimSpace(publicRaw),
			Private: ed25519.PrivateKey(privateKey),
			Created: createdAt,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return signingKey{}, err
	}

	publicKey, privateKey, keyErr := ed25519.GenerateKey(rand.Reader)
	if keyErr != nil {
		return signingKey{}, keyErr
	}
	next := signingKey{
		KID:     "kid-" + newID(),
		Alg:     "ed25519",
		Public:  base64.RawURLEncoding.EncodeToString(publicKey),
		Private: privateKey,
		Created: time.Now().UnixMilli(),
	}
	_, insertErr := s.db.Exec(`
		INSERT INTO signing_keys(kid, alg, private_key, public_key, created_at_ms)
		VALUES(?, ?, ?, ?, ?)
	`, next.KID, next.Alg, base64.RawURLEncoding.EncodeToString(privateKey), next.Public, next.Created)
	if insertErr != nil {
		return signingKey{}, insertErr
	}
	return next, nil
}

func (s *accountStore) listPublicKeys() ([]toolproto.AccountPublicKey, error) {
	rows, err := s.db.Query(`
			SELECT kid, alg, public_key
			FROM signing_keys
			ORDER BY created_at_ms DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]toolproto.AccountPublicKey, 0)
	for rows.Next() {
		var item toolproto.AccountPublicKey
		if err := rows.Scan(&item.KID, &item.Alg, &item.PublicKey); err != nil {
			return nil, err
		}
		item.KID = strings.TrimSpace(item.KID)
		item.Alg = strings.TrimSpace(item.Alg)
		item.PublicKey = strings.TrimSpace(item.PublicKey)
		if item.KID == "" || item.PublicKey == "" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func issueToken(key signingKey, userID string, username string, sid string) (string, int64, error) {
	now := time.Now().UnixMilli()
	exp := time.Now().Add(time.Duration(tokenMaxAgeSec) * time.Second).UnixMilli()
	claims := accountClaims{
		UserID:   strings.TrimSpace(userID),
		Username: strings.TrimSpace(username),
		SID:      strings.TrimSpace(sid),
		IatMS:    now,
		ExpMS:    exp,
		KID:      strings.TrimSpace(key.KID),
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", 0, err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	signature := ed25519.Sign(key.Private, []byte(payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(signature)
	return token, exp, nil
}

func isUniqueUsernameError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "users.username")
}

func hashPassword(password string) (string, error) {
	if len(password) < passwordMinLen {
		return "", fmt.Errorf("password too short")
	}
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return "bcrypt$" + string(hashBytes), nil
}

func verifyPassword(password string, stored string) bool {
	clean := strings.TrimSpace(stored)
	if clean == "" {
		return false
	}
	if strings.HasPrefix(clean, "bcrypt$") {
		return bcrypt.CompareHashAndPassword([]byte(strings.TrimPrefix(clean, "bcrypt$")), []byte(password)) == nil
	}
	return false
}

func resolveCaller(r *http.Request, ctx *toolproto.Context) callerIdentity {
	caller := callerIdentity{
		Type:      strings.ToLower(strings.TrimSpace(r.Header.Get("X-Caller-Type"))),
		UserID:    strings.TrimSpace(r.Header.Get("X-Caller-User-Id")),
		ServiceID: strings.TrimSpace(r.Header.Get("X-Caller-Service-Id")),
		SurfaceID: strings.TrimSpace(r.Header.Get("X-Caller-Surface-Id")),
	}
	if caller.Type == "" && ctx != nil {
		caller.Type = strings.ToLower(strings.TrimSpace(ctx.Caller.Type))
		caller.UserID = strings.TrimSpace(ctx.Caller.UserID)
		caller.ServiceID = strings.TrimSpace(ctx.Caller.ServiceID)
		caller.SurfaceID = strings.TrimSpace(ctx.Caller.SurfaceID)
	}
	if caller.Type == "" {
		caller.Type = "anonymous"
	}
	return caller
}

func isHubServiceCaller(caller callerIdentity) bool {
	return strings.EqualFold(strings.TrimSpace(caller.Type), "service") && strings.TrimSpace(caller.ServiceID) == "hub"
}

func isHubOnlyContext(ctx *toolproto.Context) bool {
	if ctx == nil || ctx.Meta == nil {
		return false
	}
	raw, ok := ctx.Meta["hub_only"]
	if !ok {
		return false
	}
	switch tv := raw.(type) {
	case bool:
		return tv
	case string:
		return strings.EqualFold(strings.TrimSpace(tv), "true")
	default:
		return false
	}
}

func asString(v any) string {
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv)
	default:
		return ""
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeToolResponse(w http.ResponseWriter, statusCode int, resp toolproto.CallResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}

func detectAppRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd, fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

func newID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func newSessionID() string {
	return "sid-" + newID()
}

func buildHubHeartbeatURL(registerURL string) string {
	raw := strings.TrimSpace(registerURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/service/heartbeat"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func startHubHeartbeatGuard(heartbeatURL string, sid string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(heartbeatURL) == "" || strings.TrimSpace(sid) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			body := map[string]any{
				"service_id":  strings.TrimSpace(sid),
				"instance_id": strings.TrimSpace(instanceID),
				"status":      "ready",
				"healthy":     true,
				"pid":         pid,
				"endpoint":    strings.TrimSpace(endpoint),
			}
			raw, _ := json.Marshal(body)
			req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(heartbeatURL), bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			hubsvc.ApplyServiceAuthHeaders(req.Header, serviceAuth)
			client := &http.Client{Timeout: 2200 * time.Millisecond}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d", resp.StatusCode)
			}
			return nil
		}
		if err := send(); err != nil {
			onFailure("hub heartbeat failed: " + err.Error())
			return
		}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := send(); err != nil {
				onFailure("hub heartbeat failed: " + err.Error())
				return
			}
		}
	}()
}
