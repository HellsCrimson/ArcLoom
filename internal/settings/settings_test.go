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

func TestDrift_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, _ := NewStore(path, "")
	if d := s.Drift("p1"); d.Enabled {
		t.Fatalf("unset drift should be zero: %+v", d)
	}
	if err := s.SetDrift("p1", Drift{Enabled: true, IntervalMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDrift("p2", Drift{Enabled: false, IntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}

	// Survives a reload; DriftAll sees both entries.
	s2, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if d := s2.Drift("p1"); !d.Enabled || d.IntervalMinutes != 30 {
		t.Fatalf("drift round-trip lost data: %+v", d)
	}
	if all := s2.DriftAll(); len(all) != 2 || all["p2"].IntervalMinutes != 60 {
		t.Fatalf("DriftAll = %+v", all)
	}

	if err := s2.DeleteDrift("p1"); err != nil {
		t.Fatal(err)
	}
	if all := s2.DriftAll(); len(all) != 1 {
		t.Fatalf("delete left %+v", all)
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

func TestGitCreds_RoundTripEncryptsSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	s, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGitCreds("proj1", GitCred{
		AuthType: "ssh", Username: "git",
		SSHKey: "PRIVATE-KEY-MATERIAL", Passphrase: "hunter2",
	}); err != nil {
		t.Fatal(err)
	}

	// No plaintext secret on disk.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "PRIVATE-KEY-MATERIAL") || strings.Contains(string(raw), "hunter2") {
		t.Fatal("git credential stored in plaintext on disk")
	}

	// Meta never exposes secrets but reports what is stored.
	m := s.GitCredMeta("proj1")
	if !m.Stored || m.AuthType != "ssh" || m.Username != "git" || !m.HasSSHKey || !m.HasPassphrase || m.HasToken {
		t.Fatalf("unexpected meta: %+v", m)
	}

	// A fresh store decrypts the secrets back.
	s2, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := s2.GitCreds("proj1")
	if !ok || c.SSHKey != "PRIVATE-KEY-MATERIAL" || c.Passphrase != "hunter2" {
		t.Fatalf("round-trip lost data: %+v ok=%v", c, ok)
	}

	// Empty secret fields keep the previously stored values (UI need not resend).
	if err := s2.SetGitCreds("proj1", GitCred{AuthType: "ssh", Username: "deploy"}); err != nil {
		t.Fatal(err)
	}
	c, _ = s2.GitCreds("proj1")
	if c.Username != "deploy" || c.SSHKey != "PRIVATE-KEY-MATERIAL" {
		t.Fatalf("blank secret should keep prior key: %+v", c)
	}

	// Delete clears it.
	if err := s2.DeleteGitCreds("proj1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.GitCreds("proj1"); ok {
		t.Fatal("credential should be gone after delete")
	}
}

func TestGitCreds_RequiresKeyForSecret(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "settings.yaml"), "") // no master key
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGitCreds("p", GitCred{AuthType: "https", Token: "pat"}); err != ErrSecretKeyRequired {
		t.Fatalf("want ErrSecretKeyRequired, got %v", err)
	}
	// Non-secret metadata is storable without a key.
	if err := s.SetGitCreds("p", GitCred{AuthType: "https", Username: "u"}); err != nil {
		t.Fatalf("storing without a secret should succeed: %v", err)
	}
}

func TestProjectACL_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	s, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	// Absent => open.
	if _, ok := s.ProjectACL("p1"); ok {
		t.Fatal("new project should be unrestricted")
	}
	entries := []ACLEntry{{UserID: "u1", Perms: []string{"resources", "deploy"}}, {UserID: "u2"}}
	if err := s.SetProjectACL("p1", entries); err != nil {
		t.Fatal(err)
	}
	// Reload from disk.
	s2, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.ProjectACL("p1")
	if !ok || len(got) != 2 || got[0].UserID != "u1" || len(got[0].Perms) != 2 || got[1].UserID != "u2" {
		t.Fatalf("round-trip lost data: %+v ok=%v", got, ok)
	}
	// Empty list clears the restriction.
	if err := s2.SetProjectACL("p1", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.ProjectACL("p1"); ok {
		t.Fatal("empty list should clear the ACL")
	}
}

func TestCost_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	in := Cost{
		Enabled: true,
		Overrides: []CostOverride{
			{Target: "proxmox_vm", Monthly: 12.5, Note: "homelab node"},
			{Target: "aws_instance.web", Monthly: 5},
		},
	}
	if err := s.SetCost("p1", in); err != nil {
		t.Fatal(err)
	}

	// A fresh store reloads the same config from disk.
	s2, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Cost("p1")
	if !got.Enabled || len(got.Overrides) != 2 {
		t.Fatalf("round-trip lost data: %+v", got)
	}
	if got.Overrides[0].Target != "proxmox_vm" || got.Overrides[0].Monthly != 12.5 || got.Overrides[0].Note != "homelab node" {
		t.Fatalf("override 0 wrong: %+v", got.Overrides[0])
	}

	// The returned slice is a copy — mutating it must not affect the store.
	got.Overrides[0].Monthly = 999
	if again := s2.Cost("p1"); again.Overrides[0].Monthly != 12.5 {
		t.Fatal("Cost() returned a mutable reference into the store")
	}

	// Delete removes it.
	if err := s2.DeleteCost("p1"); err != nil {
		t.Fatal(err)
	}
	if c := s2.Cost("p1"); c.Enabled || len(c.Overrides) != 0 {
		t.Fatalf("expected empty config after delete, got %+v", c)
	}
}
