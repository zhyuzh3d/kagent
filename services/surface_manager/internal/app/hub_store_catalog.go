package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func (s *HubStore) SyncScannedSurfaces(ctx context.Context, items []ScannedSurface) error {
	existing, err := s.readCatalogEntries(ctx)
	if err != nil {
		return err
	}
	existingByID := map[string]SurfaceCatalogEntry{}
	for _, item := range existing {
		existingByID[strings.TrimSpace(item.SurfaceID)] = item
	}

	scannedIDs := map[string]struct{}{}
	lastScannedAt := nowMS()
	for _, item := range items {
		surfaceID := strings.TrimSpace(item.SurfaceID)
		if surfaceID == "" {
			continue
		}
		scannedIDs[surfaceID] = struct{}{}
		lastScannedAt = maxInt64(lastScannedAt, item.ScannedAtMS)
		if err := s.writeCatalogEntry(ctx, buildCatalogEntry(item)); err != nil {
			return err
		}
		if err := s.appendLog(ctx, item.SurfaceID, TypeSurfaceChange, item.Status, map[string]any{
			"surface_id":   item.SurfaceID,
			"surface_type": item.SurfaceType,
			"pkg_path":     item.PkgPath,
			"status":       item.Status,
			"error":        item.Error,
		}); err != nil {
			return err
		}
	}

	for surfaceID, item := range existingByID {
		if _, ok := scannedIDs[surfaceID]; ok {
			continue
		}
		item.Status = SurfaceStatusMissing
		item.Error = "surface package missing on scan"
		item.ScannedAtMS = lastScannedAt
		if err := s.writeCatalogEntry(ctx, item); err != nil {
			return err
		}
		if err := s.appendLog(ctx, surfaceID, TypeWarningEvent, item.Status, map[string]any{
			"surface_id": surfaceID,
			"status":     item.Status,
			"error":      item.Error,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *HubStore) readCatalogEntries(ctx context.Context) ([]SurfaceCatalogEntry, error) {
	rows, err := s.shareRead(ctx, map[string]any{
		"namespace":  surfaceCatalogNamespace,
		"category":   surfaceCatalogCategory,
		"service_id": s.serviceID,
		"limit":      1000,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SurfaceCatalogEntry, 0, len(rows))
	for _, row := range rows {
		entry, ok, err := decodeCatalogEntryRow(row)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *HubStore) writeCatalogEntry(ctx context.Context, entry SurfaceCatalogEntry) error {
	raw, err := json.Marshal(map[string]any{
		"surface_id":     entry.SurfaceID,
		"surface_type":   entry.SurfaceType,
		"pkg_path":       entry.RawPkgPath,
		"manifest_json":  entry.RawManifest,
		"manifest_hash":  entry.ManifestHash,
		"status":         entry.Status,
		"error":          entry.Error,
		"scanned_at_ms":  entry.ScannedAtMS,
		"default_enable": entry.DefaultEnabledPolicy,
	})
	if err != nil {
		return err
	}
	_, err = s.shareWrite(ctx, map[string]any{
		"namespace":  surfaceCatalogNamespace,
		"category":   surfaceCatalogCategory,
		"key":        strings.TrimSpace(entry.SurfaceID),
		"value_json": string(raw),
		"visibility": "public",
	})
	return err
}

func (s *HubStore) CleanupDuplicateCatalogEntries(ctx context.Context) ([]string, error) {
	items, err := s.readCatalogEntries(ctx)
	if err != nil {
		return nil, err
	}
	grouped := map[string][]SurfaceCatalogEntry{}
	for _, item := range items {
		pkgKey := strings.TrimSpace(item.SurfaceType) + "|" + strings.TrimSpace(item.RawPkgPath)
		if strings.TrimSpace(item.RawPkgPath) == "" {
			continue
		}
		grouped[pkgKey] = append(grouped[pkgKey], item)
	}
	deleted := make([]string, 0, 8)
	for _, group := range grouped {
		if len(group) < 2 {
			continue
		}
		keepIdx := pickCanonicalCatalogEntry(group)
		for idx, item := range group {
			surfaceID := strings.TrimSpace(item.SurfaceID)
			if surfaceID == "" || idx == keepIdx {
				continue
			}
			if err := s.deleteCatalogEntry(ctx, surfaceID); err != nil {
				return deleted, err
			}
			deleted = append(deleted, surfaceID)
		}
	}
	if err := s.deleteUserSurfaceSettings(ctx, deleted); err != nil {
		return deleted, err
	}
	sort.Strings(deleted)
	return deleted, nil
}

func (s *HubStore) deleteCatalogEntry(ctx context.Context, surfaceID string) error {
	if strings.TrimSpace(surfaceID) == "" {
		return nil
	}
	_, err := s.shareDelete(ctx, map[string]any{
		"namespace": surfaceCatalogNamespace,
		"category":  surfaceCatalogCategory,
		"key":       strings.TrimSpace(surfaceID),
	})
	return err
}

func (s *HubStore) deleteUserSurfaceSettings(ctx context.Context, surfaceIDs []string) error {
	if len(surfaceIDs) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(surfaceIDs))
	args := make([]any, 0, len(surfaceIDs))
	for _, surfaceID := range surfaceIDs {
		sid := strings.TrimSpace(surfaceID)
		if sid == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, sid)
	}
	if len(placeholders) == 0 {
		return nil
	}
	_, err := s.databaseExecute(ctx, `
		DELETE FROM user_surfaces
		WHERE surface_id IN (`+strings.Join(placeholders, ",")+`)
	`, args)
	return err
}

func pickCanonicalCatalogEntry(items []SurfaceCatalogEntry) int {
	bestIdx := 0
	for i := 1; i < len(items); i++ {
		if preferCatalogEntry(items[i], items[bestIdx]) {
			bestIdx = i
		}
	}
	return bestIdx
}

func preferCatalogEntry(left SurfaceCatalogEntry, right SurfaceCatalogEntry) bool {
	leftStatus := strings.TrimSpace(left.Status)
	rightStatus := strings.TrimSpace(right.Status)
	if leftStatus == SurfaceStatusOK && rightStatus != SurfaceStatusOK {
		return true
	}
	if rightStatus == SurfaceStatusOK && leftStatus != SurfaceStatusOK {
		return false
	}
	if leftStatus != SurfaceStatusMissing && rightStatus == SurfaceStatusMissing {
		return true
	}
	if rightStatus != SurfaceStatusMissing && leftStatus == SurfaceStatusMissing {
		return false
	}
	leftID := strings.TrimSpace(left.SurfaceID)
	rightID := strings.TrimSpace(right.SurfaceID)
	if isUUIDLike(leftID) && !isUUIDLike(rightID) {
		return true
	}
	if isUUIDLike(rightID) && !isUUIDLike(leftID) {
		return false
	}
	if !strings.HasPrefix(leftID, "invalid-") && strings.HasPrefix(rightID, "invalid-") {
		return true
	}
	if !strings.HasPrefix(rightID, "invalid-") && strings.HasPrefix(leftID, "invalid-") {
		return false
	}
	if left.ScannedAtMS != right.ScannedAtMS {
		return left.ScannedAtMS > right.ScannedAtMS
	}
	return leftID < rightID
}

func buildCatalogEntry(item ScannedSurface) SurfaceCatalogEntry {
	entry := SurfaceCatalogEntry{
		SurfaceID:            strings.TrimSpace(item.SurfaceID),
		SurfaceType:          strings.TrimSpace(item.SurfaceType),
		Status:               strings.TrimSpace(item.Status),
		Error:                strings.TrimSpace(item.Error),
		ScannedAtMS:          item.ScannedAtMS,
		ManifestHash:         strings.TrimSpace(item.ManifestHash),
		RawManifest:          strings.TrimSpace(item.ManifestJSON),
		RawPkgPath:           strings.TrimSpace(item.PkgPath),
		DefaultEnabledPolicy: defaultSurfaceEnabled(item.SurfaceType, item.Status),
	}
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
	entry.Enabled = entry.DefaultEnabledPolicy
	entry.Available = entry.Enabled && strings.EqualFold(entry.Status, SurfaceStatusOK)
	return entry
}

func decodeCatalogEntryRow(row map[string]any) (SurfaceCatalogEntry, bool, error) {
	valueJSON := strings.TrimSpace(asMapString(row, "value_json"))
	if valueJSON == "" {
		return SurfaceCatalogEntry{}, false, nil
	}
	var payload struct {
		SurfaceID     string `json:"surface_id"`
		SurfaceType   string `json:"surface_type"`
		PkgPath       string `json:"pkg_path"`
		ManifestJSON  string `json:"manifest_json"`
		ManifestHash  string `json:"manifest_hash"`
		Status        string `json:"status"`
		Error         string `json:"error"`
		ScannedAtMS   int64  `json:"scanned_at_ms"`
		DefaultEnable bool   `json:"default_enable"`
	}
	if err := json.Unmarshal([]byte(valueJSON), &payload); err != nil {
		return SurfaceCatalogEntry{}, false, fmt.Errorf("decode surface catalog record failed: %w", err)
	}
	if strings.TrimSpace(payload.SurfaceID) == "" {
		return SurfaceCatalogEntry{}, false, nil
	}
	return buildCatalogEntry(ScannedSurface{
		SurfaceID:    payload.SurfaceID,
		SurfaceType:  payload.SurfaceType,
		PkgPath:      payload.PkgPath,
		ManifestJSON: payload.ManifestJSON,
		ManifestHash: payload.ManifestHash,
		Status:       payload.Status,
		Error:        payload.Error,
		ScannedAtMS:  payload.ScannedAtMS,
	}), true, nil
}
