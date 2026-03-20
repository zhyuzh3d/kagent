package app

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type UserStore struct {
	db *sql.DB
}

var ErrUsernameAlreadyExists = errors.New("username already exists")

func NewUserStore(path string) (*UserStore, error) {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}
	db, err := OpenSQLite(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &UserStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *UserStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *UserStore) init() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("user store is not initialized")
	}
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
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init user store failed: %w", err)
		}
	}
	return nil
}

func (s *UserStore) CreateUser(username string, passwordHash string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("user store is not initialized")
	}
	cleanUsername := strings.TrimSpace(username)
	cleanHash := strings.TrimSpace(passwordHash)
	if cleanUsername == "" || cleanHash == "" {
		return "", fmt.Errorf("username and password_hash are required")
	}
	now := time.Now().UnixMilli()
	userID := "usr-" + newRequestID()
	_, err := s.db.Exec(`
		INSERT INTO users(user_id, username, password_hash, created_at_ms, updated_at_ms)
		VALUES(?, ?, ?, ?, ?)
	`, userID, cleanUsername, cleanHash, now, now)
	if err != nil {
		if isUsernameUniqueConstraintErr(err) {
			return "", fmt.Errorf("%w: %v", ErrUsernameAlreadyExists, err)
		}
		return "", err
	}
	return userID, nil
}

func IsUsernameAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUsernameAlreadyExists) {
		return true
	}
	return isUsernameUniqueConstraintErr(err)
}

func isUsernameUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "users.username")
}

func (s *UserStore) GetUserByUsername(username string) (userID string, passwordHash string, exists bool, err error) {
	if s == nil || s.db == nil {
		return "", "", false, fmt.Errorf("user store is not initialized")
	}
	cleanUsername := strings.TrimSpace(username)
	if cleanUsername == "" {
		return "", "", false, nil
	}
	err = s.db.QueryRow(`
		SELECT user_id, password_hash
		FROM users
		WHERE username=?
	`, cleanUsername).Scan(&userID, &passwordHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return strings.TrimSpace(userID), strings.TrimSpace(passwordHash), true, nil
}

func (s *UserStore) GetUserByID(userID string) (username string, exists bool, err error) {
	if s == nil || s.db == nil {
		return "", false, fmt.Errorf("user store is not initialized")
	}
	cleanUserID := strings.TrimSpace(userID)
	if cleanUserID == "" {
		return "", false, nil
	}
	err = s.db.QueryRow(`
		SELECT username
		FROM users
		WHERE user_id=?
	`, cleanUserID).Scan(&username)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(username), true, nil
}

func (s *UserStore) UpdateUserPasswordHash(userID string, passwordHash string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("user store is not initialized")
	}
	cleanUserID := strings.TrimSpace(userID)
	cleanHash := strings.TrimSpace(passwordHash)
	if cleanUserID == "" || cleanHash == "" {
		return fmt.Errorf("user_id and password_hash are required")
	}
	_, err := s.db.Exec(`
		UPDATE users
		SET password_hash=?, updated_at_ms=?
		WHERE user_id=?
	`, cleanHash, time.Now().UnixMilli(), cleanUserID)
	return err
}
