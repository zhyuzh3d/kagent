package routing

import (
	"testing"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
)

func TestSelectPrefersHigherScore(t *testing.T) {
	engine := NewEngine()
	services := []app.HubServiceRegistration{
		{
			ServiceID:  "svc-a",
			Status:     app.ServiceStatusActive,
			InstanceID: "a1",
			Manifest: app.ServiceManifest{
				Provides: []app.ServiceToolDescriptor{{ToolID: "app.chat.project_list"}},
			},
		},
		{
			ServiceID:  "svc-b",
			Status:     app.ServiceStatusActive,
			InstanceID: "b1",
			Manifest: app.ServiceManifest{
				Provides: []app.ServiceToolDescriptor{{ToolID: "app.chat.project_list"}},
			},
		},
	}
	instances := []supervisor.Instance{
		{ServiceID: "svc-a", InstanceID: "a1", Status: supervisor.InstanceStatusReady},
		{ServiceID: "svc-b", InstanceID: "b1", Status: supervisor.InstanceStatusReady},
	}
	engine.SyncServices(services)
	selection, ok := engine.Select("app.chat.project_list", services, instances)
	if !ok {
		t.Fatalf("expected route available")
	}
	engine.Record(selection, "req-1", "tr-1", "user", "u1", false, "INTERNAL_ERROR", 20*time.Millisecond)
	engine.Record(selection, "req-2", "tr-2", "user", "u1", false, "INTERNAL_ERROR", 20*time.Millisecond)

	for i := 0; i < 20; i++ {
		engine.Record(Selection{
			ToolID: "app.chat.project_list",
			Service: app.HubServiceRegistration{
				ServiceID: "svc-b",
			},
			Instance: supervisor.Instance{
				InstanceID: "b1",
			},
		}, "req-ok", "tr-ok", "user", "u1", true, "", 5*time.Millisecond)
	}
	next, ok := engine.Select("app.chat.project_list", services, instances)
	if !ok {
		t.Fatalf("expected route available after stats")
	}
	if next.Service.ServiceID != "svc-b" {
		t.Fatalf("expected svc-b to be selected, got %s", next.Service.ServiceID)
	}
}

func TestCircuitOpenAfterFailures(t *testing.T) {
	engine := NewEngine()
	services := []app.HubServiceRegistration{
		{
			ServiceID:  "svc-a",
			Status:     app.ServiceStatusActive,
			InstanceID: "a1",
			Manifest: app.ServiceManifest{
				Provides: []app.ServiceToolDescriptor{{ToolID: "storage.database.query"}},
			},
		},
	}
	instances := []supervisor.Instance{
		{ServiceID: "svc-a", InstanceID: "a1", Status: supervisor.InstanceStatusReady},
	}
	engine.SyncServices(services)
	selection, ok := engine.Select("storage.database.query", services, instances)
	if !ok {
		t.Fatalf("expected initial route")
	}
	for i := 0; i < 6; i++ {
		engine.Record(selection, "req", "tr", "user", "u1", false, "TOOL_EXEC_ERROR", 10*time.Millisecond)
	}
	_, ok = engine.Select("storage.database.query", services, instances)
	if ok {
		t.Fatalf("expected route unavailable due to open circuit")
	}
}
