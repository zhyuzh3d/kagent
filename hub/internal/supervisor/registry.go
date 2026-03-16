package supervisor

import (
	"sort"
	"strings"
	"sync"
	"time"

	app "kagent/hub/internal/app"
)

const (
	InstanceStatusStarting  = "starting"
	InstanceStatusReady     = "ready"
	InstanceStatusDraining  = "draining"
	InstanceStatusUnhealthy = "unhealthy"
	InstanceStatusDead      = "dead"
)

type Instance struct {
	InstanceID          string `json:"instance_id"`
	ServiceID           string `json:"service_id"`
	Status              string `json:"status"`
	Transport           string `json:"transport"`
	Endpoint            string `json:"endpoint"`
	Score               int    `json:"score"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	RegisteredAtMS      int64  `json:"registered_at_ms"`
	LastHeartbeatAtMS   int64  `json:"last_heartbeat_at_ms"`
	LastSuccessAtMS     int64  `json:"last_success_at_ms"`
	LastFailureAtMS     int64  `json:"last_failure_at_ms"`
}

type Registry struct {
	mu sync.RWMutex

	instances map[string]Instance
}

func NewRegistry() *Registry {
	return &Registry{
		instances: map[string]Instance{},
	}
}

func (r *Registry) UpsertFromServiceRegistration(reg app.HubServiceRegistration, transport string) Instance {
	if r == nil {
		return Instance{}
	}
	now := time.Now().UnixMilli()
	serviceID := strings.TrimSpace(reg.ServiceID)
	instanceID := strings.TrimSpace(reg.InstanceID)
	key := makeKey(serviceID, instanceID)
	if strings.TrimSpace(transport) == "" {
		transport = inferTransport(strings.TrimSpace(reg.Endpoint))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.instances[key]
	if !ok {
		existing = Instance{
			InstanceID:     instanceID,
			ServiceID:      serviceID,
			Status:         InstanceStatusReady,
			Transport:      strings.TrimSpace(transport),
			Endpoint:       strings.TrimSpace(reg.Endpoint),
			Score:          100,
			RegisteredAtMS: now,
		}
	}
	existing.ServiceID = serviceID
	existing.InstanceID = instanceID
	existing.Endpoint = strings.TrimSpace(reg.Endpoint)
	existing.Status = InstanceStatusReady
	existing.Transport = firstNonEmpty(strings.TrimSpace(transport), strings.TrimSpace(existing.Transport))
	existing.LastHeartbeatAtMS = now
	r.instances[key] = existing
	return existing
}

func (r *Registry) Heartbeat(serviceID string, instanceID string, status string) (Instance, bool) {
	if r == nil {
		return Instance{}, false
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	if sid == "" || iid == "" {
		return Instance{}, false
	}
	key := makeKey(sid, iid)
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.instances[key]
	if !ok {
		return Instance{}, false
	}
	existing.LastHeartbeatAtMS = time.Now().UnixMilli()
	nextStatus := normalizeStatus(status)
	if nextStatus != "" {
		existing.Status = nextStatus
	} else if existing.Status == "" {
		existing.Status = InstanceStatusReady
	}
	r.instances[key] = existing
	return existing, true
}

func (r *Registry) MarkSuccess(serviceID string, instanceID string) {
	if r == nil {
		return
	}
	key := makeKey(serviceID, instanceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.instances[key]
	if !ok {
		return
	}
	existing.LastSuccessAtMS = time.Now().UnixMilli()
	existing.ConsecutiveFailures = 0
	if existing.Status == InstanceStatusUnhealthy {
		existing.Status = InstanceStatusReady
	}
	r.instances[key] = existing
}

func (r *Registry) MarkFailure(serviceID string, instanceID string) {
	if r == nil {
		return
	}
	key := makeKey(serviceID, instanceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.instances[key]
	if !ok {
		return
	}
	existing.LastFailureAtMS = time.Now().UnixMilli()
	existing.ConsecutiveFailures++
	if existing.ConsecutiveFailures >= 3 {
		existing.Status = InstanceStatusUnhealthy
	}
	r.instances[key] = existing
}

func (r *Registry) MarkDraining(serviceID string, instanceID string) {
	if r == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, instance := range r.instances {
		if strings.TrimSpace(instance.ServiceID) != sid {
			continue
		}
		if iid != "" && strings.TrimSpace(instance.InstanceID) != iid {
			continue
		}
		instance.Status = InstanceStatusDraining
		r.instances[key] = instance
	}
}

func (r *Registry) MarkDead(serviceID string, instanceID string) {
	if r == nil {
		return
	}
	key := makeKey(serviceID, instanceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.instances[key]
	if !ok {
		return
	}
	instance.Status = InstanceStatusDead
	r.instances[key] = instance
}

func (r *Registry) Unregister(serviceID string, instanceID string) {
	if r == nil {
		return
	}
	sid := strings.TrimSpace(serviceID)
	iid := strings.TrimSpace(instanceID)
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, instance := range r.instances {
		if strings.TrimSpace(instance.ServiceID) != sid {
			continue
		}
		if iid != "" && strings.TrimSpace(instance.InstanceID) != iid {
			continue
		}
		delete(r.instances, key)
	}
}

func (r *Registry) GetByService(serviceID string) []Instance {
	if r == nil {
		return nil
	}
	sid := strings.TrimSpace(serviceID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Instance, 0, 2)
	for _, instance := range r.instances {
		if strings.TrimSpace(instance.ServiceID) != sid {
			continue
		}
		out = append(out, instance)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status == out[j].Status {
			if out[i].LastSuccessAtMS == out[j].LastSuccessAtMS {
				return out[i].InstanceID < out[j].InstanceID
			}
			return out[i].LastSuccessAtMS > out[j].LastSuccessAtMS
		}
		return statusRank(out[i].Status) < statusRank(out[j].Status)
	})
	return out
}

func (r *Registry) List() []Instance {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Instance, 0, len(r.instances))
	for _, instance := range r.instances {
		out = append(out, instance)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceID == out[j].ServiceID {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].ServiceID < out[j].ServiceID
	})
	return out
}

func makeKey(serviceID string, instanceID string) string {
	return strings.TrimSpace(serviceID) + "::" + strings.TrimSpace(instanceID)
}

func inferTransport(endpoint string) string {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return "tcp"
	}
	if strings.HasPrefix(ep, "unix://") || strings.HasPrefix(ep, "uds://") {
		return "uds"
	}
	if strings.HasPrefix(ep, "/") {
		return "uds"
	}
	return "tcp"
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case InstanceStatusStarting:
		return InstanceStatusStarting
	case InstanceStatusReady, "active":
		return InstanceStatusReady
	case InstanceStatusDraining:
		return InstanceStatusDraining
	case InstanceStatusUnhealthy:
		return InstanceStatusUnhealthy
	case InstanceStatusDead:
		return InstanceStatusDead
	default:
		return ""
	}
}

func statusRank(status string) int {
	switch normalizeStatus(status) {
	case InstanceStatusReady:
		return 0
	case InstanceStatusStarting:
		return 1
	case InstanceStatusDraining:
		return 2
	case InstanceStatusUnhealthy:
		return 3
	case InstanceStatusDead:
		return 4
	default:
		return 5
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
