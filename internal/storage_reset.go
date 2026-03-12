package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CleanupLegacyStorage removes legacy storage files after moving to data/kagent.db.
func CleanupLegacyStorage(dataRoot string, activeSQLitePath string) error {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		root = "data"
	}
	active := strings.TrimSpace(activeSQLitePath)
	activeSet := map[string]struct{}{}
	if active != "" {
		clean, err := filepath.Abs(active)
		if err == nil {
			activeSet[clean] = struct{}{}
			activeSet[clean+"-wal"] = struct{}{}
			activeSet[clean+"-shm"] = struct{}{}
		}
	}

	patterns := []string{
		filepath.Join(root, "users", "*", "chat_state.db"),
		filepath.Join(root, "users", "*", "chat_state.db-wal"),
		filepath.Join(root, "users", "*", "chat_state.db-shm"),
		filepath.Join(root, "users", "*", "action_records.jsonl"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("glob %s: %w", pattern, err)
		}
		for _, p := range matches {
			if p == "" {
				continue
			}
			absPath, err := filepath.Abs(p)
			if err == nil {
				if _, blocked := activeSet[absPath]; blocked {
					continue
				}
			}
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", p, err)
			}
		}
	}
	return nil
}
