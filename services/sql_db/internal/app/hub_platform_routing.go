package app

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

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
