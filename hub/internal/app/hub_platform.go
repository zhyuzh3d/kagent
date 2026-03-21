package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

func (h *HubPlatform) RegisterService(req HubServiceRegisterRequest) (HubServiceRegisterResult, error) {
	if h == nil {
		return HubServiceRegisterResult{}, fmt.Errorf("hub is nil")
	}
	manifest, err := normalizeServiceManifest(req.Manifest)
	if err != nil {
		return HubServiceRegisterResult{}, err
	}
	instanceID := strings.TrimSpace(req.InstanceID)
	if instanceID == "" {
		instanceID = "ins-" + newRequestID()
	}
	now := nowMS()
	startedAt := req.StartedAt
	if startedAt <= 0 {
		startedAt = now
	}
	mh, err := manifestHash(manifest)
	if err != nil {
		return HubServiceRegisterResult{}, err
	}
	reg := HubServiceRegistration{
		ServiceID:      manifest.ServiceID,
		ServiceName:    manifest.ServiceName,
		InstanceID:     instanceID,
		PID:            req.PID,
		Endpoint:       strings.TrimSpace(req.Endpoint),
		Version:        manifest.Version,
		BuildHash:      manifest.BuildHash,
		Reliability:    "unverified",
		Visibility:     manifest.Visibility,
		ManifestHash:   mh,
		ToolCount:      len(manifest.Provides),
		RegisteredAtMS: startedAt,
		LastSeenAtMS:   now,
		Status:         ServiceStatusActive,
		Healthy:        boolOrDefault(req.Healthy, true),
		Manifest:       manifest,
	}
	if !reg.Healthy {
		reg.Status = ServiceStatusDown
		reg.ConflictReason = "service registered as unhealthy"
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	reg.Reliability = h.governedReliabilityLocked(manifest.ServiceID)

	if existing, ok := h.services[manifest.ServiceID]; ok && existing.InstanceID != instanceID && existing.Status == ServiceStatusActive {
		reg.Status = ServiceStatusConflict
		reg.ConflictReason = "service_id has active instance"
		h.conflicts[manifest.ServiceID] = append(h.conflicts[manifest.ServiceID], reg)
		return HubServiceRegisterResult{
			Registered: false,
			Service:    reg,
		}, fmt.Errorf("service_id %s already active with instance %s", manifest.ServiceID, existing.InstanceID)
	}

	h.services[manifest.ServiceID] = reg
	h.conflicts[manifest.ServiceID] = nil
	h.rebuildToolProvidersLocked()
	h.refreshBindingsLocked("service_register")
	h.savePersistedStateLocked()
	return HubServiceRegisterResult{
		Registered: true,
		Service:    reg,
	}, nil
}

func (h *HubPlatform) GetService(serviceID string) (HubServiceRegistration, bool) {
	if h == nil {
		return HubServiceRegistration{}, false
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return HubServiceRegistration{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	reg, ok := h.services[sid]
	return reg, ok
}

func (h *HubPlatform) AcceptServiceHeartbeat(req HubServiceHeartbeatRequest) (HubServiceRegistration, error) {
	if h == nil {
		return HubServiceRegistration{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(req.ServiceID)
	iid := strings.TrimSpace(req.InstanceID)
	if sid == "" || iid == "" {
		return HubServiceRegistration{}, fmt.Errorf("service_id and instance_id are required")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	reg, ok := h.services[sid]
	if !ok {
		return HubServiceRegistration{}, fmt.Errorf("service not registered")
	}
	if reg.InstanceID != iid {
		return HubServiceRegistration{}, fmt.Errorf("service instance mismatch")
	}
	if req.PID > 0 {
		reg.PID = req.PID
	}
	if ep := strings.TrimSpace(req.Endpoint); ep != "" {
		reg.Endpoint = ep
	}
	reg.LastSeenAtMS = nowMS()
	if req.Healthy != nil {
		reg.Healthy = *req.Healthy
	}
	if reg.Healthy {
		reg.Status = ServiceStatusActive
		reg.ConflictReason = ""
	} else {
		reg.Status = ServiceStatusDown
		reg.ConflictReason = "heartbeat unhealthy"
	}
	h.services[sid] = reg
	h.refreshBindingsLocked("service_heartbeat")
	return reg, nil
}

func (h *HubPlatform) UnregisterService(serviceID string, instanceID string) {
	if h == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	h.mu.Lock()
	defer h.mu.Unlock()
	reg, ok := h.services[sid]
	if !ok {
		return
	}
	if iid != "" && iid != reg.InstanceID {
		return
	}
	delete(h.services, sid)
	delete(h.serviceAuths, sid)
	delete(h.conflicts, sid)
	h.rebuildToolProvidersLocked()
	h.refreshBindingsLocked("service_unregister")
	h.savePersistedStateLocked()
}

func (h *HubPlatform) MarkServiceDown(serviceID string, reason string) {
	if h == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	h.mu.Lock()
	defer h.mu.Unlock()
	reg, ok := h.services[sid]
	if !ok {
		return
	}
	reg.Status = ServiceStatusDown
	reg.Healthy = false
	reg.ConflictReason = strings.TrimSpace(reason)
	reg.LastSeenAtMS = nowMS()
	h.services[sid] = reg
	h.refreshBindingsLocked("service_down")
	h.savePersistedStateLocked()
}

func (h *HubPlatform) MarkServiceActive(serviceID string) {
	if h == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	h.mu.Lock()
	defer h.mu.Unlock()
	reg, ok := h.services[sid]
	if !ok {
		return
	}
	reg.Status = ServiceStatusActive
	reg.Healthy = true
	reg.ConflictReason = ""
	reg.LastSeenAtMS = nowMS()
	h.services[sid] = reg
	h.refreshBindingsLocked("service_up")
	h.savePersistedStateLocked()
}

func (h *HubPlatform) SetServiceReliability(serviceID string, reliability string) {
	if h == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.governance == nil {
		h.governance = map[string]string{}
	}
	reliability = normalizeReliability(reliability)
	h.governance[sid] = reliability
	if reg, ok := h.services[sid]; ok {
		reg.Reliability = reliability
		h.services[sid] = reg
	}
	h.refreshBindingsLocked("service_governance")
	h.savePersistedStateLocked()
}

func (h *HubPlatform) PrepareServiceBootstrap(serviceID string, instanceID string, registerURL string, ttl time.Duration) (hubsvc.BootstrapSecret, error) {
	if h == nil {
		return hubsvc.BootstrapSecret{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	if sid == "" || iid == "" {
		return hubsvc.BootstrapSecret{}, fmt.Errorf("service_id and instance_id are required")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	issuedAtMS := nowMS()
	expiresAtMS := issuedAtMS + ttl.Milliseconds()
	s2hToken, err := randomAuthToken()
	if err != nil {
		return hubsvc.BootstrapSecret{}, err
	}
	h2sToken, err := randomAuthToken()
	if err != nil {
		return hubsvc.BootstrapSecret{}, err
	}
	for h2sToken == s2hToken {
		h2sToken, err = randomAuthToken()
		if err != nil {
			return hubsvc.BootstrapSecret{}, err
		}
	}
	auth := HubServiceAuth{
		ServiceID:   sid,
		InstanceID:  iid,
		S2HToken:    s2hToken,
		H2SToken:    h2sToken,
		IssuedAtMS:  issuedAtMS,
		ExpiresAtMS: expiresAtMS,
	}
	h.mu.Lock()
	h.serviceAuths[sid] = auth
	h.mu.Unlock()
	bootstrap := hubsvc.BootstrapSecret{
		ServiceID:      sid,
		InstanceID:     iid,
		HubRegisterURL: strings.TrimSpace(registerURL),
		S2HToken:       s2hToken,
		H2SToken:       h2sToken,
		IssuedAtMS:     issuedAtMS,
		ExpiresAtMS:    expiresAtMS,
	}
	if err := bootstrap.Validate(); err != nil {
		return hubsvc.BootstrapSecret{}, err
	}
	return bootstrap, nil
}

func (h *HubPlatform) ServiceHubAuth(serviceID string) (HubServiceAuth, bool) {
	if h == nil {
		return HubServiceAuth{}, false
	}
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return HubServiceAuth{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	auth, ok := h.serviceAuths[sid]
	return auth, ok
}

func (h *HubPlatform) VerifyServiceAuth(serviceID string, instanceID string, token string) (HubServiceAuth, error) {
	if h == nil {
		return HubServiceAuth{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	expectedToken := strings.TrimSpace(token)
	if sid == "" || iid == "" || expectedToken == "" {
		return HubServiceAuth{}, fmt.Errorf("service auth headers are required")
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	auth, ok := h.serviceAuths[sid]
	if !ok {
		return HubServiceAuth{}, fmt.Errorf("service auth missing")
	}
	if strings.TrimSpace(auth.InstanceID) != iid {
		return HubServiceAuth{}, fmt.Errorf("service instance mismatch")
	}
	if strings.TrimSpace(auth.S2HToken) != expectedToken {
		return HubServiceAuth{}, fmt.Errorf("service auth token mismatch")
	}
	return auth, nil
}

func (h *HubPlatform) VerifyHubAuth(serviceID string, instanceID string, token string) (HubServiceAuth, error) {
	if h == nil {
		return HubServiceAuth{}, fmt.Errorf("hub is nil")
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	expectedToken := strings.TrimSpace(token)
	if sid == "" || expectedToken == "" {
		return HubServiceAuth{}, fmt.Errorf("hub auth headers are required")
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	auth, ok := h.serviceAuths[sid]
	if !ok {
		return HubServiceAuth{}, fmt.Errorf("service auth missing")
	}
	if iid != "" && strings.TrimSpace(auth.InstanceID) != iid {
		return HubServiceAuth{}, fmt.Errorf("service instance mismatch")
	}
	if strings.TrimSpace(auth.H2SToken) != expectedToken {
		return HubServiceAuth{}, fmt.Errorf("hub auth token mismatch")
	}
	return auth, nil
}

func (h *HubPlatform) SetManualBinding(toolID string, serviceID string) error {
	if h == nil {
		return fmt.Errorf("hub is nil")
	}
	tid := strings.TrimSpace(toolID)
	sid := strings.TrimSpace(serviceID)
	if tid == "" || sid == "" {
		return fmt.Errorf("tool_id and service_id are required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.services[sid]; !ok {
		return fmt.Errorf("service_id not found: %s", sid)
	}
	h.manualBind[tid] = sid
	h.refreshBindingsLocked("manual_override")
	h.savePersistedStateLocked()
	return nil
}

func (h *HubPlatform) RecordToolCall(toolID string, serviceID string, success bool, latency time.Duration) {
	if h == nil {
		return
	}
	tid := strings.TrimSpace(toolID)
	sid := strings.TrimSpace(serviceID)
	if tid == "" || sid == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.stats[tid]; !ok {
		h.stats[tid] = map[string]*HubToolProviderStat{}
	}
	stat, ok := h.stats[tid][sid]
	if !ok {
		stat = &HubToolProviderStat{
			ToolID:    tid,
			ServiceID: sid,
			Enabled:   true,
		}
		h.stats[tid][sid] = stat
	}
	if success {
		stat.SuccessCount++
		stat.LastResult = "ok"
	} else {
		stat.FailureCount++
		stat.LastResult = "error"
	}
	ms := latency.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	stat.LastLatencyMS = ms
	stat.TotalLatencyMS += ms
	stat.LastCalledAtMS = nowMS()
	total := stat.SuccessCount + stat.FailureCount
	if total >= 5 {
		stat.RecentErrorRate = int((stat.FailureCount * 100) / total)
	}
	if !success {
		h.refreshBindingsLocked("tool_error")
	}
	h.savePersistedStateLocked()
}

func (h *HubPlatform) RefreshBindings(reason string) []HubToolBinding {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refreshBindingsLocked(reason)
}

func (h *HubPlatform) refreshBindingsLocked(_ string) []HubToolBinding {
	now := nowMS()
	toolIDs := make([]string, 0, len(h.toolProviders))
	for toolID := range h.toolProviders {
		toolIDs = append(toolIDs, toolID)
	}
	sort.Strings(toolIDs)
	out := make([]HubToolBinding, 0, len(toolIDs))
	for _, toolID := range toolIDs {
		providers := h.toolProviders[toolID]
		if len(providers) == 0 {
			continue
		}
		manualService := strings.TrimSpace(h.manualBind[toolID])
		manualHandled := false
		if manualService != "" {
			for _, sid := range providers {
				if sid == manualService && h.services[sid].Status == ServiceStatusActive {
					b := HubToolBinding{
						ToolID:      toolID,
						ServiceID:   sid,
						Reason:      "manual_override",
						UpdatedAtMS: now,
					}
					h.bindings[toolID] = b
					out = append(out, b)
					manualHandled = true
					break
				}
			}
		}
		if manualHandled {
			continue
		}
		bestSID := ""
		bestReason := ""
		bestScore := -1.0
		for _, sid := range providers {
			svc, ok := h.services[sid]
			if !ok || svc.Status != ServiceStatusActive {
				continue
			}
			score, reason := h.scoreProviderLocked(toolID, sid, svc.Reliability)
			if score > bestScore {
				bestScore = score
				bestSID = sid
				bestReason = reason
			}
		}
		if bestSID == "" {
			delete(h.bindings, toolID)
			continue
		}
		b := HubToolBinding{
			ToolID:      toolID,
			ServiceID:   bestSID,
			Reason:      bestReason,
			UpdatedAtMS: now,
		}
		h.bindings[toolID] = b
		out = append(out, b)
	}
	h.savePersistedStateLocked()
	return out
}

func (h *HubPlatform) scoreProviderLocked(toolID string, serviceID string, reliability string) (float64, string) {
	rel := reliabilityWeight(reliability)
	successRate := 0.5
	latencyFactor := 0.5
	manualWeight := 0
	errorRate := 0
	if toolStats, ok := h.stats[toolID]; ok {
		if stat, ok := toolStats[serviceID]; ok {
			total := stat.SuccessCount + stat.FailureCount
			if total > 0 {
				successRate = float64(stat.SuccessCount) / float64(total)
			}
			if total > 0 && stat.TotalLatencyMS > 0 {
				avgLatency := float64(stat.TotalLatencyMS) / float64(total)
				latencyFactor = clamp01(1.0 - avgLatency/5000.0)
			}
			manualWeight = stat.ManualWeight
			errorRate = stat.RecentErrorRate
			if !stat.Enabled {
				return -1, "disabled"
			}
		}
	}
	score := rel*0.45 + successRate*0.35 + latencyFactor*0.2 + float64(manualWeight)*0.01
	reason := fmt.Sprintf("rel=%.2f success=%.2f latency=%.2f err=%d%%", rel, successRate, latencyFactor, errorRate)
	return score, reason
}

func (h *HubPlatform) rebuildToolProvidersLocked() {
	h.toolProviders = map[string][]string{}
	for sid, reg := range h.services {
		for _, t := range reg.Manifest.Provides {
			toolID := strings.TrimSpace(t.ToolID)
			if toolID == "" {
				continue
			}
			h.toolProviders[toolID] = append(h.toolProviders[toolID], sid)
			if _, ok := h.stats[toolID]; !ok {
				h.stats[toolID] = map[string]*HubToolProviderStat{}
			}
			if _, ok := h.stats[toolID][sid]; !ok {
				h.stats[toolID][sid] = &HubToolProviderStat{
					ToolID:    toolID,
					ServiceID: sid,
					Enabled:   true,
				}
			}
		}
	}
	for toolID := range h.toolProviders {
		sort.Strings(h.toolProviders[toolID])
	}
}

func (h *HubPlatform) ListServices() []HubServiceRegistration {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]HubServiceRegistration, 0, len(h.services))
	for _, reg := range h.services {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceID == out[j].ServiceID {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].ServiceID < out[j].ServiceID
	})
	for _, c := range h.conflicts {
		for _, reg := range c {
			out = append(out, reg)
		}
	}
	return out
}

func (h *HubPlatform) ListRegisteredServices() []HubServiceRegistration {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]HubServiceRegistration, 0, len(h.services))
	for _, reg := range h.services {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceID == out[j].ServiceID {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].ServiceID < out[j].ServiceID
	})
	return out
}

func (h *HubPlatform) ListBindings() []HubToolBinding {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]HubToolBinding, 0, len(h.bindings))
	for _, b := range h.bindings {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ToolID == out[j].ToolID {
			return out[i].ServiceID < out[j].ServiceID
		}
		return out[i].ToolID < out[j].ToolID
	})
	return out
}

func (h *HubPlatform) ListTools() []HubToolView {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	meta := map[string]ServiceToolDescriptor{}
	for _, reg := range h.services {
		for _, t := range reg.Manifest.Provides {
			toolID := strings.TrimSpace(t.ToolID)
			if toolID == "" {
				continue
			}
			if _, ok := meta[toolID]; !ok {
				meta[toolID] = toolproto.NormalizeServiceTool(t)
			}
		}
	}

	for _, t := range h.builtinTools {
		toolID := strings.TrimSpace(t.ToolID)
		if toolID == "" {
			continue
		}
		if _, ok := meta[toolID]; !ok {
			meta[toolID] = toolproto.NormalizeServiceTool(t)
		}
	}

	toolIDs := make([]string, 0, len(h.toolProviders)+len(h.builtinTools))
	for tid := range h.toolProviders {
		toolIDs = append(toolIDs, tid)
	}
	for _, t := range h.builtinTools {
		tid := strings.TrimSpace(t.ToolID)
		if _, ok := h.toolProviders[tid]; !ok {
			toolIDs = append(toolIDs, tid)
		}
	}
	sort.Strings(toolIDs)
	out := make([]HubToolView, 0, len(toolIDs))
	for _, tid := range toolIDs {
		providers := h.toolProviders[tid]
		cands := h.buildToolCandidatesLocked(tid, providers)
		toolMeta := meta[tid]
		out = append(out, h.buildToolViewLocked(tid, toolMeta, cands))
	}
	return out
}

func (h *HubPlatform) buildToolCandidatesLocked(toolID string, providers []string) []HubToolProviderView {
	cands := make([]HubToolProviderView, 0, len(providers))
	for _, sid := range providers {
		reg, ok := h.services[sid]
		if !ok {
			continue
		}
		stat := h.stats[toolID][sid]
		successRate := 0.0
		callCount := int64(0)
		lastLatency := int64(0)
		manualWeight := 0
		lastErrorRate := 0
		enabled := reg.Status == ServiceStatusActive
		if stat != nil {
			callCount = stat.SuccessCount + stat.FailureCount
			if callCount > 0 {
				successRate = float64(stat.SuccessCount) / float64(callCount)
			}
			lastLatency = stat.LastLatencyMS
			manualWeight = stat.ManualWeight
			lastErrorRate = stat.RecentErrorRate
			enabled = enabled && stat.Enabled
		}
		cands = append(cands, HubToolProviderView{
			ServiceID:     sid,
			ServiceName:   reg.ServiceName,
			Reliability:   h.governedReliabilityLocked(sid),
			Enabled:       enabled,
			Healthy:       reg.Healthy,
			Status:        reg.Status,
			Transport:     inferTransportFromEndpoint(reg.Endpoint),
			Endpoint:      reg.Endpoint,
			LastSeenAtMS:  reg.LastSeenAtMS,
			SuccessRate:   successRate,
			CallCount:     callCount,
			LastLatencyMS: lastLatency,
			ManualWeight:  manualWeight,
			LastErrorRate: lastErrorRate,
		})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Enabled != cands[j].Enabled {
			return cands[i].Enabled
		}
		if cands[i].SuccessRate == cands[j].SuccessRate {
			return cands[i].ServiceID < cands[j].ServiceID
		}
		return cands[i].SuccessRate > cands[j].SuccessRate
	})
	return cands
}

func (h *HubPlatform) buildToolViewLocked(toolID string, spec ServiceToolDescriptor, candidates []HubToolProviderView) HubToolView {
	binding, hasBinding := h.bindings[toolID]
	isBuiltinOnly := len(candidates) == 0 && h.hasBuiltinToolLocked(toolID)
	observed := toolproto.ToolObserved{
		Registered: len(candidates) > 0 || isBuiltinOnly,
		Source:     "service_register",
	}
	if isBuiltinOnly {
		observed.HealthyInstanceCount = 1
		observed.Transport = "internal"
		observed.Endpoint = "hub://builtin"
		observed.Source = "hub_builtin"
	} else {
		activeCount := 0
		lastSeenAtMS := int64(0)
		for _, candidate := range candidates {
			if candidate.Enabled && candidate.Healthy {
				activeCount++
			}
			if candidate.LastSeenAtMS > lastSeenAtMS {
				lastSeenAtMS = candidate.LastSeenAtMS
			}
		}
		observed.HealthyInstanceCount = activeCount
		observed.LastSeenAtMS = lastSeenAtMS
		if hasBinding {
			for _, candidate := range candidates {
				if candidate.ServiceID == binding.ServiceID {
					observed.Transport = candidate.Transport
					observed.Endpoint = candidate.Endpoint
					break
				}
			}
		}
		if observed.Endpoint == "" && len(candidates) > 0 {
			observed.Transport = candidates[0].Transport
			observed.Endpoint = candidates[0].Endpoint
		}
	}

	governance := toolproto.ToolGovernance{
		Enabled: observed.Registered,
	}
	serviceID := ""
	if isBuiltinOnly {
		serviceID = "hub"
		governance.BoundServiceID = "hub"
		governance.BindingReason = "hub_builtin"
		governance.Reliability = "trusted"
	} else if hasBinding {
		serviceID = binding.ServiceID
		governance.BoundServiceID = binding.ServiceID
		governance.BindingReason = binding.Reason
		governance.ManualOverride = strings.TrimSpace(h.manualBind[toolID]) == binding.ServiceID
	}
	if serviceID == "" && len(candidates) > 0 {
		serviceID = candidates[0].ServiceID
	}
	if serviceID != "" && serviceID != "hub" {
		if reg, ok := h.services[serviceID]; ok {
			governance.Reliability = reg.Reliability
		}
		if stat := h.toolStatLocked(toolID, serviceID); stat != nil {
			callCount := stat.SuccessCount + stat.FailureCount
			governance.CallCount = callCount
			if callCount > 0 {
				governance.SuccessRate = float64(stat.SuccessCount) / float64(callCount)
			}
		}
	}
	if !isBuiltinOnly && len(candidates) == 0 {
		governance.Enabled = false
		governance.ConflictReason = "no_registered_provider"
	}
	if !isBuiltinOnly && len(candidates) > 0 && observed.HealthyInstanceCount == 0 {
		governance.Enabled = false
		governance.ConflictReason = "no_healthy_provider"
	}

	return HubToolView{
		ToolID:     toolID,
		ServiceID:  serviceID,
		Spec:       spec,
		Observed:   observed,
		Governance: governance,
		Candidates: candidates,
	}
}

func (h *HubPlatform) hasBuiltinToolLocked(toolID string) bool {
	for _, tool := range h.builtinTools {
		if strings.TrimSpace(tool.ToolID) == strings.TrimSpace(toolID) {
			return true
		}
	}
	return false
}

func (h *HubPlatform) toolStatLocked(toolID string, serviceID string) *HubToolProviderStat {
	if statsByTool, ok := h.stats[toolID]; ok {
		return statsByTool[serviceID]
	}
	return nil
}

func (h *HubPlatform) ResolveToolRoute(toolID string) (HubToolRoute, bool) {
	if h == nil {
		return HubToolRoute{}, false
	}
	tid := strings.TrimSpace(toolID)
	if tid == "" {
		return HubToolRoute{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	binding, ok := h.bindings[tid]
	if !ok {
		return HubToolRoute{}, false
	}
	service, ok := h.services[binding.ServiceID]
	if !ok {
		return HubToolRoute{}, false
	}
	if service.Status != ServiceStatusActive {
		return HubToolRoute{}, false
	}
	return HubToolRoute{
		Binding: binding,
		Service: service,
	}, true
}

func (h *HubPlatform) HasTool(toolID string) bool {
	if h == nil {
		return false
	}
	tid := strings.TrimSpace(toolID)
	if tid == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.toolProviders[tid]; ok {
		return true
	}
	for _, t := range h.builtinTools {
		if strings.TrimSpace(t.ToolID) == tid {
			return true
		}
	}
	return false
}

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

func (h *HubPlatform) governedReliabilityLocked(serviceID string) string {
	sid := strings.TrimSpace(serviceID)
	if sid == "" {
		return "unverified"
	}
	if value, ok := h.governance[sid]; ok {
		return normalizeReliability(value)
	}
	if reg, ok := h.services[sid]; ok {
		return normalizeReliability(reg.Reliability)
	}
	return "unverified"
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

func randomAuthToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate auth token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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

func EnsureServiceConfigFiles(serviceRoot string) error {
	_, err := hubsvc.EnsureProjectConfigFiles(serviceRoot)
	return err
}

func (h *HubPlatform) IssueOriginCallerToken(origin toolproto.Caller, serviceID string, requestID string, traceID string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("hub platform is nil")
	}
	claims := hubsvc.OriginCallerTokenClaims{
		OriginCaller:       origin,
		IssuedAtMS:         time.Now().UnixMilli(),
		ExpiresAtMS:        time.Now().Add(originCallerTokenTTL).UnixMilli(),
		IssuedForServiceID: strings.TrimSpace(serviceID),
		RequestID:          strings.TrimSpace(requestID),
		TraceID:            strings.TrimSpace(traceID),
	}
	return hubsvc.SignOriginCallerToken(h.originSecret, claims)
}

func (h *HubPlatform) VerifyOriginCallerToken(token string, expectedServiceID string) (hubsvc.OriginCallerTokenClaims, error) {
	if h == nil {
		return hubsvc.OriginCallerTokenClaims{}, fmt.Errorf("hub platform is nil")
	}
	claims, err := hubsvc.VerifyOriginCallerToken(h.originSecret, token)
	if err != nil {
		return hubsvc.OriginCallerTokenClaims{}, err
	}
	expected := strings.TrimSpace(expectedServiceID)
	if expected != "" && strings.TrimSpace(claims.IssuedForServiceID) != "" && strings.TrimSpace(claims.IssuedForServiceID) != expected {
		return hubsvc.OriginCallerTokenClaims{}, fmt.Errorf("origin caller token target mismatch")
	}
	return claims, nil
}
