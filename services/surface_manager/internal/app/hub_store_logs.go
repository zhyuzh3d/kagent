package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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
