package supervisor

import (
	"testing"
	"time"

	app "kagent/hub/internal/app"
)

func TestLifecycleStateTransitions(t *testing.T) {
	reg := NewRegistry()
	healthy := true
	item := reg.UpsertFromServiceRegistration(app.HubServiceRegistration{
		ServiceID:  "sql_db",
		InstanceID: "sql_db@local#1",
		Endpoint:   "http://127.0.0.1:18085",
		Healthy:    true,
	}, "tcp", InstanceStatusReady)
	if item.Status != InstanceStatusReady {
		t.Fatalf("expected ready status, got %s", item.Status)
	}
	updated, ok := reg.Heartbeat("sql_db", "sql_db@local#1", "ready", &healthy)
	if !ok {
		t.Fatalf("expected heartbeat accepted")
	}
	if updated.LastHeartbeatAtMS <= 0 {
		t.Fatalf("expected heartbeat timestamp")
	}

	reg.MarkFailure("sql_db", "sql_db@local#1")
	reg.MarkFailure("sql_db", "sql_db@local#1")
	reg.MarkFailure("sql_db", "sql_db@local#1")
	list := reg.GetByService("sql_db")
	if len(list) != 1 {
		t.Fatalf("expected one instance")
	}
	if list[0].Status != InstanceStatusUnhealthy {
		t.Fatalf("expected unhealthy after consecutive failures, got %s", list[0].Status)
	}
	reg.MarkSuccess("sql_db", "sql_db@local#1")
	list = reg.GetByService("sql_db")
	if list[0].Status != InstanceStatusReady {
		t.Fatalf("expected ready after success, got %s", list[0].Status)
	}

	reg.MarkDraining("sql_db", "sql_db@local#1")
	list = reg.GetByService("sql_db")
	if list[0].Status != InstanceStatusDraining {
		t.Fatalf("expected draining status, got %s", list[0].Status)
	}

	time.Sleep(5 * time.Millisecond)
	reg.Unregister("sql_db", "sql_db@local#1")
	list = reg.GetByService("sql_db")
	if len(list) != 0 {
		t.Fatalf("expected no instances after unregister")
	}
}
