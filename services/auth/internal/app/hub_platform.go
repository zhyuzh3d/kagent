package app

import (
	"crypto/hmac"
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
	TimeoutMSDefault     int            `json:"timeout_ms_default,omitempty"`
	Streaming            string         `json:"streaming,omitempty"`
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
		Reliability:    manifest.Reliability,
		Visibility:     manifest.Visibility,
		ManifestHash:   mh,
		ToolCount:      len(manifest.Provides),
		RegisteredAtMS: startedAt,
		LastSeenAtMS:   now,
		Status:         ServiceStatusActive,
		Manifest:       manifest,
	}

	h.mu.Lock()
	defer h.mu.Unlock()

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
	token, expMS, err := h.issueServiceSessionTokenLocked(manifest.ServiceID, instanceID, h.sessionTTL)
	if err != nil {
		return HubServiceRegisterResult{}, err
	}
	h.savePersistedStateLocked()
	return HubServiceRegisterResult{
		Registered: true,
		Service:    reg,
		Token:      token,
		ExpMS:      expMS,
	}, nil
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
	reg.ConflictReason = ""
	reg.LastSeenAtMS = nowMS()
	h.services[sid] = reg
	h.refreshBindingsLocked("service_up")
	h.savePersistedStateLocked()
}

func (h *HubPlatform) IssueServiceSessionToken(serviceID string, instanceID string, ttl time.Duration) (string, int64, error) {
	if h == nil {
		return "", 0, fmt.Errorf("hub is nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.issueServiceSessionTokenLocked(serviceID, instanceID, ttl)
}

func (h *HubPlatform) issueServiceSessionTokenLocked(serviceID string, instanceID string, ttl time.Duration) (string, int64, error) {
	if ttl <= 0 {
		ttl = h.sessionTTL
	}
	claims := HubServiceSessionClaims{
		ServiceID:  strings.TrimSpace(serviceID),
		InstanceID: strings.TrimSpace(instanceID),
		ExpMS:      nowMS() + ttl.Milliseconds(),
		Nonce:      newRequestID(),
	}
	if claims.ServiceID == "" || claims.InstanceID == "" {
		return "", 0, fmt.Errorf("service token requires service_id and instance_id")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", 0, err
	}
	raw := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.serviceSecret)
	_, _ = mac.Write([]byte(raw))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return raw + "." + sig, claims.ExpMS, nil
}

func (h *HubPlatform) VerifyServiceSessionToken(token string) (HubServiceSessionClaims, error) {
	if h == nil {
		return HubServiceSessionClaims{}, fmt.Errorf("hub is nil")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return HubServiceSessionClaims{}, fmt.Errorf("invalid service token")
	}
	raw := parts[0]
	sig := parts[1]
	mac := hmac.New(sha256.New, h.serviceSecret)
	_, _ = mac.Write([]byte(raw))
	expect := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expect), []byte(sig)) {
		return HubServiceSessionClaims{}, fmt.Errorf("invalid service token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return HubServiceSessionClaims{}, fmt.Errorf("decode service token payload: %w", err)
	}
	var claims HubServiceSessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return HubServiceSessionClaims{}, fmt.Errorf("decode service token claims: %w", err)
	}
	if claims.ExpMS <= nowMS() {
		return HubServiceSessionClaims{}, fmt.Errorf("service token expired")
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	reg, ok := h.services[claims.ServiceID]
	if !ok {
		return HubServiceSessionClaims{}, fmt.Errorf("service not registered")
	}
	if reg.InstanceID != claims.InstanceID {
		return HubServiceSessionClaims{}, fmt.Errorf("service instance mismatch")
	}
	if reg.Status != ServiceStatusActive {
		return HubServiceSessionClaims{}, fmt.Errorf("service is not active")
	}
	return claims, nil
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
				meta[toolID] = t
			}
		}
	}

	toolIDs := make([]string, 0, len(h.toolProviders))
	for tid := range h.toolProviders {
		toolIDs = append(toolIDs, tid)
	}
	sort.Strings(toolIDs)
	out := make([]HubToolView, 0, len(toolIDs))
	for _, tid := range toolIDs {
		providers := h.toolProviders[tid]
		cands := make([]HubToolProviderView, 0, len(providers))
		for _, sid := range providers {
			reg, ok := h.services[sid]
			if !ok {
				continue
			}
			stat := h.stats[tid][sid]
			successRate := 0.5
			lat := int64(0)
			mw := 0
			errRate := 0
			enabled := reg.Status == ServiceStatusActive
			if stat != nil {
				total := stat.SuccessCount + stat.FailureCount
				if total > 0 {
					successRate = float64(stat.SuccessCount) / float64(total)
				}
				lat = stat.LastLatencyMS
				mw = stat.ManualWeight
				errRate = stat.RecentErrorRate
				enabled = enabled && stat.Enabled
			}
			cands = append(cands, HubToolProviderView{
				ServiceID:     sid,
				ServiceName:   reg.ServiceName,
				Reliability:   reg.Reliability,
				Enabled:       enabled,
				SuccessRate:   successRate,
				P95LatencyMS:  lat,
				ManualWeight:  mw,
				LastErrorRate: errRate,
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
		toolMeta := meta[tid]
		out = append(out, HubToolView{
			ToolID:      tid,
			Category:    toolMeta.Category,
			Type:        toolMeta.Type,
			Tool:        toolMeta.Tool,
			Description: toolMeta.Description,
			Binding:     h.bindings[tid],
			Candidates:  cands,
		})
	}
	return out
}

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
	root := strings.TrimSpace(serviceRoot)
	if root == "" {
		return fmt.Errorf("service root is empty")
	}
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create service config dir: %w", err)
	}
	sampleFiles := map[string]string{
		filepath.Join(configDir, "config.json"):          "{\n  \"service\": {}\n}\n",
		filepath.Join(configDir, "configx.json.example"): "{\n  \"secrets\": {\n    \"token\": \"\"\n  }\n}\n",
	}
	for path, content := range sampleFiles {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write service config sample %s: %w", path, err)
		}
	}
	return nil
}
