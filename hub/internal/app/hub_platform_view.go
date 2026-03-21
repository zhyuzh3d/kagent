package app

import (
	"sort"
	"strings"

	"kagent/pkg/toolproto"
)

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
