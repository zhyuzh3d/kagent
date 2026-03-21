package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

const (
	ServiceStatusActive    = "active"
	ServiceStatusConflict  = "conflict"
	ServiceStatusDown      = "down"
	originCallerTokenTTL   = 10 * time.Minute
	originCallerSecretFile = ".origin_caller_secret"
)

type ServiceToolDescriptor = toolproto.ServiceTool

type ServiceManifest = toolproto.ServiceManifest

type HubServiceRegisterRequest struct {
	Manifest   ServiceManifest `json:"manifest"`
	InstanceID string          `json:"instance_id"`
	PID        int             `json:"pid,omitempty"`
	Endpoint   string          `json:"endpoint,omitempty"`
	StartedAt  int64           `json:"started_at_ms,omitempty"`
	Healthy    *bool           `json:"healthy,omitempty"`
}

type HubServiceHeartbeatRequest struct {
	ServiceID  string `json:"service_id"`
	InstanceID string `json:"instance_id"`
	PID        int    `json:"pid,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Healthy    *bool  `json:"healthy,omitempty"`
}

type HubServiceAuth struct {
	ServiceID   string `json:"service_id"`
	InstanceID  string `json:"instance_id"`
	S2HToken    string `json:"s2h_token,omitempty"`
	H2SToken    string `json:"h2s_token,omitempty"`
	IssuedAtMS  int64  `json:"issued_at_ms,omitempty"`
	ExpiresAtMS int64  `json:"expires_at_ms,omitempty"`
}

type HubServiceRegistration struct {
	ServiceID      string          `json:"service_id"`
	ServiceName    string          `json:"service_name"`
	InstanceID     string          `json:"instance_id"`
	PID            int             `json:"pid"`
	Endpoint       string          `json:"endpoint,omitempty"`
	Version        string          `json:"version,omitempty"`
	BuildHash      string          `json:"build_hash,omitempty"`
	Reliability    string          `json:"reliability"`
	Visibility     string          `json:"visibility"`
	ManifestHash   string          `json:"manifest_hash"`
	ToolCount      int             `json:"tool_count"`
	RegisteredAtMS int64           `json:"registered_at_ms"`
	LastSeenAtMS   int64           `json:"last_seen_at_ms"`
	Status         string          `json:"status"`
	Healthy        bool            `json:"healthy"`
	ConflictReason string          `json:"conflict_reason,omitempty"`
	Manifest       ServiceManifest `json:"manifest"`
}

type HubToolProviderStat struct {
	ToolID          string `json:"tool_id"`
	ServiceID       string `json:"service_id"`
	SuccessCount    int64  `json:"success_count"`
	FailureCount    int64  `json:"failure_count"`
	LastLatencyMS   int64  `json:"last_latency_ms"`
	TotalLatencyMS  int64  `json:"total_latency_ms"`
	LastResult      string `json:"last_result,omitempty"`
	LastCalledAtMS  int64  `json:"last_called_at_ms,omitempty"`
	ManualWeight    int    `json:"manual_weight"`
	Enabled         bool   `json:"enabled"`
	RecentErrorRate int    `json:"recent_error_rate,omitempty"`
}

type HubToolProviderView = toolproto.ToolCandidate

type HubToolBinding struct {
	ToolID      string `json:"tool_id"`
	ServiceID   string `json:"service_id"`
	Reason      string `json:"reason"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

type HubToolView = toolproto.ToolView

type HubToolRoute struct {
	Binding HubToolBinding         `json:"binding"`
	Service HubServiceRegistration `json:"service"`
}

type HubPlatform struct {
	mu sync.RWMutex

	dataRoot       string
	routeStatePath string
	originSecret   []byte

	services      map[string]HubServiceRegistration
	serviceAuths  map[string]HubServiceAuth
	conflicts     map[string][]HubServiceRegistration
	toolProviders map[string][]string
	bindings      map[string]HubToolBinding
	manualBind    map[string]string
	stats         map[string]map[string]*HubToolProviderStat
	builtinTools  []ServiceToolDescriptor
	governance    map[string]string
}

var hubBuiltinTools = []ServiceToolDescriptor{
	{
		ToolID:      "hub.system.report_log",
		Category:    "hub",
		Type:        "system",
		Tool:        "report_log",
		Description: "Report a structured log message to the Hub. For internal audit and system overview.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"level":   map[string]any{"type": "string", "enum": []string{"DEBUG", "INFO", "WARN", "ERROR"}, "default": "INFO"},
				"module":  map[string]any{"type": "string", "default": "business"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"content"},
		},
	},
}

