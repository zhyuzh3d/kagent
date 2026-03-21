package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"kagent/pkg/hubsvc"
)

func normalizeServiceManifest(in ServiceManifest) (ServiceManifest, error) {
	m := in
	m.ServiceID = strings.TrimSpace(m.ServiceID)
	if m.ServiceID == "" {
		return ServiceManifest{}, fmt.Errorf("service_id is required")
	}
	m.ServiceName = firstNonEmpty(m.ServiceName, m.ServiceID)
	m.Reliability = normalizeReliability(m.Reliability)
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
	t := in
	t.ToolID = strings.TrimSpace(t.ToolID)
	t.Category = strings.TrimSpace(t.Category)
	t.Type = strings.TrimSpace(t.Type)
	t.Tool = strings.TrimSpace(t.Tool)
	t.Description = strings.TrimSpace(t.Description)
	t.SideEffect = strings.TrimSpace(t.SideEffect)
	t.Streaming = strings.TrimSpace(t.Streaming)
	if t.ToolID == "" {
		if t.Category != "" && t.Type != "" && t.Tool != "" {
			t.ToolID = t.Category + "." + t.Type + "." + t.Tool
		}
	}
	t.CapabilitiesRequired = uniqueNonEmpty(t.CapabilitiesRequired)
	t.ScopeSupport = uniqueNonEmpty(t.ScopeSupport)
	return t
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

func EnsureServiceConfigFiles(serviceRoot string) error {
	_, err := hubsvc.EnsureProjectConfigFiles(serviceRoot)
	return err
}
