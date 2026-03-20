package app

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
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
	PkgDir       string
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

type SurfaceCatalogSyncer interface {
	SyncScannedSurfaces(context.Context, []ScannedSurface) error
}

func SyncSurfaceCatalog(ctx context.Context, store SurfaceCatalogSyncer, surfaceRoot string) error {
	if store == nil {
		return fmt.Errorf("surface store is nil")
	}
	scannedAt := nowMS()
	items, err := ScanSurfaceCatalog(surfaceRoot, scannedAt)
	if err != nil {
		return err
	}
	return store.SyncScannedSurfaces(ctx, items)
}

func ScanSurfaceCatalog(surfaceRoot string, scannedAtMS int64) ([]ScannedSurface, error) {
	root := strings.TrimSpace(surfaceRoot)
	if root == "" {
		return nil, fmt.Errorf("surface root is empty")
	}
	out := make([]ScannedSurface, 0, 16)
	for _, surfaceType := range []string{SurfaceTypeBuildin, SurfaceTypeExt, SurfaceTypeCustom} {
		typeRoot := filepath.Join(root, surfaceType)
		if surfaceType == SurfaceTypeCustom {
			items, err := scanSurfacePackagesRecursive(typeRoot, surfaceType, root, scannedAtMS)
			if err != nil {
				return nil, err
			}
			out = append(out, items...)
			continue
		}
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
	userRoots, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, ent := range userRoots {
		if !ent.IsDir() {
			continue
		}
		name := strings.TrimSpace(ent.Name())
		if name == "" || name == SurfaceTypeBuildin || name == SurfaceTypeExt || name == SurfaceTypeCustom {
			continue
		}
		customRoot := filepath.Join(root, name, SurfaceTypeCustom)
		items, err := scanSurfacePackagesRecursive(customRoot, SurfaceTypeCustom, root, scannedAtMS)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		out = append(out, items...)
	}
	markSurfaceConflicts(out)
	return out, nil
}

func scanSurfacePackagesRecursive(scanRoot string, surfaceType string, surfaceRoot string, scannedAtMS int64) ([]ScannedSurface, error) {
	entries := make([]ScannedSurface, 0, 8)
	if _, err := os.Stat(scanRoot); err != nil {
		return nil, err
	}
	err := filepath.WalkDir(scanRoot, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		manifestPath := filepath.Join(current, "manifest.json")
		if _, err := os.Stat(manifestPath); err != nil {
			return nil
		}
		rel, err := filepath.Rel(surfaceRoot, current)
		if err != nil {
			return err
		}
		pkgPath := filepath.ToSlash(strings.TrimSpace(rel))
		if strings.HasPrefix(pkgPath, SurfaceTypeCustom+"/") {
			pkgPath = strings.TrimPrefix(pkgPath, SurfaceTypeCustom+"/")
		}
		item := scanOneSurfacePkg(current, surfaceType, pkgPath, scannedAtMS)
		item.PkgDir = current
		entries = append(entries, item)
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
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
	pkgDir := typeRoot
	if strings.TrimSpace(pkgPath) != "" {
		pkgDir = filepath.Join(typeRoot, filepath.FromSlash(pkgPath))
	}
	fallbackID := fallbackInvalidSurfaceID(surfaceType, pkgPath)
	result := ScannedSurface{
		SurfaceID:   fallbackID,
		SurfaceType: surfaceType,
		PkgPath:     pkgPath,
		PkgDir:      pkgDir,
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

func buildSurfaceEntryURL(surfaceType string, pkgPath string, entry string) string {
	typ := strings.Trim(strings.TrimSpace(surfaceType), "/")
	pkg := strings.Trim(strings.TrimSpace(pkgPath), "/")
	ent := strings.Trim(strings.TrimSpace(entry), "/")
	if typ == "" || pkg == "" || ent == "" {
		return ""
	}
	if typ == SurfaceTypeCustom && strings.Contains(pkg, "/custom/") {
		return "/" + path.Join("surface", pkg, ent)
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
