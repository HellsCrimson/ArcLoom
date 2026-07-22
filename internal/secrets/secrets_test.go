package secrets

import (
	"os"
	"testing"
)

func TestLocalStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "master-passphrase")
	if !s.LocalEnabled() {
		t.Fatal("local backend should be enabled with a master key")
	}
	if err := s.Set("proj1", "db_pw", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	// Fresh store instance forces decrypt-from-disk.
	s2 := NewStore(dir, "master-passphrase")
	v, found, err := s2.Resolve("proj1", "db_pw")
	if err != nil || !found || v != "s3cr3t" {
		t.Fatalf("resolve: v=%q found=%v err=%v", v, found, err)
	}
	// The on-disk file must not contain the plaintext.
	data, _ := os.ReadFile(s.path("proj1"))
	if len(data) == 0 || contains(data, "s3cr3t") {
		t.Errorf("plaintext leaked to disk (%d bytes)", len(data))
	}
}

func TestLocalStore_WrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	NewStore(dir, "right-key").Set("p", "a", "b")
	if _, _, err := NewStore(dir, "wrong-key").Resolve("p", "a"); err == nil {
		t.Error("expected decrypt failure with wrong key")
	}
}

func TestDisabledWithoutKey(t *testing.T) {
	s := NewStore(t.TempDir(), "")
	if s.LocalEnabled() {
		t.Fatal("should be disabled without a master key")
	}
	if err := s.Set("p", "a", "b"); err == nil {
		t.Error("Set should fail when disabled")
	}
}

func TestEnvBackend(t *testing.T) {
	t.Setenv("ARCLOOM_SECRET_api_token", "envval")
	t.Setenv("ARCLOOM_SECRET_KEY", "not-a-secret")
	s := NewStore(t.TempDir(), "")
	v, found, err := s.Resolve("anyproj", "api_token")
	if err != nil || !found || v != "envval" {
		t.Fatalf("env resolve: v=%q found=%v err=%v", v, found, err)
	}
	// The reserved master-key name must not appear as a secret.
	metas, _ := s.List("anyproj")
	for _, m := range metas {
		if m.Name == "KEY" {
			t.Error("master key leaked into secret list")
		}
	}
	var found2 bool
	for _, m := range metas {
		if m.Name == "api_token" && m.Backend == "env" {
			found2 = true
		}
	}
	if !found2 {
		t.Errorf("env secret not listed: %+v", metas)
	}
}

func contains(b []byte, sub string) bool {
	return len(sub) > 0 && (indexOf(b, sub) >= 0)
}
func indexOf(b []byte, sub string) int {
	s := string(b)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// A project id that would traverse out of the secrets directory is refused by
// every path-taking method rather than reading/writing an arbitrary .enc file.
func TestSecrets_RejectsTraversalProjectID(t *testing.T) {
	s := NewStore(t.TempDir(), "master-passphrase")
	bad := "../../etc/evil"
	if err := s.Set(bad, "db_pw", "x"); err == nil {
		t.Error("Set with traversal id should fail")
	}
	if err := s.Delete(bad, "db_pw"); err == nil {
		t.Error("Delete with traversal id should fail")
	}
	if _, err := s.List(bad); err == nil {
		t.Error("List with traversal id should fail")
	}
	if _, _, err := s.Resolve(bad, "db_pw"); err == nil {
		t.Error("Resolve with traversal id should fail")
	}
}

func TestValidProjectID(t *testing.T) {
	for _, s := range []string{"p1", "under_score", "550e8400-e29b-41d4-a716-446655440000"} {
		if !validProjectID(s) {
			t.Errorf("validProjectID(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "..", "a/b", "a.enc", "a\\b"} {
		if validProjectID(s) {
			t.Errorf("validProjectID(%q) = true, want false", s)
		}
	}
}
