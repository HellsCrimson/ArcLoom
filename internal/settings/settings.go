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

type document struct {
	SSO SSO `yaml:"sso"`
	// Drift maps project ID -> drift schedule. Kept here (not on the project
	// model) so it stays out of generated HCL, Git and undo/redo snapshots.
	Drift map[string]Drift `yaml:"drift,omitempty"`
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
