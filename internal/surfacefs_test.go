package app

import (
	"bytes"
	"testing"
)

func TestSurfaceFSService_ReadWriteListAndStatic(t *testing.T) {
	service, err := NewSurfaceFSService(t.TempDir())
	if err != nil {
		t.Fatalf("NewSurfaceFSService failed: %v", err)
	}
	surfaceID := "f0f6f645-7d4a-4f13-b3c0-5f0c6e8b4421"
	sessionToken, _, err := service.IssueSurfaceSessionToken("default", surfaceID, 0)
	if err != nil {
		t.Fatalf("IssueSurfaceSessionToken failed: %v", err)
	}
	writeCap, _, err := service.IssueCapabilityTokenFromSession(sessionToken, SurfaceScopeWrite, ".", 0)
	if err != nil {
		t.Fatalf("IssueCapabilityToken(write) failed: %v", err)
	}
	readCap, _, err := service.IssueCapabilityTokenFromSession(sessionToken, SurfaceScopeRead, ".", 0)
	if err != nil {
		t.Fatalf("IssueCapabilityToken(read) failed: %v", err)
	}
	listCap, _, err := service.IssueCapabilityTokenFromSession(sessionToken, SurfaceScopeList, ".", 0)
	if err != nil {
		t.Fatalf("IssueCapabilityToken(list) failed: %v", err)
	}
	staticCap, _, err := service.IssueCapabilityTokenFromSession(sessionToken, SurfaceScopeStatic, "cache", 0)
	if err != nil {
		t.Fatalf("IssueCapabilityToken(static) failed: %v", err)
	}

	written, err := service.WriteFile(writeCap, surfaceID, "cache/state.json", []byte(`{"count":7}`))
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if written <= 0 {
		t.Fatalf("WriteFile returned empty size")
	}

	raw, err := service.ReadFile(readCap, surfaceID, "cache/state.json")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(raw, []byte(`{"count":7}`)) {
		t.Fatalf("unexpected file payload: %s", string(raw))
	}

	items, err := service.ListDir(listCap, surfaceID, "cache")
	if err != nil {
		t.Fatalf("ListDir failed: %v", err)
	}
	if len(items) != 1 || items[0].Name != "state.json" {
		t.Fatalf("unexpected list items: %#v", items)
	}

	path, err := service.ResolveStaticFile(staticCap, surfaceID, "cache/state.json")
	if err != nil {
		t.Fatalf("ResolveStaticFile failed: %v", err)
	}
	if path == "" {
		t.Fatalf("ResolveStaticFile should return path")
	}
}

func TestSurfaceFSService_PathTraversalRejected(t *testing.T) {
	service, err := NewSurfaceFSService(t.TempDir())
	if err != nil {
		t.Fatalf("NewSurfaceFSService failed: %v", err)
	}
	surfaceID := "f0f6f645-7d4a-4f13-b3c0-5f0c6e8b4421"
	sessionToken, _, err := service.IssueSurfaceSessionToken("default", surfaceID, 0)
	if err != nil {
		t.Fatalf("IssueSurfaceSessionToken failed: %v", err)
	}
	writeCap, _, err := service.IssueCapabilityTokenFromSession(sessionToken, SurfaceScopeWrite, ".", 0)
	if err != nil {
		t.Fatalf("IssueCapabilityToken(write) failed: %v", err)
	}
	if _, err := service.WriteFile(writeCap, surfaceID, "../escape.txt", []byte("oops")); err == nil {
		t.Fatalf("expected traversal write to fail")
	}
}