func NewHubPlatform(dataRoot string) (*HubPlatform, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, fmt.Errorf("hub data root is empty")
	}
	hub := &HubPlatform{
		dataRoot:       root,
		routeStatePath: filepath.Join(root, "hub", "route_state.json"),
		services:       map[string]HubServiceRegistration{},
		serviceAuths:   map[string]HubServiceAuth{},
		conflicts:      map[string][]HubServiceRegistration{},
		toolProviders:  map[string][]string{},
		bindings:       map[string]HubToolBinding{},
		manualBind:     map[string]string{},
		stats:          map[string]map[string]*HubToolProviderStat{},
		builtinTools:   hubBuiltinTools,
		governance:     map[string]string{},
	}
	secretPath := filepath.Join(root, originCallerSecretFile)
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, fmt.Errorf("origin caller secret init: %w", err)
	}
	hub.originSecret = secret
	hub.loadPersistedStateLocked()
	return hub, nil
}

func (h *HubPlatform) SetBuiltinTools(tools []toolproto.ServiceTool) {
	if h == nil {
		return
	}
	out := make([]ServiceToolDescriptor, 0, len(tools))
	seen := map[string]struct{}{}
	for _, tool := range tools {
		normalized := toolproto.NormalizeServiceTool(tool)
		if normalized.ToolID == "" {
			continue
		}
		if _, ok := seen[normalized.ToolID]; ok {
			continue
		}
		seen[normalized.ToolID] = struct{}{}
		out = append(out, normalized)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ToolID < out[j].ToolID
	})
	h.mu.Lock()
	h.builtinTools = out
	h.mu.Unlock()
}

type hubPersistState struct {
	ManualBind map[string]string                         `json:"manual_bind"`
	Bindings   map[string]HubToolBinding                 `json:"bindings"`
	Stats      map[string]map[string]HubToolProviderStat `json:"stats"`
}

func (h *HubPlatform) loadPersistedStateLocked() {
	if h == nil || strings.TrimSpace(h.routeStatePath) == "" {
		return
	}
	raw, err := os.ReadFile(h.routeStatePath)
	if err != nil {
		return
	}
	var st hubPersistState
	if err := json.Unmarshal(raw, &st); err != nil {
		Warnf("hub load persisted state failed: %v", err)
		return
	}
	if len(st.ManualBind) > 0 {
		h.manualBind = st.ManualBind
	}
	if len(st.Bindings) > 0 {
		h.bindings = st.Bindings
	}
	if len(st.Stats) > 0 {
		h.stats = map[string]map[string]*HubToolProviderStat{}
		for toolID, byService := range st.Stats {
			h.stats[toolID] = map[string]*HubToolProviderStat{}
			for serviceID, stat := range byService {
				cp := stat
				h.stats[toolID][serviceID] = &cp
			}
		}
	}
}

func (h *HubPlatform) savePersistedStateLocked() {
	if h == nil || strings.TrimSpace(h.routeStatePath) == "" {
		return
	}
	state := hubPersistState{
		ManualBind: map[string]string{},
		Bindings:   map[string]HubToolBinding{},
		Stats:      map[string]map[string]HubToolProviderStat{},
	}
	for k, v := range h.manualBind {
		state.ManualBind[k] = v
	}
	for k, v := range h.bindings {
		state.Bindings[k] = v
	}
	for toolID, byService := range h.stats {
		if _, ok := state.Stats[toolID]; !ok {
			state.Stats[toolID] = map[string]HubToolProviderStat{}
		}
		for serviceID, stat := range byService {
			if stat == nil {
				continue
			}
			state.Stats[toolID][serviceID] = *stat
		}
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		Warnf("hub persist state marshal failed: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.routeStatePath), 0o755); err != nil {
		Warnf("hub persist state mkdir failed: %v", err)
		return
	}
	if err := os.WriteFile(h.routeStatePath, append(raw, '\n'), 0o644); err != nil {
		Warnf("hub persist state write failed: %v", err)
	}
}

type HubServiceRegisterResult struct {
	Registered bool                   `json:"registered"`
	Service    HubServiceRegistration `json:"service"`
}

func EnsureServiceConfigFiles(serviceRoot string) error {
	_, err := hubsvc.EnsureProjectConfigFiles(serviceRoot)
	return err
}
