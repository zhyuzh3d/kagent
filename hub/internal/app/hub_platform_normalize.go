package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kagent/pkg/toolproto"
)

func normalizeServiceManifest(in ServiceManifest) (ServiceManifest, error) {
	m := toolproto.NormalizeServiceManifest(in)
	m.ServiceID = strings.TrimSpace(m.ServiceID)
	if m.ServiceID == "" {
		return ServiceManifest{}, fmt.Errorf("service_id is required")
	}
	m.ServiceName = firstNonEmpty(m.ServiceName, m.ServiceID)
	m.Visibility = normalizeVisibility(m.Visibility)
	if len(m.Provides) > 0 {
		out := make([]ServiceToolDescriptor, 0, len(m.Provides))
		seen := map[string]struct{}{}
		for _, t := range m.Provides {
			td := normalizeToolDescriptor(t)
			if td.ToolID == "" {
				continue
			}
			if _, ok := seen[td.ToolID]; ok {
				continue
			}
			seen[td.ToolID] = struct{}{}
			out = append(out, td)
		}
		sort.Slice(out, func(i, j int) bool {
			return out[i].ToolID < out[j].ToolID
		})
		m.Provides = out
	}
	m.Requires = uniqueNonEmpty(m.Requires)
	return m, nil
}

func normalizeToolDescriptor(in ServiceToolDescriptor) ServiceToolDescriptor {
	return toolproto.NormalizeServiceTool(in)
}

func manifestHash(manifest ServiceManifest) (string, error) {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func normalizeReliability(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "trusted":
		return "trusted"
	case "verified":
		return "verified"
	case "unverified":
		return "unverified"
	case "risky":
		return "risky"
	case "high_risk":
		return "high_risk"
	default:
		return "unverified"
	}
}

func normalizeVisibility(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "public":
		return "public"
	case "private":
		return "private"
	case "internal":
		return "internal"
	default:
		return "public"
	}
}

func inferTransportFromEndpoint(endpoint string) string {
	value := strings.TrimSpace(endpoint)
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "http://"), strings.HasPrefix(value, "https://"):
		return "tcp"
	default:
		return "uds"
	}
}

func reliabilityWeight(v string) float64 {
	switch normalizeReliability(v) {
	case "trusted":
		return 1.0
	case "verified":
		return 0.8
	case "unverified":
		return 0.6
	case "risky":
		return 0.3
	case "high_risk":
		return 0.1
	default:
		return 0.5
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}
