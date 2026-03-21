package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type StartupSnapshotStore struct {
	db *sql.DB
}

type StartupSnapshotRecord struct {
	SnapshotID  string          `json:"snapshot_id"`
	HubVersion  string          `json:"hub_version"`
	PayloadJSON json.RawMessage `json:"payload_json"`
	CreatedAtMS int64           `json:"created_at_ms"`
}

func NewStartupSnapshotStore(path string) (*StartupSnapshotStore, error) {
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
	store := &StartupSnapshotStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *StartupSnapshotStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *StartupSnapshotStore) Save(hubVersion string, payload any) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("startup snapshot store is not initialized")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal startup snapshot: %w", err)
	}
	now := time.Now().UnixMilli()
	snapshotID := "hss-" + NewRequestID()
	_, err = s.db.Exec(`
		INSERT INTO hub_startup_snapshots(snapshot_id, hub_version, payload_json, created_at_ms)
		VALUES(?, ?, ?, ?)
	`, snapshotID, strings.TrimSpace(hubVersion), string(raw), now)
	if err != nil {
		return fmt.Errorf("insert startup snapshot: %w", err)
	}
	return nil
}

func (s *StartupSnapshotStore) LoadLatest() (StartupSnapshotRecord, bool, error) {
	if s == nil || s.db == nil {
		return StartupSnapshotRecord{}, false, fmt.Errorf("startup snapshot store is not initialized")
	}
	row := s.db.QueryRow(`
		SELECT snapshot_id, hub_version, payload_json, created_at_ms
		FROM hub_startup_snapshots
		ORDER BY created_at_ms DESC
		LIMIT 1
	`)
	var record StartupSnapshotRecord
	var payload string
	if err := row.Scan(&record.SnapshotID, &record.HubVersion, &payload, &record.CreatedAtMS); err != nil {
		if err == sql.ErrNoRows {
			return StartupSnapshotRecord{}, false, nil
		}
		return StartupSnapshotRecord{}, false, fmt.Errorf("query latest startup snapshot: %w", err)
	}
	record.PayloadJSON = json.RawMessage(payload)
	return record, true, nil
}

func (s *StartupSnapshotStore) init() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("startup snapshot store is not initialized")
	}
	stmts := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		`CREATE TABLE IF NOT EXISTS hub_startup_snapshots (
			snapshot_id TEXT PRIMARY KEY,
			hub_version TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hub_startup_snapshots_created_at ON hub_startup_snapshots(created_at_ms DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("init startup snapshot store failed: %w", err)
		}
	}
	return nil
}
