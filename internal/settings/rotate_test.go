package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedEncrypted fills a store with one of every at-rest encrypted field, so a
// rotation test covers the whole set rather than the SSO secret alone.
func seedEncrypted(t *testing.T, path, key string) *Store {
	t.Helper()
	s, err := NewStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSSO(SSO{Issuer: "https://idp.test", ClientID: "cid", ClientSecret: "sso-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGitCreds("proj1", GitCred{AuthType: "https", Username: "u", Token: "git-token"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotify("proj1", Notify{Enabled: true, Kind: "slack", URL: "https://hooks.test/x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecretStore(SecretStore{Kind: SecretStoreVault, Address: "https://vault.test", Token: "vault-token"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRotate_ReSealsEveryEncryptedField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s := seedEncrypted(t, path, "old-master-key")

	rep, err := s.Rotate("old-master-key", "new-master-key")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// SSO secret + git token + notify URL + vault token.
	if !rep.Complete || rep.Items != 4 || rep.Rekeyed != 4 || rep.Verified != 4 {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// A fresh process with the NEW key reads every value back.
	s2, err := NewStore(path, "new-master-key")
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.SSO().ClientSecret; got != "sso-secret" {
		t.Errorf("sso secret = %q", got)
	}
	if c, ok := s2.GitCreds("proj1"); !ok || c.Token != "git-token" {
		t.Errorf("git token = %+v", c)
	}
	if n, ok := s2.Notify("proj1"); !ok || n.URL != "https://hooks.test/x" {
		t.Errorf("notify url = %+v", n)
	}
	if got := s2.SecretStore().Token; got != "vault-token" {
		t.Errorf("vault token = %q", got)
	}

	// Nothing remains readable under the old key.
	s3, err := NewStore(path, "old-master-key")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range s3.EncryptedFields() {
		if f.Readable {
			t.Errorf("%s still opens with the old key", f.Field)
		}
	}
}

func TestRotate_WrongCurrentKeyChangesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s := seedEncrypted(t, path, "old-master-key")
	before, _ := os.ReadFile(path)

	if _, err := s.Rotate("not-the-current-key", "new-master-key"); err == nil {
		t.Fatal("expected a wrong current key to be refused")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("a refused rotation must not rewrite the document")
	}
	if got := s.SSO().ClientSecret; got != "sso-secret" {
		t.Errorf("live store damaged by a refused rotation: %q", got)
	}
}

// A field that no key opens must be reported as unreadable rather than as
// absent — the silent half of a wrong ARCLOOM_SECRET_KEY.
func TestEncryptedFields_DistinguishesUnreadableFromAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	seedEncrypted(t, path, "right-key")

	s, err := NewStore(path, "wrong-key")
	if err != nil {
		t.Fatal(err)
	}
	fields := s.EncryptedFields()
	if len(fields) != 4 {
		t.Fatalf("expected 4 encrypted fields, got %d (%+v)", len(fields), fields)
	}
	for _, f := range fields {
		if f.Readable {
			t.Errorf("%s reported readable under the wrong key", f.Field)
		}
	}
	// The old behaviour: the value itself still reads as "" (callers are
	// unchanged) — the difference is that the failure is now visible.
	if s.SSO().ClientSecret != "" {
		t.Error("an unreadable secret should not surface a value")
	}

	// A store with nothing configured reports no fields at all, which is what
	// makes the distinction meaningful.
	empty, err := NewStore(filepath.Join(t.TempDir(), "settings.yaml"), "k")
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.EncryptedFields(); len(got) != 0 {
		t.Errorf("empty store reported fields: %+v", got)
	}
}

func TestRotate_RejectsBadInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, _ := NewStore(path, "master")
	if _, err := s.Rotate("master", ""); err == nil {
		t.Error("empty new key should be rejected")
	}
	if _, err := s.Rotate("master", "master"); err == nil {
		t.Error("an unchanged key should be rejected")
	}
}

func TestPersist_IsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	s, _ := NewStore(path, "master")
	if err := s.SetSSO(SSO{Issuer: "https://idp.example", ClientID: "arcloom"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}
