package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

type UserCustomConfigFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	UserID        string         `json:"userId"`
	UpdatedAt     string         `json:"updatedAt"`
	Overrides     map[string]any `json:"overrides"`
}

type RuntimeConfigManager struct {
	defaultPath string
	userPath    string

	mu           sync.RWMutex
	defaultMap   map[string]any
	effectiveMap map[string]any
	snapshot     PublicConfig
}

func NewRuntimeConfigManager(defaultPath string, userPath string) (*RuntimeConfigManager, error) {
	m := &RuntimeConfigManager{
		defaultPath: defaultPath,
		userPath:    userPath,
	}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *RuntimeConfigManager) Reload() error {
	baseMap, err := structToMap(defaultPublicConfig())
	if err != nil {
		return fmt.Errorf("default public config: %w", err)
	}
	fileDefaults, err := loadOptionalConfigMap(m.defaultPath, false)
	if err != nil {
		return fmt.Errorf("load public config %s: %w", m.defaultPath, err)
	}
	mergedDefaults := deepMergeMaps(baseMap, fileDefaults)

	userCfg, err := loadUserCustomConfigFile(m.userPath)
	if err != nil {
		return fmt.Errorf("load user custom config %s: %w", m.userPath, err)
	}
	effective := deepMergeMaps(mergedDefaults, userCfg.Overrides)

	var snapshot PublicConfig
	if err := mapToStruct(effective, &snapshot); err != nil {
		return fmt.Errorf("decode effective public config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultMap = mergedDefaults
	m.effectiveMap = effective
	m.snapshot = snapshot
	return nil
}

func (m *RuntimeConfigManager) EffectiveMap() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneMap(m.effectiveMap)
}

func (m *RuntimeConfigManager) Snapshot() PublicConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *RuntimeConfigManager) UpdateEffectiveMap(next map[string]any) (map[string]any, error) {
	m.mu.RLock()
	base := cloneMap(m.defaultMap)
	m.mu.RUnlock()

	overrides := diffMaps(base, next)
	userCfg := UserCustomConfigFile{
		SchemaVersion: 1,
		UserID:        "default",
		UpdatedAt:     time.Now().Format(time.RFC3339),
		Overrides:     overrides,
	}
	if err := writeJSONAtomic(m.userPath, userCfg); err != nil {
		return nil, fmt.Errorf("write user custom config: %w", err)
	}
	if err := m.Reload(); err != nil {
		return nil, err
	}
	return m.EffectiveMap(), nil
}

func loadUserCustomConfigFile(path string) (*UserCustomConfigFile, error) {
	if path == "" {
		return &UserCustomConfigFile{SchemaVersion: 1, UserID: "default", Overrides: map[string]any{}}, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return &UserCustomConfigFile{SchemaVersion: 1, UserID: "default", Overrides: map[string]any{}}, nil
		}
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg UserCustomConfigFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if cfg.Overrides == nil {
		cfg.Overrides = map[string]any{}
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = 1
	}
	if cfg.UserID == "" {
		cfg.UserID = "default"
	}
	return &cfg, nil
}

func loadOptionalConfigMap(path string, missingOK bool) (map[string]any, error) {
	if path == "" {
		return map[string]any{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if missingOK && os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	m, err := unmarshalMap(b)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

func deepMergeMaps(base map[string]any, override map[string]any) map[string]any {
	out := cloneMap(base)
	for k, v := range override {
		if vm, ok := v.(map[string]any); ok {
			if bm, ok := out[k].(map[string]any); ok {
				out[k] = deepMergeMaps(bm, vm)
				continue
			}
		}
		out[k] = cloneValue(v)
	}
	return out
}

func diffMaps(base map[string]any, current map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range current {
		baseVal, ok := base[k]
		if !ok {
			out[k] = cloneValue(v)
			continue
		}
		vm, vok := v.(map[string]any)
		bm, bok := baseVal.(map[string]any)
		if vok && bok {
			if diff := diffMaps(bm, vm); len(diff) > 0 {
				out[k] = diff
			}
			continue
		}
		if !reflect.DeepEqual(baseVal, v) {
			out[k] = cloneValue(v)
		}
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneSlice(in []any) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		return cloneSlice(t)
	default:
		return t
	}
}

func structToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return unmarshalMap(b)
}

func mapToStruct(m map[string]any, out any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "cfg-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func durationFromMS(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}
