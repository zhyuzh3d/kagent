package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func (s *HubStore) ListSurfacesForUser(ctx context.Context, userID string) ([]SurfaceCatalogEntry, error) {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return nil, fmt.Errorf("user id is empty")
	}
	entries, err := s.readCatalogEntries(ctx)
	if err != nil {
		return nil, err
	}
	settings, err := s.loadUserSurfaceSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		enabled, ok := settings[entries[i].SurfaceID]
		if !ok {
			enabled = entries[i].DefaultEnabledPolicy
		}
		entries[i].Enabled = enabled
		entries[i].Available = enabled && strings.EqualFold(strings.TrimSpace(entries[i].Status), SurfaceStatusOK)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SurfaceType != entries[j].SurfaceType {
			return entries[i].SurfaceType < entries[j].SurfaceType
		}
		return entries[i].SurfaceID < entries[j].SurfaceID
	})
	return entries, nil
}

func (s *HubStore) GetSurfaceForUser(ctx context.Context, userID string, surfaceID string) (SurfaceCatalogEntry, bool, error) {
	items, err := s.ListSurfacesForUser(ctx, userID)
	if err != nil {
		return SurfaceCatalogEntry{}, false, err
	}
	sid := strings.TrimSpace(surfaceID)
	for _, item := range items {
		if strings.TrimSpace(item.SurfaceID) == sid {
			return item, true, nil
		}
	}
	return SurfaceCatalogEntry{}, false, nil
}

func (s *HubStore) SetSurfaceEnabled(ctx context.Context, userID string, surfaceID string, enabled bool) error {
	uid := strings.TrimSpace(userID)
	sid := strings.TrimSpace(surfaceID)
	if uid == "" || sid == "" {
		return fmt.Errorf("user_id or surface_id is empty")
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := s.databaseExecute(ctx, `
		INSERT INTO user_surfaces(user_id, surface_id, enabled, updated_at_ms)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(user_id, surface_id) DO UPDATE SET
			enabled = excluded.enabled,
			updated_at_ms = excluded.updated_at_ms
	`, []any{uid, sid, enabledInt, nowMS()})
	return err
}

func (s *HubStore) loadUserSurfaceSettings(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.databaseQuery(ctx, `
		SELECT surface_id, enabled
		FROM user_surfaces
		WHERE user_id=?
	`, []any{strings.TrimSpace(userID)})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, row := range rows {
		surfaceID := strings.TrimSpace(asMapString(row, "surface_id"))
		if surfaceID == "" {
			continue
		}
		out[surfaceID] = asMapInt64(row, "enabled") > 0
	}
	return out, nil
}
