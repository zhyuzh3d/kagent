package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	ServiceStatusActive   = "active"
	ServiceStatusConflict = "conflict"
	ServiceStatusDown     = "down"
)

type ServiceToolDescriptor struct {
	ToolID               string         `json:"tool_id"`
	Category             string         `json:"category"`
	Type                 string         `json:"type"`
	Tool                 string         `json:"tool"`
	Description          string         `json:"description"`
	InputSchema          map[string]any `json:"input_schema,omitempty"`
	OutputSchema         map[string]any `json:"output_schema,omitempty"`
	SideEffect           string         `json:"side_effect,omitempty"`
	CapabilitiesRequired []string       `json:"capabilities_required,omitempty"`
	AllowedCallerTypes   []string       `json:"allowed_caller_types,omitempty"`
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	Streaming            string         `json:"streaming,omitempty"`
	WSPath               string         `json:"ws_path,omitempty"`
	ScopeSupport         []string       `json:"scope_support,omitempty"`
}

type ServiceManifest struct {
	ServiceID           string                  `json:"service_id"`
	ServiceName         string                  `json:"service_name"`
	Version             string                  `json:"version,omitempty"`
	BuildHash           string                  `json:"build_hash,omitempty"`
	Reliability         string                  `json:"reliability,omitempty"`
	Visibility          string                  `json:"visibility,omitempty"`
	Entry               string                  `json:"entry,omitempty"`
	ConfigSchemaVersion int                     `json:"config_schema_version,omitempty"`
	Provides            []ServiceToolDescriptor `json:"provides,omitempty"`
	Requires            []string                `json:"requires,omitempty"`
}

type HubServiceRegisterRequest struct {
	Manifest   ServiceManifest `json:"manifest"`
	InstanceID string          `json:"instance_id"`
	PID        int             `json:"pid,omitempty"`
	Endpoint   string          `json:"endpoint,omitempty"`
	StartedAt  int64           `json:"started_at_ms,omitempty"`
}

type HubServiceSessionClaims struct {
	ServiceID  string `json:"service_id"`
	InstanceID string `json:"instance_id"`
	ExpMS      int64  `json:"exp_ms"`
	Nonce      string `json:"nonce"`
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

type HubToolProviderView struct {
	ServiceID     string  `json:"service_id"`
	ServiceName   string  `json:"service_name"`
	Reliability   string  `json:"reliability"`
	Enabled       bool    `json:"enabled"`
	SuccessRate   float64 `json:"success_rate"`
	P95LatencyMS  int64   `json:"p95_latency_ms"`
	ManualWeight  int     `json:"manual_weight"`
	LastErrorRate int     `json:"last_error_rate"`
}

type HubToolBinding struct {
	ToolID      string `json:"tool_id"`
	ServiceID   string `json:"service_id"`
	Reason      string `json:"reason"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

type HubToolView struct {
	ToolID      string                `json:"tool_id"`
	Category    string                `json:"category"`
	Type        string                `json:"type"`
	Tool        string                `json:"tool"`
	Description string                `json:"description"`
	Binding     HubToolBinding        `json:"binding"`
	Candidates  []HubToolProviderView `json:"candidates"`
}

type HubPlatform struct {
	mu sync.RWMutex

	dataRoot       string
	routeStatePath string
	serviceSecret  []byte
	sessionTTL     time.Duration

	services      map[string]HubServiceRegistration
	conflicts     map[string][]HubServiceRegistration
	toolProviders map[string][]string
	bindings      map[string]HubToolBinding
	manualBind    map[string]string
	stats         map[string]map[string]*HubToolProviderStat
}

func NewHubPlatform(dataRoot string) (*HubPlatform, error) {
	root := strings.TrimSpace(dataRoot)
	if root == "" {
		return nil, fmt.Errorf("hub data root is empty")
	}
	secretPath := filepath.Join(root, ".service_secret")
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, fmt.Errorf("load hub service secret: %w", err)
	}
	hub := &HubPlatform{
		dataRoot:       root,
		routeStatePath: filepath.Join(root, "hub", "route_state.json"),
		serviceSecret:  secret,
		sessionTTL:     30 * time.Minute,
		services:       map[string]HubServiceRegistration{},
		conflicts:      map[string][]HubServiceRegistration{},
		toolProviders:  map[string][]string{},
		bindings:       map[string]HubToolBinding{},
		manualBind:     map[string]string{},
		stats:          map[string]map[string]*HubToolProviderStat{},
	}
	hub.loadPersistedStateLocked()
	return hub, nil
}

func loadOrCreateSecret(path string) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err == nil && len(decoded) >= 32 {
			return decoded[:32], nil
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create secret dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	return secret, nil
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
	Token      string                 `json:"service_session_token,omitempty"`
	ExpMS      int64                  `json:"exp_ms,omitempty"`
}
