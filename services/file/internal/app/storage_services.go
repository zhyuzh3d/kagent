package app

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"kagent/pkg/sqlitedriver"
)

type StorageScopeTarget struct {
	Scope     string `json:"scope"`
	UserID    string `json:"user_id,omitempty"`
	SurfaceID string `json:"surface_id,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	DBName    string `json:"db_name,omitempty"`
}

type ScopedFileService struct {
	dataRoot string
}

func NewScopedFileService(dataRoot string) (*ScopedFileService, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, fmt.Errorf("data root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve data root: %w", err)
	}
	return &ScopedFileService{dataRoot: absRoot}, nil
}

func (s *ScopedFileService) ReadFile(target StorageScopeTarget, relPath string) ([]byte, error) {
	_, absPath, err := s.resolvePath(target, relPath, false)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(absPath)
}

func (s *ScopedFileService) WriteFile(target StorageScopeTarget, relPath string, data []byte) (int64, error) {
	_, absPath, err := s.resolvePath(target, relPath, true)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return 0, fmt.Errorf("mkdir parent: %w", err)
	}
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

func (s *ScopedFileService) Exists(target StorageScopeTarget, relPath string) (bool, error) {
	_, absPath, err := s.resolvePath(target, relPath, false)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(absPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *ScopedFileService) Stat(target StorageScopeTarget, relPath string) (os.FileInfo, error) {
	_, absPath, err := s.resolvePath(target, relPath, false)
	if err != nil {
		return nil, err
	}
	return os.Stat(absPath)
}

func (s *ScopedFileService) List(target StorageScopeTarget, relPath string) ([]SurfaceFSListEntry, error) {
	_, absPath, err := s.resolvePath(target, relPath, true)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absPath, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}
	out := make([]SurfaceFSListEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SurfaceFSListEntry{
			Name:        e.Name(),
			IsDir:       e.IsDir(),
			SizeBytes:   info.Size(),
			UpdatedAtMS: info.ModTime().UnixMilli(),
		})
	}
	return out, nil
}

func (s *ScopedFileService) Mkdir(target StorageScopeTarget, relPath string) error {
	_, absPath, err := s.resolvePath(target, relPath, true)
	if err != nil {
		return err
	}
	return os.MkdirAll(absPath, 0o755)
}

func (s *ScopedFileService) Delete(target StorageScopeTarget, relPath string, recursive bool) error {
	_, absPath, err := s.resolvePath(target, relPath, false)
	if err != nil {
		return err
	}
	fi, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if !recursive {
			return fmt.Errorf("recursive=true required for directory delete")
		}
		return os.RemoveAll(absPath)
	}
	return os.Remove(absPath)
}

func (s *ScopedFileService) Rename(target StorageScopeTarget, oldRelPath string, newRelPath string) error {
	_, oldPath, err := s.resolvePath(target, oldRelPath, false)
	if err != nil {
		return err
	}
	_, newPath, err := s.resolvePath(target, newRelPath, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}

func (s *ScopedFileService) Copy(target StorageScopeTarget, srcRelPath string, dstRelPath string) (int64, error) {
	_, srcPath, err := s.resolvePath(target, srcRelPath, false)
	if err != nil {
		return 0, err
	}
	_, dstPath, err := s.resolvePath(target, dstRelPath, true)
	if err != nil {
		return 0, err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	dst, err := os.Create(dstPath)
	if err != nil {
		return 0, err
	}
	defer dst.Close()
	return io.Copy(dst, src)
}

func (s *ScopedFileService) resolvePath(target StorageScopeTarget, relPath string, ensureRoot bool) (string, string, error) {
	root, err := resolveStorageScopeRoot(s.dataRoot, target)
	if err != nil {
		return "", "", err
	}
	if ensureRoot {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", "", fmt.Errorf("ensure scope root: %w", err)
		}
	}
	cleanRel, err := normalizeRelativePath(relPath)
	if err != nil {
		return "", "", err
	}
	absPath := filepath.Join(root, cleanRel)
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return "", "", err
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes scope root")
	}
	return root, absPath, nil
}

func resolveStorageScopeRoot(dataRoot string, target StorageScopeTarget) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(target.Scope))
	switch scope {
	case "user":
		userID := strings.TrimSpace(target.UserID)
		if userID == "" {
			return "", fmt.Errorf("user scope requires user_id")
		}
		return filepath.Join(dataRoot, "user", userID), nil
	case "surface":
		userID := strings.TrimSpace(target.UserID)
		surfaceID := strings.TrimSpace(target.SurfaceID)
		if userID == "" || surfaceID == "" {
			return "", fmt.Errorf("surface scope requires user_id and surface_id")
		}
		return filepath.Join(dataRoot, "user", userID, "surface", surfaceID), nil
	case "service":
		serviceID := strings.TrimSpace(target.ServiceID)
		if serviceID == "" {
			return "", fmt.Errorf("service scope requires service_id")
		}
		return filepath.Join(dataRoot, "service", serviceID), nil
	default:
		return "", fmt.Errorf("unsupported scope: %s", target.Scope)
	}
}

type ScopedDatabaseService struct {
	dataRoot string
}

func NewScopedDatabaseService(dataRoot string) (*ScopedDatabaseService, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, fmt.Errorf("data root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve data root: %w", err)
	}
	return &ScopedDatabaseService{dataRoot: absRoot}, nil
}

func (s *ScopedDatabaseService) Query(target StorageScopeTarget, query string, args []any) ([]map[string]any, error) {
	db, err := s.openDB(target)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(strings.TrimSpace(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, 16)
	for rows.Next() {
		values := make([]any, len(cols))
		scans := make([]any, len(cols))
		for i := range values {
			scans[i] = &values[i]
		}
		if err := rows.Scan(scans...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, c := range cols {
			item[c] = normalizeDBValue(values[i])
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *ScopedDatabaseService) Execute(target StorageScopeTarget, query string, args []any) (int64, int64, error) {
	db, err := s.openDB(target)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	res, err := db.Exec(strings.TrimSpace(query), args...)
	if err != nil {
		return 0, 0, err
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return affected, lastID, nil
}

func (s *ScopedDatabaseService) Schema(target StorageScopeTarget) ([]map[string]any, error) {
	return s.Query(target, `
		SELECT type, name, tbl_name, sql
		FROM sqlite_master
		WHERE type IN ('table', 'index', 'view')
		ORDER BY type, name
	`, nil)
}

func (s *ScopedDatabaseService) openDB(target StorageScopeTarget) (*sql.DB, error) {
	root, err := resolveStorageScopeRoot(s.dataRoot, target)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	dbName := strings.TrimSpace(target.DBName)
	if dbName == "" {
		dbName = "kagent.db"
	}
	if !strings.HasSuffix(strings.ToLower(dbName), ".db") {
		dbName += ".db"
	}
	cleanName := filepath.Base(dbName)
	if cleanName == "." || cleanName == "" || cleanName == ".." {
		return nil, fmt.Errorf("invalid db_name")
	}
	dbPath := filepath.Join(root, cleanName)
	db, err := sqlitedriver.Open(dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func normalizeDBValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return t
	}
}
