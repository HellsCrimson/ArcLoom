package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSO_RoundTripEncryptsSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	s, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSSO(SSO{
		Enabled: true, Issuer: "https://idp.test", ClientID: "cid",
		ClientSecret: "s3cr3t-value", RedirectURL: "https://app/cb", Scopes: "email",
	}); err != nil {
		t.Fatal(err)
	}

	// The plaintext secret must not appear on disk.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "s3cr3t-value") {
		t.Fatal("client secret stored in plaintext on disk")
	}

	// A fresh store decrypts it back.
	s2, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	got := s2.SSO()
	if got.Issuer != "https://idp.test" || got.ClientID != "cid" || got.ClientSecret != "s3cr3t-value" {
		t.Fatalf("round-trip lost data: %+v", got)
	}
	if !s2.HasSecret() {
		t.Error("HasSecret should be true")
	}
}

func TestSetSSO_EmptySecretKeepsExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, _ := NewStore(path, "master")
	_ = s.SetSSO(SSO{Issuer: "i", ClientID: "c", ClientSecret: "keep-me"})
	// Update other fields without resending the secret.
	if err := s.SetSSO(SSO{Issuer: "i2", ClientID: "c", ClientSecret: ""}); err != nil {
		t.Fatal(err)
	}
	got := s.SSO()
	if got.Issuer != "i2" || got.ClientSecret != "keep-me" {
		t.Fatalf("empty secret should preserve existing: %+v", got)
	}
}

func TestSetSSO_RequiresKeyForSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, _ := NewStore(path, "") // no master key
	if s.SecretStorable() {
		t.Fatal("SecretStorable should be false without a key")
	}
	if err := s.SetSSO(SSO{ClientSecret: "x"}); err != ErrSecretKeyRequired {
		t.Fatalf("want ErrSecretKeyRequired, got %v", err)
	}
	// Storing config without a secret is fine.
	if err := s.SetSSO(SSO{Issuer: "i", ClientID: "c"}); err != nil {
		t.Fatalf("no-secret save should succeed: %v", err)
	}
}
