package app

import "context"

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
