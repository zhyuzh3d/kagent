package routing

import (
	"testing"
	"time"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
)

func TestBuildMetadataSchema(t *testing.T) {
	engine := NewEngine()
	services := []app.HubServiceRegistration{
		{
			ServiceID:   "sql_db",
			Status:      app.ServiceStatusActive,
			Version:     "1.2.3",
			Reliability: "verified",
			Visibility:  "internal",
			Manifest: app.ServiceManifest{
				Provides: []app.ServiceToolDescriptor{
					{
						ToolID:               "storage.database.query",
						TimeoutMSDefault:     15000,
						CapabilitiesRequired: []string{"storage.database.read"},
						InputSchema:          map[string]any{"type": "object"},
						OutputSchema:         map[string]any{"type": "object"},
					},
				},
			},
		},
	}
	instances := []supervisor.Instance{
		{
			ServiceID:         "sql_db",
			InstanceID:        "sql_db@local#1",
			Status:            supervisor.InstanceStatusReady,
			Healthy:           true,
			Transport:         "uds",
			Endpoint:          "/tmp/kagent-database.sock",
			Score:             100,
			LastHeartbeatAtMS: 11,
			LastSuccessAtMS:   22,
			LastFailureAtMS:   33,
		},
	}
	engine.SyncServices(services)
	selection, ok := engine.Select("storage.database.query", services, instances)
	if !ok {
		t.Fatalf("expected route available")
	}
	engine.Record(selection, "req-1", "tr-1", "user", "u_1", true, "", 12*time.Millisecond)

	schema := engine.BuildMetadataSchema(services, instances, 20)
	if len(schema.ToolRegistry) != 1 {
		t.Fatalf("expected one tool_registry item, got %d", len(schema.ToolRegistry))
	}
	if schema.ToolRegistry[0].ToolID != "storage.database.query" {
		t.Fatalf("unexpected tool id: %s", schema.ToolRegistry[0].ToolID)
	}
	if schema.ToolRegistry[0].OwnerServiceID != "sql_db" {
		t.Fatalf("unexpected owner service: %s", schema.ToolRegistry[0].OwnerServiceID)
	}
	if schema.ToolRegistry[0].InputSchemaRef == "" || schema.ToolRegistry[0].OutputSchemaRef == "" {
		t.Fatalf("expected inline schema refs")
	}

	if len(schema.ServiceRegistry) != 1 {
		t.Fatalf("expected one service_registry item, got %d", len(schema.ServiceRegistry))
	}
	if schema.ServiceRegistry[0].ServiceID != "sql_db" {
		t.Fatalf("unexpected service id: %s", schema.ServiceRegistry[0].ServiceID)
	}
	if schema.ServiceRegistry[0].TransportPreference != "uds" {
		t.Fatalf("expected uds preference, got %s", schema.ServiceRegistry[0].TransportPreference)
	}
	if !schema.ServiceRegistry[0].Enabled {
		t.Fatalf("expected service enabled")
	}

	if len(schema.InstanceRegistry) != 1 {
		t.Fatalf("expected one instance_registry item, got %d", len(schema.InstanceRegistry))
	}
	if schema.InstanceRegistry[0].InstanceID != "sql_db@local#1" {
		t.Fatalf("unexpected instance id: %s", schema.InstanceRegistry[0].InstanceID)
	}
	if schema.InstanceRegistry[0].Transport != "uds" {
		t.Fatalf("expected uds transport, got %s", schema.InstanceRegistry[0].Transport)
	}

	if len(schema.ToolRouteBinding) != 1 {
		t.Fatalf("expected one tool_route_binding item, got %d", len(schema.ToolRouteBinding))
	}
	if len(schema.ToolCallAudit) != 1 {
		t.Fatalf("expected one tool_call_audit item, got %d", len(schema.ToolCallAudit))
	}
	if schema.ToolCallAudit[0].RequestID != "req-1" {
		t.Fatalf("unexpected audit request id: %s", schema.ToolCallAudit[0].RequestID)
	}
}
