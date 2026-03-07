package app

import (
	"encoding/json"
	"strings"
)

func lowerContainsAny(s string, keywords ...string) bool {
	ls := strings.ToLower(s)
	for _, k := range keywords {
		if strings.Contains(ls, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func unmarshalMap(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func collectStringsByKeys(v any, keySet map[string]struct{}, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			if _, ok := keySet[strings.ToLower(k)]; ok {
				if s, ok := vv.(string); ok && strings.TrimSpace(s) != "" {
					*out = append(*out, strings.TrimSpace(s))
				}
			}
			collectStringsByKeys(vv, keySet, out)
		}
	case []any:
		for _, it := range t {
			collectStringsByKeys(it, keySet, out)
		}
	}
}

func firstNonEmpty(items ...string) string {
	for _, it := range items {
		if strings.TrimSpace(it) != "" {
			return strings.TrimSpace(it)
		}
	}
	return ""
}

func uniqueNonEmpty(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		v := strings.TrimSpace(it)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func uniqueStrings(items ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		v := strings.TrimSpace(it)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
