package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	app "kagent/hub/internal/app"
)

func TestListManagedServicesDoesNotDeadlockWhenManifestExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	manifest := `{
  "service_id": "demo",
  "description": "Demo service",
  "entry": { "args": ["demo-bin"] },
  "lifecycle": {}
}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	hubPlatform, err := app.NewHubPlatform(tmpDir)
	if err != nil {
		t.Fatalf("new hub platform: %v", err)
	}

	manager := &LifecycleManager{
		hubPlatform: hubPlatform,
		services: []managedService{
			{
				entry: LifecycleServiceEntry{
					ServiceID: "demo",
					Dir:       "services/demo",
					Enabled:   true,
				},
				dirAbs:          filepath.Join(tmpDir, "services", "demo"),
				execPath:        filepath.Join(tmpDir, "services", "demo", "run", "demo"),
				startupManifest: manifestPath,
				runtimeManifest: filepath.Join(tmpDir, "services", "demo", "run", "manifest_runtime.json"),
				enabled:         true,
			},
		},
		procs: map[string]*managedProcess{},
	}

	done := make(chan []ManagedServiceInfo, 1)
	go func() {
		done <- manager.ListManagedServices()
	}()

	select {
	case got := <-done:
		if len(got) != 1 {
			t.Fatalf("expected 1 service, got %d", len(got))
		}
		if got[0].Description != "Demo service" {
			t.Fatalf("unexpected description: %q", got[0].Description)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListManagedServices timed out, likely due to recursive locking")
	}
}

func TestWriteConfigJSONSyncsProjectAndRunConfig(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	serviceRoot := filepath.Join(tmpDir, "services", "demo")
	if err := os.MkdirAll(filepath.Join(serviceRoot, "config"), 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}

	manager := &LifecycleManager{
		services: []managedService{
			{
				entry: LifecycleServiceEntry{
					ServiceID: "demo",
					Dir:       "services/demo",
					Enabled:   true,
				},
				dirAbs: serviceRoot,
			},
		},
	}

	want := map[string]any{
		"enabled": true,
		"nested": map[string]any{
			"value": "sync",
		},
	}

	path, err := manager.WriteConfigJSON("demo", want)
	if err != nil {
		t.Fatalf("WriteConfigJSON failed: %v", err)
	}
	if got := path; got != filepath.Join(serviceRoot, "config", "config.json") {
		t.Fatalf("unexpected config path: %s", got)
	}

	for _, target := range []string{
		filepath.Join(serviceRoot, "config", "config.json"),
		filepath.Join(serviceRoot, "run", "config", "config.json"),
	} {
		raw, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("read %s: %v", target, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode %s: %v", target, err)
		}
		if got["enabled"] != true {
			t.Fatalf("unexpected enabled in %s: %#v", target, got["enabled"])
		}
		nested, ok := got["nested"].(map[string]any)
		if !ok || nested["value"] != "sync" {
			t.Fatalf("unexpected nested payload in %s: %#v", target, got["nested"])
		}
	}
}

func TestUpdateServiceGovernancePersistsReliabilityAndSyncsHubPlatform(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "hub", "config", "services.json")
	hubPlatform, err := app.NewHubPlatform(tmpDir)
	if err != nil {
		t.Fatalf("new hub platform: %v", err)
	}

	manager := &LifecycleManager{
		configPath:  configPath,
		hubPlatform: hubPlatform,
		global: LifecycleGlobalConfig{
			MaxTimeoutMS:    5000,
			MaxRestartTimes: 10,
			GracePeriodMS:   1000,
		},
		defaults: LifecycleDefaultConfig{
			RegisterTimeoutMS: 1000,
			RestartPolicy:     "never",
			RestartBackoffMS:  300,
		},
		services: []managedService{
			{
				entry: LifecycleServiceEntry{
					ServiceID:   "demo",
					Dir:         "services/demo",
					Enabled:     true,
					Reliability: "verified",
				},
				enabled:     true,
				reliability: "verified",
			},
		},
	}

	hubPlatform.SetServiceReliability("demo", "verified")
	if _, err := hubPlatform.RegisterService(app.HubServiceRegisterRequest{
		Manifest: app.ServiceManifest{
			ServiceID:   "demo",
			ServiceName: "demo",
			Provides: []app.ServiceToolDescriptor{
				{ToolID: "demo.echo", Description: "echo"},
			},
		},
		InstanceID: "demo@1",
		Endpoint:   "http://127.0.0.1:18081",
		Healthy:    boolPtr(true),
	}); err != nil {
		t.Fatalf("register service: %v", err)
	}

	if err := manager.UpdateServiceGovernance("demo", false, "high_risk"); err != nil {
		t.Fatalf("UpdateServiceGovernance failed: %v", err)
	}
	if manager.services[0].entry.Reliability != "high_risk" {
		t.Fatalf("unexpected in-memory reliability: %q", manager.services[0].entry.Reliability)
	}
	if manager.services[0].enabled {
		t.Fatal("expected service enabled=false after update")
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg LifecycleConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if got := cfg.Service.Services[0].Reliability; got != "high_risk" {
		t.Fatalf("unexpected persisted reliability: %q", got)
	}
	if cfg.Service.Services[0].Enabled {
		t.Fatal("expected persisted enabled=false")
	}
	updated, ok := hubPlatform.GetService("demo")
	if !ok {
		t.Fatal("expected registered service present")
	}
	if updated.Reliability != "high_risk" {
		t.Fatalf("expected hub platform reliability synced, got %q", updated.Reliability)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
