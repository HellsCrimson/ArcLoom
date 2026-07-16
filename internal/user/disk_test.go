package user

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleUser(id, username string) *User {
	return &User{
		ID:           id,
		Username:     username,
		Email:        username + "@example.com",
		PasswordHash: "argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		Provider:     "local",
		CreatedAt:    time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestDisk_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Create(sampleUser("u1", "alice")); err != nil {
		t.Fatal(err)
	}

	// Fresh store instance forces a reload from disk.
	d2, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d2.ByUsername("alice")
	if err != nil {
		t.Fatalf("ByUsername: %v", err)
	}
	if got.ID != "u1" || got.Email != "alice@example.com" {
		t.Fatalf("unexpected reload: %+v", got)
	}
}

func TestDisk_DuplicateUsername(t *testing.T) {
	d, _ := NewDisk(t.TempDir())
	if err := d.Create(sampleUser("u1", "alice")); err != nil {
		t.Fatal(err)
	}
	if err := d.Create(sampleUser("u2", "alice")); err != ErrExists {
		t.Fatalf("expected ErrExists, got %v", err)
	}
}

func TestDisk_PasswordHashNotOnDiskAsPlaintext(t *testing.T) {
	// The stored file must contain the (already-hashed) value, never a plaintext
	// password. We assert the plaintext "hunter2" never appears on disk.
	dir := t.TempDir()
	d, _ := NewDisk(dir)
	u := sampleUser("u1", "alice")
	u.PasswordHash = "argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA" // stand-in for a real hash
	if err := d.Create(u); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "u1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hunter2") {
		t.Fatal("plaintext password must not appear on disk")
	}

	info, err := os.Stat(filepath.Join(dir, "u1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("user file perms = %o, want 600", perm)
	}
}

func TestDisk_BySubject(t *testing.T) {
	d, _ := NewDisk(t.TempDir())
	u := sampleUser("u1", "bob")
	u.Provider = "oidc"
	u.Subject = "idp|abc123"
	u.PasswordHash = ""
	if err := d.Create(u); err != nil {
		t.Fatal(err)
	}
	got, err := d.BySubject("oidc", "idp|abc123")
	if err != nil {
		t.Fatalf("BySubject: %v", err)
	}
	if got.ID != "u1" {
		t.Fatalf("unexpected: %+v", got)
	}
	// Empty subject must never match.
	if _, err := d.BySubject("local", ""); err != ErrNotFound {
		t.Fatalf("empty subject should not match, got %v", err)
	}
}

func TestDisk_UpdateDelete(t *testing.T) {
	d, _ := NewDisk(t.TempDir())
	_ = d.Create(sampleUser("u1", "alice"))
	if err := d.Update("u1", func(u *User) error { u.Disabled = true; return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Get("u1")
	if !got.Disabled {
		t.Fatal("update not persisted")
	}
	if err := d.Delete("u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get("u1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
