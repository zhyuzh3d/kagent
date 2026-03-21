package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	app "kagent/services/surface_manager/internal/app"
)

func surfaceStoragePath(claims app.SurfaceTokenClaims, cleanPath string) string {
	base := filepath.Join("surface", strings.TrimSpace(claims.UserID), strings.TrimSpace(claims.SurfaceID))
	if strings.TrimSpace(cleanPath) == "" || cleanPath == "." {
		return base
	}
	return filepath.Join(base, cleanPath)
}

func decodeDataBase64Result(result any) ([]byte, error) {
	value := decodeResultMap(result)["data_base64"]
	raw, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("data_base64 is missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeItemsResult(result any) ([]map[string]any, error) {
	itemsRaw := decodeResultMap(result)["items"]
	switch tv := itemsRaw.(type) {
	case []map[string]any:
		return tv, nil
	case []any:
		out := make([]map[string]any, 0, len(tv))
		for _, item := range tv {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("items is missing")
	}
}

func decodeResultMap(result any) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	if m, ok := result.(map[string]any); ok {
		return m
	}
	raw, _ := json.Marshal(result)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
