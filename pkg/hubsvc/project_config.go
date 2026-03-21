package hubsvc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ConfigLoadErrorMissing = "missing"
	ConfigLoadErrorLoad    = "load"
)

var DefaultProjectConfigFiles = []string{"config.json", "configx.json"}

type ProjectConfigFiles struct {
	ConfigDir          string
	ConfigPath         string
	ConfigXPath        string
	ConfigXExamplePath string
}

type ConfigFileError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (e *ConfigFileError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Kind) == "" {
		return strings.TrimSpace(e.Message)
	}
	if strings.TrimSpace(e.Message) == "" {
		return strings.TrimSpace(e.Kind)
	}
	return strings.TrimSpace(e.Kind) + ": " + strings.TrimSpace(e.Message)
}

type ConfigFileLoadResult struct {
	Result map[string]any   `json:"result"`
	Err    *ConfigFileError `json:"err,omitempty"`
}

type ProjectConfig struct {
	Files   ProjectConfigFiles
	Config  map[string]any
	ConfigX map[string]any
	Loaded  map[string]ConfigFileLoadResult
}

func ProjectConfigLayout(projectRoot string) ProjectConfigFiles {
	root := strings.TrimSpace(projectRoot)
	configDir := filepath.Join(root, "config")
	return ProjectConfigFiles{
		ConfigDir:          configDir,
		ConfigPath:         filepath.Join(configDir, "config.json"),
		ConfigXPath:        filepath.Join(configDir, "configx.json"),
		ConfigXExamplePath: filepath.Join(configDir, "configx.json.example"),
	}
}

func EnsureProjectConfigFiles(projectRoot string) (ProjectConfigFiles, error) {
	layout := ProjectConfigLayout(projectRoot)
	if err := os.MkdirAll(layout.ConfigDir, 0o755); err != nil {
		return layout, fmt.Errorf("ensure config dir: %w", err)
	}
	defaults := map[string]string{
		layout.ConfigPath:         "{}\n",
		layout.ConfigXPath:        "{}\n",
		layout.ConfigXExamplePath: "{\n  \"secrets\": {}\n}\n",
	}
	for path, content := range defaults {
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return layout, fmt.Errorf("stat %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return layout, fmt.Errorf("write default %s: %w", path, err)
		}
	}
	return layout, nil
}

func LoadProjectConfig(projectRoot string) (*ProjectConfig, error) {
	layout, err := EnsureProjectConfigFiles(projectRoot)
	if err != nil {
		return nil, err
	}
	loaded, err := LoadProjectConfigFiles(projectRoot, nil)
	if err != nil {
		return nil, err
	}
	cfg := &ProjectConfig{
		Files:   layout,
		Config:  map[string]any{},
		ConfigX: map[string]any{},
		Loaded:  loaded,
	}
	if item, ok := loaded["config.json"]; ok && item.Result != nil {
		cfg.Config = cloneJSONMap(item.Result)
	}
	if item, ok := loaded["configx.json"]; ok && item.Result != nil {
		cfg.ConfigX = cloneJSONMap(item.Result)
	}
	return cfg, nil
}

func LoadProjectConfigFiles(projectRoot string, files []string) (map[string]ConfigFileLoadResult, error) {
	layout, err := EnsureProjectConfigFiles(projectRoot)
	if err != nil {
		return nil, err
	}
	requested := normalizeRequestedConfigFiles(files)
	results := make(map[string]ConfigFileLoadResult, len(requested))
	var failures []string
	for _, rel := range requested {
		result := ConfigFileLoadResult{Result: map[string]any{}}
		safeRel, err := normalizeConfigRelativePath(rel)
		if err != nil {
			result.Err = &ConfigFileError{Kind: ConfigLoadErrorLoad, Message: err.Error()}
			results[rel] = result
			failures = append(failures, rel+": "+result.Err.Error())
			continue
		}
		path := filepath.Join(layout.ConfigDir, safeRel)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			kind := ConfigLoadErrorLoad
			if os.IsNotExist(readErr) {
				kind = ConfigLoadErrorMissing
			}
			result.Err = &ConfigFileError{Kind: kind, Message: readErr.Error()}
			results[safeRel] = result
			failures = append(failures, safeRel+": "+result.Err.Error())
			continue
		}
		decoded, decodeErr := DecodeJSONMapAllowEmpty(data)
		if decodeErr != nil {
			result.Err = &ConfigFileError{Kind: ConfigLoadErrorLoad, Message: decodeErr.Error()}
			results[safeRel] = result
			failures = append(failures, safeRel+": "+result.Err.Error())
			continue
		}
		result.Result = decoded
		results[safeRel] = result
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("load project config files failed: %s", strings.Join(failures, "; "))
	}
	return results, nil
}

func LoadJSONMapFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	return DecodeJSONMapAllowEmpty(data)
}

func DecodeJSONMapAllowEmpty(raw []byte) (map[string]any, error) {
	if JSONBytesBlank(raw) {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func JSONBytesBlank(raw []byte) bool {
	return strings.TrimSpace(string(raw)) == ""
}

func normalizeRequestedConfigFiles(files []string) []string {
	if len(files) == 0 {
		return append([]string(nil), DefaultProjectConfigFiles...)
	}
	out := make([]string, 0, len(files))
	seen := map[string]struct{}{}
	for _, item := range files {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultProjectConfigFiles...)
	}
	return out
}

func normalizeConfigRelativePath(path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("config file path is empty")
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("config file path must be relative: %s", path)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("config file path escapes config directory: %s", path)
	}
	return clean, nil
}

func cloneJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneJSONValue(v)
	}
	return out
}

func cloneJSONSlice(in []any) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = cloneJSONValue(v)
	}
	return out
}

func cloneJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneJSONMap(t)
	case []any:
		return cloneJSONSlice(t)
	default:
		return t
	}
}
