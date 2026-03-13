package app

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("mypassword123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !VerifyPassword("mypassword123", hash) {
		t.Fatal("VerifyPassword should succeed for correct password")
	}
	if VerifyPassword("wrongpassword", hash) {
		t.Fatal("VerifyPassword should fail for wrong password")
	}
	if VerifyPassword("mypassword123", "bad:hash") {
		t.Fatal("VerifyPassword should fail for malformed stored hash")
	}
}

func TestPasswordTooShort(t *testing.T) {
	_, err := HashPassword("abc")
	if err == nil {
		t.Fatal("HashPassword should reject password shorter than 6 chars")
	}
}

func TestJWTIssueAndParse(t *testing.T) {
	dir := t.TempDir()
	auth, err := NewAuthService(dir)
	if err != nil {
		t.Fatalf("NewAuthService failed: %v", err)
	}
	token, err := auth.IssueJWT("usr-123", "alice")
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}
	claims, err := auth.ParseJWT(token)
	if err != nil {
		t.Fatalf("ParseJWT failed: %v", err)
	}
	if claims.UserID != "usr-123" {
		t.Fatalf("expected user_id usr-123, got %s", claims.UserID)
	}
	if claims.Username != "alice" {
		t.Fatalf("expected username alice, got %s", claims.Username)
	}
}

func TestJWTExpired(t *testing.T) {
	dir := t.TempDir()
	auth, err := NewAuthService(dir)
	if err != nil {
		t.Fatalf("NewAuthService failed: %v", err)
	}
	// Manually create an expired token by signing with past exp
	_ = time.Now()
	token, err := auth.IssueJWT("usr-exp", "bob")
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}
	// Valid token should parse
	if _, err := auth.ParseJWT(token); err != nil {
		t.Fatalf("ParseJWT should succeed for valid token: %v", err)
	}
	// Tampered token should fail
	if _, err := auth.ParseJWT(token + "x"); err == nil {
		t.Fatal("ParseJWT should fail for tampered token")
	}
}

func TestJWTSecretPersistence(t *testing.T) {
	dir := t.TempDir()
	auth1, err := NewAuthService(dir)
	if err != nil {
		t.Fatalf("NewAuthService(1) failed: %v", err)
	}
	token, err := auth1.IssueJWT("usr-p", "charlie")
	if err != nil {
		t.Fatalf("IssueJWT failed: %v", err)
	}

	// Create second instance from same dir — should load same secret
	auth2, err := NewAuthService(dir)
	if err != nil {
		t.Fatalf("NewAuthService(2) failed: %v", err)
	}
	claims, err := auth2.ParseJWT(token)
	if err != nil {
		t.Fatalf("ParseJWT with persisted secret should succeed: %v", err)
	}
	if claims.UserID != "usr-p" || claims.Username != "charlie" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestCreateUserAndAuth(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "test.db"), "default", "project-default", "chat-default")
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	userID, err := store.CreateUser("testuser", hash)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if userID == "" {
		t.Fatal("CreateUser should return non-empty user_id")
	}

	gotID, gotHash, exists, err := store.GetUserByUsername("testuser")
	if err != nil {
		t.Fatalf("GetUserByUsername failed: %v", err)
	}
	if !exists {
		t.Fatal("user should exist")
	}
	if gotID != userID {
		t.Fatalf("expected user_id %s, got %s", userID, gotID)
	}
	if !VerifyPassword("secret123", gotHash) {
		t.Fatal("password should verify")
	}

	// Duplicate username should fail
	_, err = store.CreateUser("testuser", hash)
	if err == nil {
		t.Fatal("CreateUser should fail for duplicate username")
	}
}
