package app

import "testing"

func TestRegisterServiceUsesGovernedReliabilityAndRoutingPrefersHigherLevel(t *testing.T) {
	t.Parallel()

	hub, err := NewHubPlatform(t.TempDir())
	if err != nil {
		t.Fatalf("new hub platform: %v", err)
	}
	hub.SetServiceReliability("svc_trusted", "trusted")
	hub.SetServiceReliability("svc_risky", "risky")

	register := func(serviceID string) {
		_, err := hub.RegisterService(HubServiceRegisterRequest{
			Manifest: ServiceManifest{
				ServiceID:   serviceID,
				ServiceName: serviceID,
				Version:     "1.0.0",
				Visibility:  "public",
				Provides: []ServiceToolDescriptor{
					{ToolID: "demo.echo", Description: "echo"},
				},
			},
			InstanceID: serviceID + "@1",
			Endpoint:   "http://127.0.0.1:18080",
		})
		if err != nil {
			t.Fatalf("register %s: %v", serviceID, err)
		}
	}

	register("svc_risky")
	register("svc_trusted")

	trusted, ok := hub.GetService("svc_trusted")
	if !ok {
		t.Fatal("expected svc_trusted registered")
	}
	if trusted.Reliability != "trusted" {
		t.Fatalf("unexpected trusted reliability: %q", trusted.Reliability)
	}

	bindings := hub.ListBindings()
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].ToolID != "demo.echo" {
		t.Fatalf("unexpected tool binding: %+v", bindings[0])
	}
	if bindings[0].ServiceID != "svc_trusted" {
		t.Fatalf("expected trusted service to win routing, got %s", bindings[0].ServiceID)
	}
}
