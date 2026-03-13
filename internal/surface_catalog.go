package app

import (
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	SurfaceTypeBuildin = "buildin"
	SurfaceTypeExt     = "ext"
	SurfaceTypeCustom  = "custom"

	SurfaceStatusOK           = "ok"
	SurfaceStatusInvalid      = "invalid"
	SurfaceStatusConflict     = "conflict"
	SurfaceStatusMissingEntry = "missing_entry"
	SurfaceStatusMissing      = "missing"
)

var (
	uuidV4LikePattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
)

type SurfaceManifest struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Version             string         `json:"version"`
	MinSupportedVersion string         `json:"min_supported_version"`
	Entry               string         `json:"entry"`
	Desc                string         `json:"desc,omitempty"`
	Icon                string         `json:"icon,omitempty"`
	Tags                []string       `json:"tags,omitempty"`
	Permissions         map[string]any `json:"permissions,omitempty"`
}

type ScannedSurface struct {
	SurfaceID    string
	SurfaceType  string
	PkgPath      string
	ManifestJSON string
	ManifestHash string
	Status       string
	Error        string
	ScannedAtMS  int64
}

type SurfaceCatalogEntry struct {
	SurfaceID            string         `json:"surface_id"`
	SurfaceType          string         `json:"surface_type"`
	Name                 string         `json:"name"`
	Version              string         `json:"version"`
	MinSupportedVersion  string         `json:"min_supported_version"`
	Entry                string         `json:"entry"`
	EntryURL             string         `json:"entry_url"`
	Desc                 string         `json:"desc,omitempty"`
	Icon                 string         `json:"icon,omitempty"`
	Tags                 []string       `json:"tags,omitempty"`
	Permissions          map[string]any `json:"permissions,omitempty"`
	Status               string         `json:"status"`
	Error                string         `json:"error,omitempty"`
	Enabled              bool           `json:"enabled"`
	Available            bool           `json:"available"`
	ScannedAtMS          int64          `json:"scanned_at_ms"`
	ManifestHash         string         `json:"manifest_hash,omitempty"`
	RawManifest          string         `json:"-"`
	RawPkgPath           string         `json:"-"`
	DefaultEnabledPolicy bool           `json:"-"`
}

type surfaceSemVersion struct {
	Major int
	Minor int
}

func SyncSurfaceCatalog(store *SQLiteStore, surfaceRoot string) error {
	if store == nil {
		return fmt.Errorf("sqlite store is nil")
	}
	scannedAt := nowMS()
	items, err := ScanSurfaceCatalog(surfaceRoot, scannedAt)
	if err != nil {
		return err
	}
	if err := store.SyncScannedSurfaces(items); err != nil {
		return err
	}
	return nil
}

func ScanSurfaceCatalog(surfaceRoot string, scannedAtMS int64) ([]ScannedSurface, error) {
	root := strings.TrimSpace(surfaceRoot)
	if root == "" {
		return nil, fmt.Errorf("surface root is empty")
	}
	out := make([]ScannedSurface, 0, 16)
	for _, surfaceType := range []string{SurfaceTypeBuildin, SurfaceTypeExt, SurfaceTypeCustom} {
		typeRoot := filepath.Join(root, surfaceType)
		entries, err := os.ReadDir(typeRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan %s: %w", typeRoot, err)
		}
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			pkgPath := strings.TrimSpace(ent.Name())
			if pkgPath == "" {
				continue
			}
			item := scanOneSurfacePkg(typeRoot, surfaceType, pkgPath, scannedAtMS)
			out = append(out, item)
		}
	}
	markSurfaceConflicts(out)
	return out, nil
}

