package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// An .enc file written before the KDF (sealed with the bare sha256 key, no
// kdf.json salt) is migrated to the argon2id key when the store is opened: it
// still resolves, a salt file appears, and the on-disk file is re-sealed under
// the new key.
func TestLocalStore_MigratesLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	const master = "master-passphrase"
	legacy := LegacyKey(master)
	plain, _ := json.Marshal(map[string]string{"db_pw": "s3cr3t"})
	blob, err := encrypt(legacy, plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proj1.enc"), blob, 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewStore(dir, master)
	if v, found, err := s.Resolve("proj1", "db_pw"); err != nil || !found || v != "s3cr3t" {
		t.Fatalf("resolve after migrate: v=%q found=%v err=%v", v, found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, kdfFile)); err != nil {
		t.Fatalf("kdf salt file not created: %v", err)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "proj1.enc"))
	if _, err := decrypt(legacy, onDisk); err == nil {
		t.Error("file still sealed with the legacy key after migration")
	}
	// A fresh instance reads the persisted salt and still resolves it.
	if v, _, _ := NewStore(dir, master).Resolve("proj1", "db_pw"); v != "s3cr3t" {
		t.Errorf("resolve from reopened store: %q", v)
	}
}

// A corrupt kdf.json makes the store fall back to the legacy key derivation
// rather than failing, so pre-KDF secrets stay accessible.
func TestLocalStore_CorruptSaltFallsBackToLegacy(t *testing.T) {
	dir := t.TempDir()
	const master = "master-passphrase"
	legacy := LegacyKey(master)
	plain, _ := json.Marshal(map[string]string{"k": "v"})
	blob, _ := encrypt(legacy, plain)
	if err := os.WriteFile(filepath.Join(dir, "p.enc"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, kdfFile), []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir, master)
	if !s.LocalEnabled() {
		t.Fatal("store should stay enabled on a corrupt salt")
	}
	if v, found, err := s.Resolve("p", "k"); err != nil || !found || v != "v" {
		t.Fatalf("resolve with corrupt salt: v=%q found=%v err=%v", v, found, err)
	}
}

// The KDF stores a salt and derives a key distinct from the bare sha256 one, so
// a freshly written file is not decryptable with the legacy key.
func TestLocalStore_UsesArgonNotSha256(t *testing.T) {
	dir := t.TempDir()
	const master = "master-passphrase"
	s := NewStore(dir, master)
	if err := s.Set("proj", "a", "b"); err != nil {
		t.Fatal(err)
	}
	onDisk, _ := os.ReadFile(s.path("proj"))
	if _, err := decrypt(LegacyKey(master), onDisk); err == nil {
		t.Error("secret decryptable with the bare sha256 key — KDF not applied")
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
