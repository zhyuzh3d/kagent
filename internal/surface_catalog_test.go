package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanSurfaceCatalog_ConflictAndInvalid(t *testing.T) {
	root := t.TempDir()
	writeSurfacePkg(t, root, "buildin", "counter-a", `{
		"id":"f0f6f645-7d4a-4f13-b3c0-5f0c6e8b4421",
		"name":"Counter A",
		"version":"1.0",
		"min_supported_version":"1.0",
		"entry":"index.html"
	}`)
	writeSurfacePkg(t, root, "ext", "counter-b", `{
		"id":"f0f6f645-7d4a-4f13-b3c0-5f0c6e8b4421",
		"name":"Counter B",
		"version":"1.0",
		"min_supported_version":"1.0",
		"entry":"index.html"
	}`)
	_ = os.MkdirAll(filepath.Join(root, "custom", "broken"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "custom", "broken", "manifest.json"), []byte(`{"id":"not-uuid"}`), 0o644); err != nil {
		t.Fatalf("write invalid manifest failed: %v", err)
	}

	items, err := ScanSurfaceCatalog(root, nowMS())
	if err != nil {
		t.Fatalf("ScanSurfaceCatalog failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	conflicts := 0
	invalid := 0
	for _, item := range items {
		if item.Status == SurfaceStatusConflict {
			conflicts += 1
		}
		if item.Status == SurfaceStatusInvalid {
			invalid += 1
		}
	}
	if conflicts != 2 {
		t.Fatalf("expected 2 conflict items, got %d", conflicts)
	}
	if invalid != 1 {
		t.Fatalf("expected 1 invalid item, got %d", invalid)
	}
}

func TestSQLiteStoreSurfaceCatalog_DefaultEnableAndUpdate(t *testing.T) {
	root := t.TempDir()
	writeSurfacePkg(t, root, "buildin", "counter", `{
		"id":"f0f6f645-7d4a-4f13-b3c0-5f0c6e8b4421",
		"name":"Counter",
		"version":"1.0",
		"min_supported_version":"1.0",
		"entry":"index.html"
	}`)

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "kagent.db"), "default", "project-default", "chat-default")
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()
	if err := SyncSurfaceCatalog(store, root); err != nil {
		t.Fatalf("SyncSurfaceCatalog failed: %v", err)
	}

	items, err := store.ListSurfacesForUser(store.RuntimeUserID())
	if err != nil {
		t.Fatalf("ListSurfacesForUser failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 surface, got %d", len(items))
	}
	if !items[0].Enabled || !items[0].Available {
		t.Fatalf("buildin surface should be enabled and available by default: %#v", items[0])
	}

	if err := store.SetSurfaceEnabled(store.RuntimeUserID(), items[0].SurfaceID, false); err != nil {
		t.Fatalf("SetSurfaceEnabled failed: %v", err)
	}
	updated, err := store.ListSurfacesForUser(store.RuntimeUserID())
	if err != nil {
		t.Fatalf("ListSurfacesForUser(update) failed: %v", err)
	}
	if len(updated) != 1 || updated[0].Enabled || updated[0].Available {
		t.Fatalf("surface should be disabled after update: %#v", updated)
	}
}

func writeSurfacePkg(t *testing.T, root string, surfaceType string, pkgName string, manifest string) {
	t.Helper()
	dir := filepath.Join(root, surfaceType, pkgName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s failed: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatalf("write entry failed: %v", err)
	}
}