func (s *SQLiteStore) SyncScannedSurfaces(items []ScannedSurface) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sync surfaces: %w", err)
	}
	defer tx.Rollback()

	scannedSet := map[string]struct{}{}
	lastScannedAt := nowMS()
	for _, item := range items {
		surfaceID := strings.TrimSpace(item.SurfaceID)
		if surfaceID == "" {
			continue
		}
		scannedSet[surfaceID] = struct{}{}
		lastScannedAt = maxInt64(lastScannedAt, item.ScannedAtMS)
		if _, err := tx.Exec(`
			INSERT INTO surfaces(surface_id, surface_type, pkg_path, manifest_json, manifest_hash, status, error, scanned_at_ms)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(surface_id) DO UPDATE SET
				surface_type = excluded.surface_type,
				pkg_path = excluded.pkg_path,
				manifest_json = excluded.manifest_json,
				manifest_hash = excluded.manifest_hash,
				status = excluded.status,
				error = excluded.error,
				scanned_at_ms = excluded.scanned_at_ms
		`, surfaceID, strings.TrimSpace(item.SurfaceType), strings.TrimSpace(item.PkgPath), strings.TrimSpace(item.ManifestJSON), strings.TrimSpace(item.ManifestHash), strings.TrimSpace(item.Status), strings.TrimSpace(item.Error), item.ScannedAtMS); err != nil {
			return fmt.Errorf("upsert surface %s: %w", surfaceID, err)
		}
	}

	existingRows, err := tx.Query(`SELECT surface_id FROM surfaces`)
	if err != nil {
		return fmt.Errorf("list existing surfaces: %w", err)
	}
	missingIDs := make([]string, 0, 8)
	for existingRows.Next() {
		var surfaceID string
		if err := existingRows.Scan(&surfaceID); err != nil {
			existingRows.Close()
			return fmt.Errorf("scan existing surfaces: %w", err)
		}
		if _, ok := scannedSet[surfaceID]; ok {
			continue
		}
		missingIDs = append(missingIDs, surfaceID)
	}
	existingRows.Close()

	for _, surfaceID := range missingIDs {
		if _, err := tx.Exec(`
			UPDATE surfaces
			SET status=?, error=?, scanned_at_ms=?
			WHERE surface_id=?
		`, SurfaceStatusMissing, "surface package missing on scan", lastScannedAt, surfaceID); err != nil {
			return fmt.Errorf("mark missing surface %s: %w", surfaceID, err)
		}
	}

	users := make([]string, 0, 4)
	userRows, err := tx.Query(`SELECT user_id FROM users`)
	if err != nil {
		return fmt.Errorf("list users for surfaces: %w", err)
	}
	for userRows.Next() {
		var uid string
		if err := userRows.Scan(&uid); err != nil {
			userRows.Close()
			return fmt.Errorf("scan users for surfaces: %w", err)
		}
		uid = strings.TrimSpace(uid)
		if uid != "" {
			users = append(users, uid)
		}
	}
	userRows.Close()
	if len(users) == 0 && strings.TrimSpace(s.userID) != "" {
		users = append(users, strings.TrimSpace(s.userID))
	}
	now := nowMS()
	for _, item := range items {
		surfaceID := strings.TrimSpace(item.SurfaceID)
		if surfaceID == "" {
			continue
		}
		enabledByDefault := defaultSurfaceEnabled(item.SurfaceType, item.Status)
		enabledInt := 0
		if enabledByDefault {
			enabledInt = 1
		}
		for _, uid := range users {
			if _, err := tx.Exec(`
				INSERT OR IGNORE INTO user_surfaces(user_id, surface_id, enabled, updated_at_ms)
				VALUES(?, ?, ?, ?)
			`, uid, surfaceID, enabledInt, now); err != nil {
				return fmt.Errorf("init user_surface %s/%s: %w", uid, surfaceID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sync surfaces: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListSurfacesForUser(userID string) ([]SurfaceCatalogEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	uid := strings.TrimSpace(firstNonEmpty(userID, s.userID))
	if uid == "" {
		return nil, fmt.Errorf("user id is empty")
	}
	rows, err := s.db.Query(`
		SELECT
			s.surface_id,
			s.surface_type,
			s.pkg_path,
			s.manifest_json,
			s.manifest_hash,
			s.status,
			s.error,
			s.scanned_at_ms,
			COALESCE(us.enabled, CASE WHEN s.surface_type=? AND s.status=? THEN 1 ELSE 0 END) AS enabled_defaulted
		FROM surfaces s
		LEFT JOIN user_surfaces us
			ON us.user_id=? AND us.surface_id=s.surface_id
		ORDER BY s.surface_type, s.surface_id
	`, SurfaceTypeBuildin, SurfaceStatusOK, uid)
	if err != nil {
		return nil, fmt.Errorf("query surfaces: %w", err)
	}
	defer rows.Close()

	out := make([]SurfaceCatalogEntry, 0, 16)
	for rows.Next() {
		entry, err := scanSurfaceCatalogRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *SQLiteStore) GetSurfaceForUser(userID string, surfaceID string) (SurfaceCatalogEntry, bool, error) {
	if s == nil || s.db == nil {
		return SurfaceCatalogEntry{}, false, nil
	}
	uid := strings.TrimSpace(firstNonEmpty(userID, s.userID))
	sid := strings.TrimSpace(surfaceID)
	if uid == "" || sid == "" {
		return SurfaceCatalogEntry{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT
			s.surface_id,
			s.surface_type,
			s.pkg_path,
			s.manifest_json,
			s.manifest_hash,
			s.status,
			s.error,
			s.scanned_at_ms,
			COALESCE(us.enabled, CASE WHEN s.surface_type=? AND s.status=? THEN 1 ELSE 0 END) AS enabled_defaulted
		FROM surfaces s
		LEFT JOIN user_surfaces us
			ON us.user_id=? AND us.surface_id=s.surface_id
		WHERE s.surface_id=?
	`, SurfaceTypeBuildin, SurfaceStatusOK, uid, sid)
	entry, err := scanSurfaceCatalogRow(row)
	if err == sql.ErrNoRows {
		return SurfaceCatalogEntry{}, false, nil
	}
	if err != nil {
		return SurfaceCatalogEntry{}, false, err
	}
	return entry, true, nil
}

func (s *SQLiteStore) SetSurfaceEnabled(userID string, surfaceID string, enabled bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	uid := strings.TrimSpace(firstNonEmpty(userID, s.userID))
	sid := strings.TrimSpace(surfaceID)
	if uid == "" || sid == "" {
		return fmt.Errorf("user_id or surface_id is empty")
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	now := nowMS()
	_, err := s.db.Exec(`
		INSERT INTO user_surfaces(user_id, surface_id, enabled, updated_at_ms)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(user_id, surface_id) DO UPDATE SET
			enabled = excluded.enabled,
			updated_at_ms = excluded.updated_at_ms
	`, uid, sid, enabledInt, now)
	if err != nil {
		return fmt.Errorf("set surface enabled failed: %w", err)
	}
	return nil
}

func parseSurfaceVersion(raw string) (surfaceSemVersion, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return surfaceSemVersion{}, fmt.Errorf("version is empty")
	}
	parts := strings.Split(clean, ".")
	if len(parts) > 2 {
		return surfaceSemVersion{}, fmt.Errorf("invalid version: %s", clean)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return surfaceSemVersion{}, fmt.Errorf("invalid major version: %s", clean)
	}
	minor := 0
	if len(parts) == 2 {
		minor, err = strconv.Atoi(parts[1])
		if err != nil || minor < 0 {
			return surfaceSemVersion{}, fmt.Errorf("invalid minor version: %s", clean)
		}
	}
	return surfaceSemVersion{Major: major, Minor: minor}, nil
}

func (v surfaceSemVersion) lessThan(other surfaceSemVersion) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	return v.Minor < other.Minor
}

func isUUIDLike(raw string) bool {
	return uuidV4LikePattern.MatchString(strings.TrimSpace(raw))
}

func markSurfaceConflicts(items []ScannedSurface) {
	conflicts := map[string][]int{}
	for i := range items {
		sid := strings.TrimSpace(items[i].SurfaceID)
		if !isUUIDLike(sid) {
			continue
		}
		conflicts[sid] = append(conflicts[sid], i)
	}
	for sid, indexes := range conflicts {
		if len(indexes) < 2 {
			continue
		}
		errText := "surface_id conflict in scan batch: " + sid
		for _, idx := range indexes {
			items[idx].Status = SurfaceStatusConflict
			items[idx].Error = errText
		}
	}
}

func scanOneSurfacePkg(typeRoot string, surfaceType string, pkgPath string, scannedAtMS int64) ScannedSurface {
	pkgDir := filepath.Join(typeRoot, pkgPath)
	fallbackID := fallbackInvalidSurfaceID(surfaceType, pkgPath)
	result := ScannedSurface{
		SurfaceID:   fallbackID,
		SurfaceType: surfaceType,
		PkgPath:     pkgPath,
		Status:      SurfaceStatusInvalid,
		Error:       "manifest is invalid",
		ScannedAtMS: scannedAtMS,
	}
	manifestPath := filepath.Join(pkgDir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		result.Error = "read manifest failed: " + err.Error()
		result.ManifestJSON = `{}`
		result.ManifestHash = sha256Hex([]byte(result.ManifestJSON))
		return result
	}
	result.ManifestJSON = string(raw)
	result.ManifestHash = sha256Hex(raw)

	var manifest SurfaceManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		result.Error = "parse manifest failed: " + err.Error()
		return result
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	if isUUIDLike(manifest.ID) {
		result.SurfaceID = manifest.ID
	} else if manifest.ID != "" {
		result.Error = "manifest id must be UUID"
		return result
	} else {
		result.Error = "manifest missing id"
		return result
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.MinSupportedVersion = strings.TrimSpace(manifest.MinSupportedVersion)
	manifest.Entry = strings.TrimSpace(manifest.Entry)
	if manifest.Name == "" {
		result.Error = "manifest missing name"
		return result
	}
	ver, err := parseSurfaceVersion(manifest.Version)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	minVer, err := parseSurfaceVersion(manifest.MinSupportedVersion)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if ver.lessThan(minVer) {
		result.Error = "version is lower than min_supported_version"
		return result
	}
	if manifest.Entry == "" {
		result.Error = "manifest missing entry"
		return result
	}
	entryPath, err := secureManifestEntryPath(pkgDir, manifest.Entry)
	if err != nil {
		result.Status = SurfaceStatusMissingEntry
		result.Error = err.Error()
		return result
	}
	fi, err := os.Stat(entryPath)
	if err != nil || fi.IsDir() {
		result.Status = SurfaceStatusMissingEntry
		if err != nil {
			result.Error = "entry not found: " + err.Error()
		} else {
			result.Error = "entry is directory"
		}
		return result
	}

	result.Status = SurfaceStatusOK
	result.Error = ""
	return result
}

func secureManifestEntryPath(pkgDir string, entry string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(entry))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("entry path is empty")
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("entry path must be relative")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry path escapes package root")
	}
	target := filepath.Join(pkgDir, clean)
	rel, err := filepath.Rel(pkgDir, target)
	if err != nil {
		return "", fmt.Errorf("resolve entry path failed: %w", err)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry path escapes package root")
	}
	return target, nil
}

func fallbackInvalidSurfaceID(surfaceType string, pkgPath string) string {
	h := sha1.Sum([]byte(strings.TrimSpace(surfaceType) + "|" + strings.TrimSpace(pkgPath)))
	return "invalid-" + hex.EncodeToString(h[:8])
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func scanSurfaceCatalogRow(row interface {
	Scan(dest ...any) error
}) (SurfaceCatalogEntry, error) {
	var (
		entry           SurfaceCatalogEntry
		pkgPath         string
		manifestJSONRaw string
		enabledInt      int
	)
	if err := row.Scan(
		&entry.SurfaceID,
		&entry.SurfaceType,
		&pkgPath,
		&manifestJSONRaw,
		&entry.ManifestHash,
		&entry.Status,
		&entry.Error,
		&entry.ScannedAtMS,
		&enabledInt,
	); err != nil {
		return SurfaceCatalogEntry{}, err
	}
	entry.RawManifest = strings.TrimSpace(manifestJSONRaw)
	entry.RawPkgPath = strings.TrimSpace(pkgPath)
	entry.Enabled = enabledInt > 0
	entry.DefaultEnabledPolicy = defaultSurfaceEnabled(entry.SurfaceType, entry.Status)
	manifest := SurfaceManifest{}
	_ = json.Unmarshal([]byte(entry.RawManifest), &manifest)
	entry.Name = firstNonEmpty(strings.TrimSpace(manifest.Name), strings.TrimSpace(entry.SurfaceID))
	entry.Version = strings.TrimSpace(manifest.Version)
	entry.MinSupportedVersion = strings.TrimSpace(manifest.MinSupportedVersion)
	entry.Entry = strings.TrimSpace(manifest.Entry)
	entry.EntryURL = buildSurfaceEntryURL(entry.SurfaceType, entry.RawPkgPath, entry.Entry)
	entry.Desc = strings.TrimSpace(manifest.Desc)
	entry.Icon = strings.TrimSpace(manifest.Icon)
	entry.Tags = append([]string(nil), manifest.Tags...)
	entry.Permissions = cloneAnyMap(manifest.Permissions)
	entry.Available = entry.Enabled && strings.EqualFold(entry.Status, SurfaceStatusOK)
	return entry, nil
}

func buildSurfaceEntryURL(surfaceType string, pkgPath string, entry string) string {
	typ := strings.Trim(strings.TrimSpace(surfaceType), "/")
	pkg := strings.Trim(strings.TrimSpace(pkgPath), "/")
	ent := strings.Trim(strings.TrimSpace(entry), "/")
	if typ == "" || pkg == "" || ent == "" {
		return ""
	}
	return "/" + path.Join("surface", typ, pkg, ent)
}

func defaultSurfaceEnabled(surfaceType string, status string) bool {
	if !strings.EqualFold(strings.TrimSpace(status), SurfaceStatusOK) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(surfaceType), SurfaceTypeBuildin)
}

func maxInt64(a int64, b int64) int64 {
	if a >= b {
		return a
	}
	return b
}
