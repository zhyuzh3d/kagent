package supervisor

import (
	"slices"
	"strings"
)

type startupPlan struct {
	layers      [][]string
	missingDeps map[string][]string
	cyclic      map[string]struct{}
}

func buildStartupPlan(services map[string]*managedService) startupPlan {
	adjacency := map[string][]string{}
	indegree := map[string]int{}
	missingDeps := map[string][]string{}
	for sid := range services {
		indegree[sid] = 0
	}
	for sid, svc := range services {
		for _, dep := range svc.dependsOn {
			if _, ok := services[dep]; !ok {
				missingDeps[sid] = append(missingDeps[sid], dep)
				continue
			}
			adjacency[dep] = append(adjacency[dep], sid)
			indegree[sid]++
		}
	}
	ready := make([]string, 0, len(indegree))
	for sid, degree := range indegree {
		if degree == 0 {
			ready = append(ready, sid)
		}
	}
	slices.Sort(ready)

	layers := make([][]string, 0, len(ready))
	processed := map[string]struct{}{}
	for len(ready) > 0 {
		layer := append([]string(nil), ready...)
		layers = append(layers, layer)
		nextSet := map[string]struct{}{}
		for _, sid := range layer {
			processed[sid] = struct{}{}
			for _, dependent := range adjacency[sid] {
				indegree[dependent]--
				if indegree[dependent] == 0 {
					nextSet[dependent] = struct{}{}
				}
			}
		}
		next := make([]string, 0, len(nextSet))
		for sid := range nextSet {
			next = append(next, sid)
		}
		slices.Sort(next)
		ready = next
	}

	cyclic := map[string]struct{}{}
	for sid := range services {
		if _, ok := processed[sid]; ok {
			continue
		}
		cyclic[sid] = struct{}{}
	}
	for sid, deps := range missingDeps {
		slices.Sort(deps)
		missingDeps[sid] = deps
	}
	return startupPlan{
		layers:      layers,
		missingDeps: missingDeps,
		cyclic:      cyclic,
	}
}

func firstFailedDependency(outcomes []StartupServiceOutcome, indexByService map[string]int, deps []string) string {
	for _, dep := range deps {
		idx, ok := indexByService[dep]
		if !ok {
			return dep
		}
		if !outcomes[idx].Ready {
			return dep
		}
	}
	return ""
}

func normalizeDependsOn(serviceID string, values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	self := strings.TrimSpace(serviceID)
	for _, value := range values {
		clean := strings.TrimSpace(value)
		if clean == "" || clean == self {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil
	}
	return out
}
