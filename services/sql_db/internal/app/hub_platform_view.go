package app

import (
	"sort"
	"strings"
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
