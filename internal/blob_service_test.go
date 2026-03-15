package app

import (
	"testing"
	"time"
)

func TestBlobServicePutGetSign(t *testing.T) {
	svc, err := NewBlobService(t.TempDir())
	if err != nil {
		t.Fatalf("new blob service failed: %v", err)
	}
	meta, err := svc.Put("u1", "text/plain", []byte("hello"), time.Hour)
	if err != nil {
		t.Fatalf("blob put failed: %v", err)
	}
	raw, gotMeta, err := svc.Get("u1", meta.BlobID)
	if err != nil {
		t.Fatalf("blob get failed: %v", err)
	}
	if string(raw) != "hello" {
		t.Fatalf("unexpected blob bytes: %q", string(raw))
	}
	if gotMeta.BlobID != meta.BlobID {
		t.Fatalf("blob metadata mismatch")
	}
	token, _, err := svc.SignDownloadURL("u1", meta.BlobID, 2*time.Minute)
	if err != nil {
		t.Fatalf("sign download url failed: %v", err)
	}
	verifyMeta, err := svc.VerifyDownloadToken(meta.BlobID, token)
	if err != nil {
		t.Fatalf("verify token failed: %v", err)
	}
	if verifyMeta.BlobID != meta.BlobID {
		t.Fatalf("verify metadata mismatch")
	}
}

func TestBlobServiceGC(t *testing.T) {
	svc, err := NewBlobService(t.TempDir())
	if err != nil {
		t.Fatalf("new blob service failed: %v", err)
	}
	meta, err := svc.Put("u1", "text/plain", []byte("hello"), time.Millisecond)
	if err != nil {
		t.Fatalf("blob put failed: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	deleted, err := svc.GC(time.Now())
	if err != nil {
		t.Fatalf("gc failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 blob deleted, got %d", deleted)
	}
	if _, _, err := svc.Get("u1", meta.BlobID); err == nil {
		t.Fatalf("expected blob to be unavailable after gc")
	}
}
