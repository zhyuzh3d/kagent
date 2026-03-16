package routing

import (
	"sort"
	"strings"
	"sync"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
)

const (
	BindingModeManual     = "manual"
	BindingModeDiscovered = "discovered"

	CircuitClosed   = "closed"
	CircuitOpen     = "open"
	CircuitHalfOpen = "half_open"
)

type Binding struct {
	ToolID           string `json:"tool_id"`
	ServiceID        string `json:"service_id"`
	BindingMode      string `json:"binding_mode"`
	Priority         int    `json:"priority"`
	Weight           int    `json:"weight"`
	Enabled          bool   `json:"enabled"`
	CircuitState     string `json:"circuit_state"`
	FailureWindowSec int    `json:"failure_window_sec"`
	FailureThreshold int    `json:"failure_threshold"`
	RecoverAfterSec  int    `json:"recover_after_sec"`

	LastFailureAtMS int64 `json:"last_failure_at_ms"`
	LastSuccessAtMS int64 `json:"last_success_at_ms"`
}

type CallAudit struct {
	RequestID  string `json:"request_id"`
	TraceID    string `json:"trace_id"`
	ToolID     string `json:"tool_id"`
	ServiceID  string `json:"service_id"`
	InstanceID string `json:"instance_id"`
	CallerType string `json:"caller_type"`
	CallerID   string `json:"caller_id"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	ErrorCode  string `json:"error_code,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

type Selection struct {
	ToolID    string                     `json:"tool_id"`
	Binding   Binding                    `json:"binding"`
	Service   app.HubServiceRegistration `json:"service"`
	Instance  supervisor.Instance        `json:"instance"`
	Score     int                        `json:"score"`
	Available []Binding                  `json:"available"`
}

type providerStat struct {
	SuccessCount         int64
	FailureCount         int64
	ConsecutiveFailures  int64
	LastLatencyMS        int64
	TotalLatencyMS       int64
	LastSuccessAtMS      int64
	LastFailureAtMS      int64
	RecentFailureSamples []int64
}

type Engine struct {
	mu sync.RWMutex

	manual map[string]string

	bindings map[string]map[string]*Binding
	stats    map[string]map[string]*providerStat
	audits   []CallAudit

	maxAudits int
}

func NewEngine() *Engine {
	return &Engine{
		manual:    map[string]string{},
		bindings:  map[string]map[string]*Binding{},
		stats:     map[string]map[string]*providerStat{},
		audits:    make([]CallAudit, 0, 512),
		maxAudits: 3000,
	}
}

func (e *Engine) SetManualBinding(toolID string, serviceID string) {
	if e == nil {
		return
	}
	tid := strings.TrimSpace(toolID)
	sid := strings.TrimSpace(serviceID)
	if tid == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if sid == "" {
		delete(e.manual, tid)
		return
	}
	e.manual[tid] = sid
}

func (e *Engine) SyncServices(services []app.HubServiceRegistration) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	seen := map[string]map[string]struct{}{}
	for _, service := range services {
		if strings.TrimSpace(service.ServiceID) == "" {
			continue
		}
		for _, tool := range service.Manifest.Provides {
			toolID := strings.TrimSpace(tool.ToolID)
			if toolID == "" {
				continue
			}
			if _, ok := seen[toolID]; !ok {
				seen[toolID] = map[string]struct{}{}
			}
			seen[toolID][service.ServiceID] = struct{}{}
			if _, ok := e.bindings[toolID]; !ok {
				e.bindings[toolID] = map[string]*Binding{}
			}
			if _, ok := e.bindings[toolID][service.ServiceID]; !ok {
				mode := BindingModeDiscovered
				if strings.TrimSpace(e.manual[toolID]) == strings.TrimSpace(service.ServiceID) {
					mode = BindingModeManual
				}
				e.bindings[toolID][service.ServiceID] = &Binding{
					ToolID:           toolID,
					ServiceID:        service.ServiceID,
					BindingMode:      mode,
					Priority:         100,
					Weight:           100,
					Enabled:          true,
					CircuitState:     CircuitClosed,
					FailureWindowSec: 60,
					FailureThreshold: 5,
					RecoverAfterSec:  15,
				}
			}
		}
	}
	for toolID, byService := range e.bindings {
		seenService := seen[toolID]
		for serviceID := range byService {
			if _, ok := seenService[serviceID]; ok {
				continue
			}
			delete(byService, serviceID)
		}
		if len(byService) == 0 {
			delete(e.bindings, toolID)
			delete(e.manual, toolID)
		}
	}
}

