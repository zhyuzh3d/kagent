package supervisor

import (
	"testing"
	"time"

	app "kagent/hub/internal/app"
)

func TestLifecycleStateTransitions(t *testing.T) {
	reg := NewRegistry()
	item := reg.UpsertFromServiceRegistration(app.HubServiceRegistration{
		ServiceID:  "database",
		InstanceID: "database@local#1",
		Endpoint:   "http://127.0.0.1:18085",
	}, "tcp")
	if item.Status != InstanceStatusReady {
		t.Fatalf("expected ready status, got %s", item.Status)
	}
	updated, ok := reg.Heartbeat("database", "database@local#1", "ready")
	if !ok {
		t.Fatalf("expected heartbeat accepted")
	}
	if updated.LastHeartbeatAtMS <= 0 {
		t.Fatalf("expected heartbeat timestamp")
	}

	reg.MarkFailure("database", "database@local#1")
	reg.MarkFailure("database", "database@local#1")
	reg.MarkFailure("database", "database@local#1")
	list := reg.GetByService("database")
	if len(list) != 1 {
		t.Fatalf("expected one instance")
	}
	if list[0].Status != InstanceStatusUnhealthy {
		t.Fatalf("expected unhealthy after consecutive failures, got %s", list[0].Status)
	}
	reg.MarkSuccess("database", "database@local#1")
	list = reg.GetByService("database")
	if list[0].Status != InstanceStatusReady {
		t.Fatalf("expected ready after success, got %s", list[0].Status)
	}

	reg.MarkDraining("database", "database@local#1")
	list = reg.GetByService("database")
	if list[0].Status != InstanceStatusDraining {
		t.Fatalf("expected draining status, got %s", list[0].Status)
	}

	time.Sleep(5 * time.Millisecond)
	reg.Unregister("database", "database@local#1")
	list = reg.GetByService("database")
	if len(list) != 0 {
		t.Fatalf("expected no instances after unregister")
	}
}
