// Package settings persists mutable, app-level configuration edited at runtime
// (the OIDC SSO provider and per-project drift schedules). It is a single YAML
// document on disk; the
// OIDC client secret is encrypted at rest with the ARCLOOM_SECRET_KEY-derived
// key (reusing internal/secrets), so it never touches disk in plaintext.
package settings

import (
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/HellsCrimson/ArcLoom/internal/secrets"
)

// ErrSecretKeyRequired is returned when saving an OIDC client secret without a
// configured ARCLOOM_SECRET_KEY to encrypt it.
var ErrSecretKeyRequired = errors.New("set ARCLOOM_SECRET_KEY to store the OIDC client secret")

// SSO is the OIDC provider configuration. ClientSecret is held in memory only;
// on disk it lives (encrypted) in clientSecretEnc.
type SSO struct {
	Enabled      bool   `yaml:"enabled"`
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"-"`
	RedirectURL  string `yaml:"redirect_url"`
	Scopes       string `yaml:"scopes"`

	ClientSecretEnc string `yaml:"client_secret_enc,omitempty"`
}

// Drift is one project's drift-detection schedule: a periodic refresh-only
// plan that compares the persisted state against real infrastructure.
type Drift struct {
	Enabled         bool `yaml:"enabled" json:"enabled"`
	IntervalMinutes int  `yaml:"interval_minutes" json:"intervalMinutes"`
}

// GitCred is one project's stored remote credential, so a homelabber need not
// retype a PAT / SSH key on every push and pull. The secret fields (Token,
// SSHKey, Passphrase) are held in memory only; on disk they live encrypted in
// their *Enc counterparts (reusing the ARCLOOM_SECRET_KEY-derived key).
type GitCred struct {
	AuthType string `yaml:"auth_type" json:"authType"` // "https" | "ssh"
	Username string `yaml:"username,omitempty" json:"username,omitempty"`

	Token      string `yaml:"-" json:"-"` // HTTPS personal access token
	SSHKey     string `yaml:"-" json:"-"` // SSH private key (PEM)
	Passphrase string `yaml:"-" json:"-"` // SSH key passphrase

	TokenEnc      string `yaml:"token_enc,omitempty" json:"-"`
	SSHKeyEnc     string `yaml:"ssh_key_enc,omitempty" json:"-"`
	PassphraseEnc string `yaml:"passphrase_enc,omitempty" json:"-"`
}

// GitCredMeta describes stored credentials WITHOUT exposing any secret, for the
// UI to show what is on file and prefill the auth type / username.
type GitCredMeta struct {
	Stored        bool   `json:"stored"`
	AuthType      string `json:"authType"`
	Username      string `json:"username,omitempty"`
	HasToken      bool   `json:"hasToken"`
	HasSSHKey     bool   `json:"hasSSHKey"`
	HasPassphrase bool   `json:"hasPassphrase"`
}

// ACLEntry grants one user a set of project-scoped permissions. An entry with
// no perms still grants read access (the user can open the project). Perms are a
// subset of the global Perm* capabilities and act as a *restriction* on top of
// the user's global permissions — an ACL can narrow access, never escalate it.
type ACLEntry struct {
	UserID string   `yaml:"user_id" json:"userId"`
	Perms  []string `yaml:"perms,omitempty" json:"perms"`
}

type document struct {
	SSO SSO `yaml:"sso"`
	// Drift maps project ID -> drift schedule. Kept here (not on the project
	// model) so it stays out of generated HCL, Git and undo/redo snapshots.
	Drift map[string]Drift `yaml:"drift,omitempty"`
	// GitCreds maps project ID -> stored remote credential (encrypted secrets).
	GitCreds map[string]GitCred `yaml:"git_creds,omitempty"`
	// ACL maps project ID -> access list. A project ABSENT from the map is open
	// (global RBAC applies); a project PRESENT (even with an empty list) is
	// restricted to admins + the listed users. Kept here so it stays out of
	// HCL/Git/undo, like drift and git creds.
	ACL map[string][]ACLEntry `yaml:"acl,omitempty"`
	// License is the ArcLoom Enterprise license token (vendor-signed, verified
	// offline). Stored in plaintext — it is signed, not secret.
	License string `yaml:"license,omitempty"`
}

// Store is a disk-backed settings document.
type Store struct {
	path string
	key  []byte // AES key derived from the master passphrase; nil disables secret storage
	mu   sync.Mutex
	doc  document
}

// NewStore loads (or initializes) the settings document at path. masterKey (the
// ARCLOOM_SECRET_KEY) enables encryption of the OIDC client secret.
func NewStore(path, masterKey string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create settings dir: %w", err)
	}
	s := &Store{path: path, key: secrets.DeriveKey(masterKey)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &s.doc); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return s, nil
}

// SecretStorable reports whether an OIDC client secret can be stored (i.e. a
// master key is configured).
func (s *Store) SecretStorable() bool { return s.key != nil }

// SSO returns the current SSO config with ClientSecret decrypted in memory.
func (s *Store) SSO() SSO {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.doc.SSO
	out.ClientSecret = s.decryptSecret(out.ClientSecretEnc)
	out.ClientSecretEnc = ""
	return out
}

// HasSecret reports whether a client secret is stored.
func (s *Store) HasSecret() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc.SSO.ClientSecretEnc != ""
}

// SetSSO persists the SSO config. A non-empty ClientSecret is encrypted; an
// empty ClientSecret keeps the previously stored one (so the UI need not resend
// it). Requires a master key when introducing a new secret.
func (s *Store) SetSSO(in SSO) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	enc := s.doc.SSO.ClientSecretEnc // keep existing by default
	if in.ClientSecret != "" {
		if s.key == nil {
			return ErrSecretKeyRequired
		}
		blob, err := secrets.Encrypt(s.key, []byte(in.ClientSecret))
		if err != nil {
			return err
		}
		enc = base64.StdEncoding.EncodeToString(blob)
	}
	in.ClientSecret = ""
	in.ClientSecretEnc = enc
	s.doc.SSO = in
	return s.persist()
}

// License returns the stored license token ("" when none).
func (s *Store) License() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc.License
}

// SetLicense persists the license token (empty clears it).
func (s *Store) SetLicense(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.License = token
	return s.persist()
}

// Drift returns a project's drift schedule (zero value when none is stored).
func (s *Store) Drift(projectID string) Drift {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc.Drift[projectID]
}

// DriftAll returns a copy of every stored drift schedule, keyed by project ID.
func (s *Store) DriftAll() map[string]Drift {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Drift, len(s.doc.Drift))
	maps.Copy(out, s.doc.Drift)
	return out
}

// SetDrift persists a project's drift schedule.
func (s *Store) SetDrift(projectID string, d Drift) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc.Drift == nil {
		s.doc.Drift = map[string]Drift{}
	}
	s.doc.Drift[projectID] = d
	return s.persist()
}

// DeleteDrift removes a project's drift schedule (used when the project is
// deleted).
func (s *Store) DeleteDrift(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.doc.Drift[projectID]; !ok {
		return nil
	}
	delete(s.doc.Drift, projectID)
	return s.persist()
}

// GitCreds returns a project's stored credential with its secret fields
// decrypted in memory (ok=false when none is stored).
func (s *Store) GitCreds(projectID string) (GitCred, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.doc.GitCreds[projectID]
	if !ok {
		return GitCred{}, false
	}
	c.Token = s.decryptSecret(c.TokenEnc)
	c.SSHKey = s.decryptSecret(c.SSHKeyEnc)
	c.Passphrase = s.decryptSecret(c.PassphraseEnc)
	c.TokenEnc, c.SSHKeyEnc, c.PassphraseEnc = "", "", ""
	return c, true
}

// GitCredMeta reports what is stored for a project without exposing any secret.
func (s *Store) GitCredMeta(projectID string) GitCredMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.doc.GitCreds[projectID]
	if !ok {
		return GitCredMeta{}
	}
	return GitCredMeta{
		Stored:        true,
		AuthType:      c.AuthType,
		Username:      c.Username,
		HasToken:      c.TokenEnc != "",
		HasSSHKey:     c.SSHKeyEnc != "",
		HasPassphrase: c.PassphraseEnc != "",
	}
}

// SetGitCreds persists a project's credential. Non-empty secret fields are
// encrypted; empty ones keep the previously stored value (so the UI need not
// resend a token/key it never received back). Requires a master key when
// introducing a new secret.
func (s *Store) SetGitCreds(projectID string, in GitCred) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.doc.GitCreds[projectID]
	enc := func(plain, existing string) (string, error) {
		if plain == "" {
			return existing, nil
		}
		if s.key == nil {
			return "", ErrSecretKeyRequired
		}
		blob, err := secrets.Encrypt(s.key, []byte(plain))
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(blob), nil
	}
	var err error
	if in.TokenEnc, err = enc(in.Token, prev.TokenEnc); err != nil {
		return err
	}
	if in.SSHKeyEnc, err = enc(in.SSHKey, prev.SSHKeyEnc); err != nil {
		return err
	}
	if in.PassphraseEnc, err = enc(in.Passphrase, prev.PassphraseEnc); err != nil {
		return err
	}
	in.Token, in.SSHKey, in.Passphrase = "", "", ""
	if s.doc.GitCreds == nil {
		s.doc.GitCreds = map[string]GitCred{}
	}
	s.doc.GitCreds[projectID] = in
	return s.persist()
}

// DeleteGitCreds removes a project's stored credential (also on project delete).
func (s *Store) DeleteGitCreds(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.doc.GitCreds[projectID]; !ok {
		return nil
	}
	delete(s.doc.GitCreds, projectID)
	return s.persist()
}

// ProjectACL returns a project's access list and whether one is configured
// (ok=false means the project is open / unrestricted).
func (s *Store) ProjectACL(projectID string) ([]ACLEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acl, ok := s.doc.ACL[projectID]
	if !ok {
		return nil, false
	}
	out := make([]ACLEntry, len(acl))
	copy(out, acl)
	return out, true
}

// SetProjectACL restricts a project to the given entries. Passing a nil/empty
// list removes the restriction (the project becomes open again).
func (s *Store) SetProjectACL(projectID string, entries []ACLEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(entries) == 0 {
		if _, ok := s.doc.ACL[projectID]; !ok {
			return nil
		}
		delete(s.doc.ACL, projectID)
		return s.persist()
	}
	if s.doc.ACL == nil {
		s.doc.ACL = map[string][]ACLEntry{}
	}
	s.doc.ACL[projectID] = entries
	return s.persist()
}

// DeleteProjectACL removes a project's access list (used on project delete).
func (s *Store) DeleteProjectACL(projectID string) error {
	return s.SetProjectACL(projectID, nil)
}

func (s *Store) persist() error {
	data, err := yaml.Marshal(s.doc)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func (s *Store) decryptSecret(enc string) string {
	if enc == "" || s.key == nil {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	plain, err := secrets.Decrypt(s.key, blob)
	if err != nil {
		return ""
	}
	return string(plain)
}
