package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

const (
	surfaceCatalogNamespace = "surface_manager"
	surfaceCatalogCategory  = "surface_catalog"
	surfaceDBName           = "surface_manager.db"
)

type SurfaceStore interface {
	Close() error
	EnsureSchema(context.Context) error
	SyncScannedSurfaces(context.Context, []ScannedSurface) error
	ListSurfacesForUser(context.Context, string) ([]SurfaceCatalogEntry, error)
	GetSurfaceForUser(context.Context, string, string) (SurfaceCatalogEntry, bool, error)
	SetSurfaceEnabled(context.Context, string, string, bool) error
	LoadRecentSurfaceMessages(context.Context, string, int) ([]ChatMessage, error)
}

type HubStore struct {
	toolCallURL string
	serviceAuth hubsvc.BootstrapSecret
	serviceID   string
	httpClient  *http.Client
}

func NewHubStore(toolCallURL string, serviceAuth hubsvc.BootstrapSecret, serviceID string, timeout time.Duration) *HubStore {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &HubStore{
		toolCallURL: strings.TrimSpace(toolCallURL),
		serviceAuth: serviceAuth,
		serviceID:   strings.TrimSpace(serviceID),
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func (s *HubStore) Close() error {
	return nil
}

func (s *HubStore) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_surfaces (
			user_id TEXT NOT NULL,
			surface_id TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			PRIMARY KEY(user_id, surface_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_surface_user_surfaces_user ON user_surfaces(user_id, updated_at_ms DESC, surface_id)`,
		`CREATE TABLE IF NOT EXISTS surface_logs (
			log_id TEXT PRIMARY KEY,
			surface_id TEXT NOT NULL,
			category TEXT NOT NULL,
			message_type TEXT NOT NULL,
			content TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL,
			created_at_iso TEXT NOT NULL,
			created_at_local_ymdhms TEXT NOT NULL,
			created_at_local_weekday TEXT NOT NULL,
			created_at_local_lunar TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_surface_logs_surface_time ON surface_logs(surface_id, created_at_ms DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.databaseExecute(ctx, stmt, nil); err != nil {
			return err
		}
	}
	return nil
}

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

func (s *HubStore) LoadRecentSurfaceMessages(ctx context.Context, surfaceID string, limit int) ([]ChatMessage, error) {
	sid := strings.TrimSpace(surfaceID)
	if sid == "" {
		return nil, fmt.Errorf("surface_id is empty")
	}
	if limit <= 0 {
		limit = 80
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.databaseQuery(ctx, `
		SELECT
			log_id,
			surface_id,
			category,
			message_type,
			content,
			payload_json,
			created_at_ms,
			created_at_iso,
			created_at_local_ymdhms,
			created_at_local_weekday,
			created_at_local_lunar
		FROM surface_logs
		WHERE surface_id=?
		ORDER BY created_at_ms DESC
		LIMIT ?
	`, []any{sid, limit})
	if err != nil {
		return nil, err
	}
	out := make([]ChatMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, ChatMessage{
			MessageID:             strings.TrimSpace(asMapString(row, "log_id")),
			Role:                  RoleObserver,
			Category:              strings.TrimSpace(asMapString(row, "category")),
			MessageType:           strings.TrimSpace(asMapString(row, "message_type")),
			Content:               strings.TrimSpace(asMapString(row, "content")),
			PayloadJSON:           firstNonEmpty(strings.TrimSpace(asMapString(row, "payload_json")), "{}"),
			CreatedAtMS:           asMapInt64(row, "created_at_ms"),
			CreatedAtISO:          strings.TrimSpace(asMapString(row, "created_at_iso")),
			CreatedAtLocalYMDHMS:  strings.TrimSpace(asMapString(row, "created_at_local_ymdhms")),
			CreatedAtLocalWeekday: strings.TrimSpace(asMapString(row, "created_at_local_weekday")),
			CreatedAtLocalLunar:   strings.TrimSpace(asMapString(row, "created_at_local_lunar")),
		})
	}
	return out, nil
}

func (s *HubStore) appendLog(ctx context.Context, surfaceID string, messageType string, content string, payload map[string]any) error {
	now := nowMS()
	timeFields := buildSemanticTimeFields(now)
	rawPayload, _ := json.Marshal(nonNilMap(payload))
	_, err := s.databaseExecute(ctx, `
		INSERT INTO surface_logs(
			log_id, surface_id, category, message_type, content, payload_json,
			created_at_ms, created_at_iso, created_at_local_ymdhms, created_at_local_weekday, created_at_local_lunar
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, []any{
		"slog-" + newRequestID(),
		strings.TrimSpace(surfaceID),
		CategorySurface,
		firstNonEmpty(strings.TrimSpace(messageType), TypeSurfaceChange),
		firstNonEmpty(strings.TrimSpace(content), "surface event"),
		string(rawPayload),
		now,
		timeFields.ISO,
		timeFields.LocalYMDHMS,
		timeFields.LocalWeekday,
		timeFields.LocalLunar,
	})
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

func (s *HubStore) databaseQuery(ctx context.Context, query string, args []any) ([]map[string]any, error) {
	result, err := s.callTool(ctx, "storage.database.query", map[string]any{
		"db_name": surfaceDBName,
		"query":   strings.TrimSpace(query),
		"args":    args,
	})
	if err != nil {
		return nil, err
	}
	rawRows, ok := result["rows"]
	if !ok || rawRows == nil {
		return []map[string]any{}, nil
	}
	items, ok := rawRows.([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *HubStore) databaseExecute(ctx context.Context, query string, args []any) (map[string]any, error) {
	return s.callTool(ctx, "storage.database.execute", map[string]any{
		"db_name": surfaceDBName,
		"query":   strings.TrimSpace(query),
		"args":    args,
	})
}

func (s *HubStore) shareWrite(ctx context.Context, args map[string]any) (map[string]any, error) {
	return s.callTool(ctx, "storage.share.write", args)
}

func (s *HubStore) shareRead(ctx context.Context, args map[string]any) ([]map[string]any, error) {
	result, err := s.callTool(ctx, "storage.share.read", args)
	if err != nil {
		return nil, err
	}
	rawItems, ok := result["items"]
	if !ok || rawItems == nil {
		return []map[string]any{}, nil
	}
	items, ok := rawItems.([]any)
	if !ok {
		return []map[string]any{}, nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *HubStore) callTool(ctx context.Context, toolID string, args map[string]any) (map[string]any, error) {
	if s == nil || s.toolCallURL == "" {
		return nil, fmt.Errorf("hub store is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callReq := toolproto.CallRequest{
		ToolID: strings.TrimSpace(toolID),
		Args:   args,
		Context: &toolproto.Context{
			RequestID: "req_" + newRequestID(),
			TraceID:   "tr_" + newRequestID(),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: strings.TrimSpace(s.serviceID),
			},
		},
	}
	hubsvc.AttachDelegationFromContext(callReq.Context, ctx)
	rawReq, err := json.Marshal(callReq)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.toolCallURL, bytes.NewReader(rawReq))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(httpReq.Header, s.serviceAuth)
	httpResp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	var out toolproto.CallResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tool response: %w", err)
	}
	if !out.Ok {
		if out.Error == nil {
			return nil, fmt.Errorf("tool call failed")
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(out.Error.Message))
	}
	if out.Result == nil {
		return map[string]any{}, nil
	}
	if result, ok := out.Result.(map[string]any); ok {
		return result, nil
	}
	rawResult, _ := json.Marshal(out.Result)
	result := map[string]any{}
	_ = json.Unmarshal(rawResult, &result)
	return result, nil
}

func asMapString(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	switch t := row[key].(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		if t == nil {
			return ""
		}
		return fmt.Sprintf("%v", t)
	}
}

func asMapInt64(row map[string]any, key string) int64 {
	if row == nil {
		return 0
	}
	switch t := row[key].(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		v, _ := t.Int64()
		return v
	case string:
		var v int64
		_, _ = fmt.Sscan(strings.TrimSpace(t), &v)
		return v
	default:
		return 0
	}
}
