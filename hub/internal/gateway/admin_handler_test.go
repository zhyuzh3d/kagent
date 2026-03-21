package gateway

import (
	"testing"

	app "kagent/hub/internal/app"
	"kagent/hub/internal/supervisor"
)

func TestFlattenServiceListItemRemovesRuntimeNesting(t *testing.T) {
	t.Parallel()

	info := supervisor.ManagedServiceInfo{
		ServiceID:   "account",
		Description: "Account service",
		Dir:         "services/account",
		Registered:  true,
		Active:      true,
		Healthy:     true,
		Status:      "active",
		InstanceID:  "account-1",
		Endpoint:    "http://127.0.0.1:18083",
		PID:         1234,
		RegisteredManifest: &app.ServiceManifest{
			ServiceID:   "account",
			ServiceName: "account",
			Version:     "1.0.0",
		},
	}

	got := flattenServiceListItem(info)
	if got.ServiceID != "account" {
		t.Fatalf("unexpected service_id: %q", got.ServiceID)
	}
	if got.Status != "active" {
		t.Fatalf("unexpected status: %q", got.Status)
	}
	if got.InstanceID != "account-1" || got.Endpoint != "http://127.0.0.1:18083" || got.PID != 1234 {
		t.Fatalf("unexpected flattened runtime fields: %+v", got)
	}
	if got.RegisteredManifest == nil || got.RegisteredManifest.ServiceID != "account" {
		t.Fatalf("registered manifest not preserved: %+v", got.RegisteredManifest)
	}
}