func (e *Engine) Select(toolID string, services []app.HubServiceRegistration, instances []supervisor.Instance) (Selection, bool) {
	if e == nil {
		return Selection{}, false
	}
	tid := strings.TrimSpace(toolID)
	if tid == "" {
		return Selection{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	byService, ok := e.bindings[tid]
	if !ok || len(byService) == 0 {
		return Selection{}, false
	}
	serviceMap := map[string]app.HubServiceRegistration{}
	for _, service := range services {
		serviceMap[strings.TrimSpace(service.ServiceID)] = service
	}
	instanceMap := map[string][]supervisor.Instance{}
	for _, instance := range instances {
		sid := strings.TrimSpace(instance.ServiceID)
		instanceMap[sid] = append(instanceMap[sid], instance)
	}
	for sid := range instanceMap {
		sort.Slice(instanceMap[sid], func(i, j int) bool {
			if instanceMap[sid][i].LastSuccessAtMS == instanceMap[sid][j].LastSuccessAtMS {
				return instanceMap[sid][i].InstanceID < instanceMap[sid][j].InstanceID
			}
			return instanceMap[sid][i].LastSuccessAtMS > instanceMap[sid][j].LastSuccessAtMS
		})
	}

	candidates := make([]Selection, 0, len(byService))
	nowMS := time.Now().UnixMilli()
	manualService := strings.TrimSpace(e.manual[tid])

	for serviceID, bindingPtr := range byService {
		if bindingPtr == nil {
			continue
		}
		binding := *bindingPtr
		if !binding.Enabled {
			continue
		}
		service, ok := serviceMap[serviceID]
		if !ok {
			continue
		}
		if strings.TrimSpace(service.Status) != app.ServiceStatusActive {
			continue
		}
		if binding.CircuitState == CircuitOpen && nowMS < binding.LastFailureAtMS+int64(binding.RecoverAfterSec)*1000 {
			continue
		}
		if binding.CircuitState == CircuitOpen && nowMS >= binding.LastFailureAtMS+int64(binding.RecoverAfterSec)*1000 {
			binding.CircuitState = CircuitHalfOpen
			*bindingPtr = binding
		}
		insList := instanceMap[serviceID]
		if len(insList) == 0 {
			continue
		}
		selectedInstance, hasReady := pickReadyInstance(insList)
		if !hasReady {
			continue
		}
		score := e.computeScoreLocked(tid, serviceID, binding.Weight)
		candidates = append(candidates, Selection{
			ToolID:   tid,
			Binding:  binding,
			Service:  service,
			Instance: selectedInstance,
			Score:    score,
		})
	}
	if len(candidates) == 0 {
		return Selection{}, false
	}

	manualSet := make([]Selection, 0, len(candidates))
	if manualService != "" {
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.Service.ServiceID) == manualService {
				candidate.Binding.BindingMode = BindingModeManual
				manualSet = append(manualSet, candidate)
			}
		}
	}
	if len(manualSet) > 0 {
		candidates = manualSet
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Binding.Priority != candidates[j].Binding.Priority {
			return candidates[i].Binding.Priority > candidates[j].Binding.Priority
		}
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Binding.Weight != candidates[j].Binding.Weight {
			return candidates[i].Binding.Weight > candidates[j].Binding.Weight
		}
		if candidates[i].Binding.LastSuccessAtMS != candidates[j].Binding.LastSuccessAtMS {
			return candidates[i].Binding.LastSuccessAtMS > candidates[j].Binding.LastSuccessAtMS
		}
		if candidates[i].Instance.LastSuccessAtMS != candidates[j].Instance.LastSuccessAtMS {
			return candidates[i].Instance.LastSuccessAtMS > candidates[j].Instance.LastSuccessAtMS
		}
		return candidates[i].Service.ServiceID < candidates[j].Service.ServiceID
	})

	selected := candidates[0]
	available := make([]Binding, 0, len(candidates))
	for _, candidate := range candidates {
		available = append(available, candidate.Binding)
	}
	selected.Available = available
	return selected, true
}

