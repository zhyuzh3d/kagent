package app

import (
	"fmt"
	"strings"
)

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
