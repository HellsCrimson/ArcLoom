package settings

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/secrets"
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

func TestNotify_RoundTripEncryptsURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	const hook = "https://hooks.slack.com/services/T0/B0/SECRET-TOKEN"
	if err := s.SetNotify("proj1", Notify{
		Enabled: true, Kind: "slack", URL: hook, OnApplyFailure: true, OnDrift: true,
	}); err != nil {
		t.Fatal(err)
	}

	// The URL is a credential: never on disk in plaintext.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "SECRET-TOKEN") {
		t.Fatal("webhook URL stored in plaintext on disk")
	}

	// A fresh store decrypts it back.
	s2, err := NewStore(path, "master-pass")
	if err != nil {
		t.Fatal(err)
	}
	n, ok := s2.Notify("proj1")
	if !ok || !n.Notified() || n.URL != hook || n.Kind != "slack" || !n.OnApplyFailure || !n.OnDrift || n.OnApplySuccess {
		t.Fatalf("round-trip lost data: %+v", n)
	}

	// A blank URL keeps the stored one (the UI never receives it back).
	if err := s2.SetNotify("proj1", Notify{Enabled: true, Kind: "webhook", OnApplySuccess: true}); err != nil {
		t.Fatal(err)
	}
	n, _ = s2.Notify("proj1")
	if n.URL != hook || n.Kind != "webhook" || n.OnApplyFailure || !n.OnApplySuccess {
		t.Fatalf("blank URL should keep the prior webhook: %+v", n)
	}

	if err := s2.DeleteNotify("proj1"); err != nil {
		t.Fatal(err)
	}
	if n, ok := s2.Notify("proj1"); ok || n.Enabled || n.URL != "" {
		t.Fatalf("config should be gone after delete: %+v", n)
	}
}

func TestNotify_RequiresKeyForURL(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "settings.yaml"), "") // no master key
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNotify("p", Notify{Enabled: true, URL: "https://example.com/hook"}); err != ErrSecretKeyRequired {
		t.Fatalf("want ErrSecretKeyRequired, got %v", err)
	}
	// The rest of the config is storable without a key (so it can be turned off).
	if err := s.SetNotify("p", Notify{Kind: "webhook"}); err != nil {
		t.Fatalf("storing without a URL should succeed: %v", err)
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

// A settings document written before the argon2id KDF (an encrypted client
// secret sealed with the bare sha256 key, no kdf salt) is migrated on open: the
// secret still decrypts, the document gains a KDF salt, and the field is re-
// sealed under the new key (no longer readable with the legacy key).
func TestSettings_MigratesLegacySecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	const master = "master-pass"
	legacy := secrets.LegacyKey(master)
	blob, err := secrets.Encrypt(legacy, []byte("topsecret"))
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.StdEncoding.EncodeToString(blob)
	doc := "sso:\n  enabled: true\n  client_secret_enc: " + enc + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(path, master)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.SSO().ClientSecret; got != "topsecret" {
		t.Fatalf("migrated secret: got %q, want topsecret", got)
	}

	// Reopen: the persisted document carries the salt and the re-sealed field.
	s2, err := NewStore(path, master)
	if err != nil {
		t.Fatal(err)
	}
	if s2.doc.KDF == nil || len(s2.doc.KDF.Salt) == 0 {
		t.Fatal("KDF salt not persisted after migration")
	}
	raw, _ := base64.StdEncoding.DecodeString(s2.doc.SSO.ClientSecretEnc)
	if _, err := secrets.Decrypt(legacy, raw); err == nil {
		t.Error("client secret still sealed with the legacy key after migration")
	}
	if got := s2.SSO().ClientSecret; got != "topsecret" {
		t.Fatalf("reopened secret: got %q, want topsecret", got)
	}
}

func TestEnvironments_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	// Absent => nil.
	if envs := s.Environments("p1"); envs != nil {
		t.Fatalf("new project should have no environments, got %+v", envs)
	}
	in := []Environment{
		{Name: "dev", Vars: map[string]string{"region": "eu-west-1"}},
		{Name: "prod", BackendConfig: map[string]string{"key": "prod/terraform.tfstate"}, Vars: map[string]string{"region": "us-east-1"}},
	}
	if err := s.SetEnvironments("p1", in); err != nil {
		t.Fatal(err)
	}
	// Reload from disk.
	s2, err := NewStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Environments("p1")
	if len(got) != 2 || got[0].Name != "dev" || got[1].Name != "prod" {
		t.Fatalf("round-trip lost data: %+v", got)
	}
	if got[0].Vars["region"] != "eu-west-1" || got[1].BackendConfig["key"] != "prod/terraform.tfstate" {
		t.Fatalf("env fields wrong: %+v", got)
	}
	// Returned maps are copies — mutating must not affect the store.
	got[0].Vars["region"] = "mutated"
	if again := s2.Environments("p1"); again[0].Vars["region"] != "eu-west-1" {
		t.Fatal("Environments() returned a mutable reference into the store")
	}
	// Environment() looks one up by name.
	if e, ok := s2.Environment("p1", "prod"); !ok || e.Vars["region"] != "us-east-1" {
		t.Fatalf("Environment(prod) = %+v ok=%v", e, ok)
	}
	if _, ok := s2.Environment("p1", "nope"); ok {
		t.Fatal("Environment(nope) should be absent")
	}
	// Empty list clears it.
	if err := s2.SetEnvironments("p1", nil); err != nil {
		t.Fatal(err)
	}
	if envs := s2.Environments("p1"); envs != nil {
		t.Fatalf("empty list should clear environments, got %+v", envs)
	}
}

func TestApprovalRequired_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, _ := NewStore(path, "")
	if s.ApprovalRequired("p1") {
		t.Fatal("default should be false")
	}
	if err := s.SetApprovalRequired("p1", true); err != nil {
		t.Fatal(err)
	}
	s2, _ := NewStore(path, "")
	if !s2.ApprovalRequired("p1") {
		t.Fatal("approval requirement not persisted")
	}
	if err := s2.SetApprovalRequired("p1", false); err != nil {
		t.Fatal(err)
	}
	if s2.ApprovalRequired("p1") {
		t.Fatal("disabling did not clear")
	}
}