func (e *Engine) Record(selection Selection, requestID string, traceID string, callerType string, callerID string, ok bool, errorCode string, duration time.Duration) {
	if e == nil {
		return
	}
	tid := strings.TrimSpace(selection.ToolID)
	sid := strings.TrimSpace(selection.Service.ServiceID)
	if tid == "" || sid == "" {
		return
	}
	now := time.Now().UnixMilli()

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.stats[tid]; !ok {
		e.stats[tid] = map[string]*providerStat{}
	}
	stat, okStat := e.stats[tid][sid]
	if !okStat {
		stat = &providerStat{}
		e.stats[tid][sid] = stat
	}
	ms := duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	stat.LastLatencyMS = ms
	stat.TotalLatencyMS += ms
	if ok {
		stat.SuccessCount++
		stat.ConsecutiveFailures = 0
		stat.LastSuccessAtMS = now
	} else {
		stat.FailureCount++
		stat.ConsecutiveFailures++
		stat.LastFailureAtMS = now
		stat.RecentFailureSamples = append(stat.RecentFailureSamples, now)
		if len(stat.RecentFailureSamples) > 100 {
			stat.RecentFailureSamples = stat.RecentFailureSamples[len(stat.RecentFailureSamples)-100:]
		}
	}
	binding := e.bindings[tid][sid]
	if binding != nil {
		if ok {
			binding.LastSuccessAtMS = now
			if binding.CircuitState == CircuitHalfOpen || binding.CircuitState == CircuitOpen {
				binding.CircuitState = CircuitClosed
			}
		} else {
			binding.LastFailureAtMS = now
			failuresInWindow := 0
			windowStart := now - int64(binding.FailureWindowSec)*1000
			for _, ts := range stat.RecentFailureSamples {
				if ts >= windowStart {
					failuresInWindow++
				}
			}
			if failuresInWindow >= binding.FailureThreshold {
				binding.CircuitState = CircuitOpen
			}
		}
	}

	status := "ok"
	if !ok {
		status = "error"
	}
	record := CallAudit{
		RequestID:  strings.TrimSpace(requestID),
		TraceID:    strings.TrimSpace(traceID),
		ToolID:     tid,
		ServiceID:  sid,
		InstanceID: strings.TrimSpace(selection.Instance.InstanceID),
		CallerType: strings.TrimSpace(callerType),
		CallerID:   strings.TrimSpace(callerID),
		Status:     status,
		DurationMS: ms,
		ErrorCode:  strings.TrimSpace(errorCode),
		CreatedAt:  now,
	}
	e.audits = append(e.audits, record)
	if len(e.audits) > e.maxAudits {
		e.audits = e.audits[len(e.audits)-e.maxAudits:]
	}
}

func (e *Engine) ListAudits(limit int) []CallAudit {
	if e == nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.audits) == 0 {
		return nil
	}
	if limit > len(e.audits) {
		limit = len(e.audits)
	}
	out := make([]CallAudit, 0, limit)
	start := len(e.audits) - limit
	for i := len(e.audits) - 1; i >= start; i-- {
		out = append(out, e.audits[i])
	}
	return out
}

func (e *Engine) HasTool(toolID string) bool {
	if e == nil {
		return false
	}
	tid := strings.TrimSpace(toolID)
	if tid == "" {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	byService, ok := e.bindings[tid]
	return ok && len(byService) > 0
}

func (e *Engine) ListBindings() []Binding {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Binding, 0, 64)
	for _, byService := range e.bindings {
		for _, binding := range byService {
			if binding == nil {
				continue
			}
			out = append(out, *binding)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ToolID == out[j].ToolID {
			return out[i].ServiceID < out[j].ServiceID
		}
		return out[i].ToolID < out[j].ToolID
	})
	return out
}

func (e *Engine) computeScoreLocked(toolID string, serviceID string, baseWeight int) int {
	score := baseWeight
	stat := e.stats[toolID][serviceID]
	if stat == nil {
		return score
	}
	total := stat.SuccessCount + stat.FailureCount
	if total > 0 {
		successRate := float64(stat.SuccessCount) / float64(total)
		successBonus := int(successRate * 30)
		score += successBonus
		failurePenalty := int(stat.ConsecutiveFailures * 8)
		score -= failurePenalty
		avgLatency := float64(stat.TotalLatencyMS) / float64(total)
		latencyPenalty := int(avgLatency / 120)
		score -= latencyPenalty
	}
	return score
}

func pickReadyInstance(instances []supervisor.Instance) (supervisor.Instance, bool) {
	for _, instance := range instances {
		if strings.TrimSpace(instance.Status) == supervisor.InstanceStatusReady {
			return instance, true
		}
	}
	return supervisor.Instance{}, false
}
