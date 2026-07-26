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

	// GroupsClaim is the ID-token claim carrying the user's groups; empty means
	// "groups" (Keycloak, Authentik, Okta, Google Workspace all use that name;
	// Entra ID typically needs "roles").
	GroupsClaim string `yaml:"groups_claim,omitempty"`
	// GroupRoles maps an IdP group to ArcLoom access, applied when an SSO user is
	// provisioned — so an IdP group can land its members as admins instead of the
	// read-only default. Configuring any mapping also makes ArcLoom request the
	// "groups" scope.
	GroupRoles []GroupRole `yaml:"group_roles,omitempty"`
	// SyncGroupsOnLogin re-applies GroupRoles on every SSO login, so the IdP stays
	// the source of truth. OFF by default: it overwrites admin/permission edits
	// made in the Users tab, which is a surprise if the mapping is incomplete.
	SyncGroupsOnLogin bool `yaml:"sync_groups_on_login,omitempty"`

	ClientSecretEnc string `yaml:"client_secret_enc,omitempty"`
}

// GroupRole grants ArcLoom access to the members of one IdP group. Admin implies
// every permission (user.Can), so Perms is ignored when Admin is set.
type GroupRole struct {
	Group string   `yaml:"group" json:"group"`
	Admin bool     `yaml:"admin,omitempty" json:"admin,omitempty"`
	Perms []string `yaml:"perms,omitempty" json:"perms,omitempty"`
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

// Notify is one project's outbound notification config: where operational
// events (apply outcomes, scheduled drift) are posted and which of them are
// sent. The webhook URL is treated as a credential — a Slack incoming-webhook
// URL is bearer-equivalent — so it is held in memory only and lives encrypted
// (URLEnc) on disk, like the git credentials. Kept here (not on the model) so it
// stays out of HCL/Git/undo, like drift/cost/policy.
type Notify struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Kind    string `yaml:"kind" json:"kind"` // "slack" | "webhook"

	URL string `yaml:"-" json:"-"` // webhook endpoint (in memory only)

	URLEnc string `yaml:"url_enc,omitempty" json:"-"`

	OnApplyFailure bool `yaml:"on_apply_failure" json:"onApplyFailure"`
	OnApplySuccess bool `yaml:"on_apply_success" json:"onApplySuccess"`
	OnDrift        bool `yaml:"on_drift" json:"onDrift"`
}

// Notified reports whether the config can actually deliver an event.
func (n Notify) Notified() bool { return n.Enabled && n.URL != "" }

// CostOverride assigns a flat monthly price (USD) to a resource, keyed by
// Target which is either a resource type ("proxmox_vm") or a full resource
// address ("proxmox_vm.web"). An address match takes precedence over a type
// match. Overrides let ArcLoom price providers Infracost cannot (homelab), and
// win over Infracost's estimate where both exist.
type CostOverride struct {
	Target  string  `yaml:"target" json:"target"`
	Monthly float64 `yaml:"monthly" json:"monthly"`
	Note    string  `yaml:"note,omitempty" json:"note,omitempty"`
}

// Cost is one project's cost-estimation config: whether plans compute a cost
// breakdown, and the user-defined flat-price overrides applied on top of any
// Infracost pricing.
type Cost struct {
	Enabled   bool           `yaml:"enabled" json:"enabled"`
	Overrides []CostOverride `yaml:"overrides,omitempty" json:"overrides"`
}

// PolicyDoc is one Rego policy document evaluated against a plan by conftest.
// Name is the file name it is written under in the throwaway policy directory
// (validated at the API so it can never escape that directory).
type PolicyDoc struct {
	Name string `yaml:"name" json:"name"`
	Rego string `yaml:"rego" json:"rego"`
}

// Policy is one project's policy-as-code config: Rego documents run against the
// plan JSON with conftest. Enabled annotates every plan with the result;
// Enforce additionally BLOCKS an apply while the check reports failures (deny
// rules), and FailOnWarn promotes warnings to blocking too. Kept here (not on
// the model) so policies stay out of generated HCL, Git and undo, like
// drift/cost/ACL.
type Policy struct {
	Enabled    bool        `yaml:"enabled" json:"enabled"`
	Enforce    bool        `yaml:"enforce" json:"enforce"`
	FailOnWarn bool        `yaml:"fail_on_warn" json:"failOnWarn"`
	Docs       []PolicyDoc `yaml:"docs,omitempty" json:"docs"`
}

// Environment is one deployment target (dev / staging / prod) for a project.
// The generated HCL stays env-agnostic (one backend TYPE, shared config); an
// environment layers per-env backend config values (applied via -backend-config
// at init) and per-env variable value overrides (injected as TF_VAR_*), and gets
// its own persistent workspace (run/envs/<name>/) so state never crosses envs.
// Kept here (not on the model) so it stays out of generated HCL, Git and
// undo/redo, like drift/cost/ACL.
type Environment struct {
	Name string `yaml:"name" json:"name"`
	// BackendConfig supplies the per-env values for the project's backend
	// (partial config, e.g. the state key/path), raw literals keyed by backend
	// argument name. Empty when the project has no backend.
	BackendConfig map[string]string `yaml:"backend_config,omitempty" json:"backendConfig,omitempty"`
	// Vars are per-env input-variable value overrides, injected as TF_VAR_<name>.
	Vars map[string]string `yaml:"vars,omitempty" json:"vars,omitempty"`
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
	// Cost maps project ID -> cost-estimation config (enable + price overrides).
	// Kept here so it stays out of HCL/Git/undo, like drift and ACLs.
	Cost map[string]Cost `yaml:"cost,omitempty"`
	// Policy maps project ID -> policy-as-code config (Rego documents + whether
	// a failing check blocks an apply). Kept here for the same reason as Cost.
	Policy map[string]Policy `yaml:"policy,omitempty"`
	// Notify maps project ID -> outbound notification config (webhook URL
	// encrypted). Kept here for the same reason as Cost and Policy.
	Notify map[string]Notify `yaml:"notify,omitempty"`
	// Environments maps project ID -> ordered deployment environments. Kept here
	// (not on the model) so per-env backend config / var overrides stay out of
	// HCL/Git/undo.
	Environments map[string][]Environment `yaml:"environments,omitempty"`
	// ApprovalRequired maps project ID -> whether an apply must run a plan a
	// user with the approve permission has signed off on (the plan → approve →
	// apply gate). Absent/false = apply re-plans and auto-approves (default).
	ApprovalRequired map[string]bool `yaml:"approval_required,omitempty"`
	// License is the ArcLoom Enterprise license token (vendor-signed, verified
	// offline). Stored in plaintext — it is signed, not secret.
	License string `yaml:"license,omitempty"`
	// KDF is the key-derivation record for the encrypted fields (SSO client
	// secret + git credentials). Persisting the salt makes the argon2id key
	// reproducible across restarts. Absent = pre-KDF document (sha256 key).
	KDF *kdfMeta `yaml:"kdf,omitempty"`
}

// kdfMeta persists the argon2id salt (and version, for future parameter changes)
// alongside the encrypted fields it protects.
type kdfMeta struct {
	Version int    `yaml:"version"`
	Salt    []byte `yaml:"salt"`
}

// Store is a disk-backed settings document.
type Store struct {
	path string
	key  []byte // AES key derived from the master passphrase; nil disables secret storage
	// legacyKey is the pre-KDF (bare sha256) key, kept to decrypt and migrate
	// fields sealed before the argon2id KDF landed.
	legacyKey []byte
	mu        sync.Mutex
	doc       document
}

// NewStore loads (or initializes) the settings document at path. masterKey (the
// ARCLOOM_SECRET_KEY) enables encryption of the OIDC client secret and git
// credentials; it is stretched to an AES key with argon2id (see
// secrets.DeriveKey) against a salt persisted in the document. Fields sealed
// under the old sha256 key are migrated to the new key on first run.
func NewStore(path, masterKey string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create settings dir: %w", err)
	}
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else if err := yaml.Unmarshal(data, &s.doc); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	if masterKey != "" {
		s.legacyKey = secrets.LegacyKey(masterKey)
		if err := s.initKey(masterKey); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// initKey establishes the argon2id encryption key. If the document already
// carries a KDF salt, the key is reproduced from it. Otherwise a fresh salt is
// minted and any pre-KDF secret fields are re-sealed under the new key and
// persisted together with the salt; when nothing is stored yet, persistence is
// deferred to the first write (which carries the salt) so a fresh install stays
// file-less.
func (s *Store) initKey(masterKey string) error {
	if s.doc.KDF != nil && len(s.doc.KDF.Salt) > 0 {
		s.key = secrets.DeriveKey(masterKey, s.doc.KDF.Salt)
		return nil
	}
	salt, err := secrets.NewSalt()
	if err != nil {
		return err
	}
	s.key = secrets.DeriveKey(masterKey, salt)
	s.doc.KDF = &kdfMeta{Version: 1, Salt: salt}
	if s.hasEncryptedFields() {
		s.migrateLegacy()
		return s.persist()
	}
	return nil
}

// hasEncryptedFields reports whether any at-rest secret is stored.
func (s *Store) hasEncryptedFields() bool {
	if s.doc.SSO.ClientSecretEnc != "" {
		return true
	}
	for _, c := range s.doc.GitCreds {
		if c.TokenEnc != "" || c.SSHKeyEnc != "" || c.PassphraseEnc != "" {
			return true
		}
	}
	for _, n := range s.doc.Notify {
		if n.URLEnc != "" {
			return true
		}
	}
	return false
}

// migrateLegacy re-seals every encrypted field (SSO client secret + git creds)
// from the legacy sha256 key to the new argon2id key. Best-effort per field: one
// that can't be read is left as-is (decryptSecret still handles it via the
// legacy-key fallback).
func (s *Store) migrateLegacy() {
	if s.legacyKey == nil {
		return
	}
	s.doc.SSO.ClientSecretEnc = s.reseal(s.doc.SSO.ClientSecretEnc)
	for id, c := range s.doc.GitCreds {
		c.TokenEnc = s.reseal(c.TokenEnc)
		c.SSHKeyEnc = s.reseal(c.SSHKeyEnc)
		c.PassphraseEnc = s.reseal(c.PassphraseEnc)
		s.doc.GitCreds[id] = c
	}
	for id, n := range s.doc.Notify {
		n.URLEnc = s.reseal(n.URLEnc)
		s.doc.Notify[id] = n
	}
}

// reseal decrypts a base64 blob with the legacy key and re-encrypts it under the
// new key. It returns the input unchanged when the field is empty, already under
// the new key, or unreadable.
func (s *Store) reseal(enc string) string {
	if enc == "" {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return enc
	}
	if _, err := secrets.Decrypt(s.key, blob); err == nil {
		return enc // already under the new key
	}
	plain, err := secrets.Decrypt(s.legacyKey, blob)
	if err != nil {
		return enc
	}
	nb, err := secrets.Encrypt(s.key, plain)
	if err != nil {
		return enc
	}
	return base64.StdEncoding.EncodeToString(nb)
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

// Cost returns a project's cost-estimation config (zero value when none is
// stored). The Overrides slice is copied so callers cannot mutate the store.
func (s *Store) Cost(projectID string) Cost {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.doc.Cost[projectID]
	if c.Overrides != nil {
		out := make([]CostOverride, len(c.Overrides))
		copy(out, c.Overrides)
		c.Overrides = out
	}
	return c
}

// SetCost persists a project's cost-estimation config.
func (s *Store) SetCost(projectID string, c Cost) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc.Cost == nil {
		s.doc.Cost = map[string]Cost{}
	}
	s.doc.Cost[projectID] = c
	return s.persist()
}

// DeleteCost removes a project's cost config (used when the project is deleted).
func (s *Store) DeleteCost(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.doc.Cost[projectID]; !ok {
		return nil
	}
	delete(s.doc.Cost, projectID)
	return s.persist()
}

// Policy returns a project's policy-as-code config (zero value when none is
// stored). The document slice is copied so callers cannot mutate the store.
func (s *Store) Policy(projectID string) Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.doc.Policy[projectID]
	if p.Docs != nil {
		out := make([]PolicyDoc, len(p.Docs))
		copy(out, p.Docs)
		p.Docs = out
	}
	return p
}

// SetPolicy persists a project's policy-as-code config.
func (s *Store) SetPolicy(projectID string, p Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc.Policy == nil {
		s.doc.Policy = map[string]Policy{}
	}
	s.doc.Policy[projectID] = p
	return s.persist()
}

// DeletePolicy removes a project's policy config (used on project delete).
func (s *Store) DeletePolicy(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.doc.Policy[projectID]; !ok {
		return nil
	}
	delete(s.doc.Policy, projectID)
	return s.persist()
}

// Notify returns a project's notification config with the webhook URL decrypted
// in memory. ok=false means the project has never been configured (the caller
// decides what the defaults are), not that notifications are off.
func (s *Store) Notify(projectID string) (Notify, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.doc.Notify[projectID]
	if !ok {
		return Notify{}, false
	}
	n.URL = s.decryptSecret(n.URLEnc)
	n.URLEnc = ""
	return n, true
}

// SetNotify persists a project's notification config. A non-empty URL is
// encrypted; an empty URL keeps the previously stored one (so the UI, which
// never receives it back, need not resend it). Requires a master key when
// introducing a URL.
func (s *Store) SetNotify(projectID string, in Notify) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	enc := s.doc.Notify[projectID].URLEnc // keep existing by default
	if in.URL != "" {
		if s.key == nil {
			return ErrSecretKeyRequired
		}
		blob, err := secrets.Encrypt(s.key, []byte(in.URL))
		if err != nil {
			return err
		}
		enc = base64.StdEncoding.EncodeToString(blob)
	}
	in.URL = ""
	in.URLEnc = enc
	if s.doc.Notify == nil {
		s.doc.Notify = map[string]Notify{}
	}
	s.doc.Notify[projectID] = in
	return s.persist()
}

// DeleteNotify removes a project's notification config (also on project delete).
func (s *Store) DeleteNotify(projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.doc.Notify[projectID]; !ok {
		return nil
	}
	delete(s.doc.Notify, projectID)
	return s.persist()
}

// Environments returns a project's deployment environments (nil when none are
// configured). The slice and its maps are deep-copied so callers cannot mutate
// the store.
func (s *Store) Environments(projectID string) []Environment {
	s.mu.Lock()
	defer s.mu.Unlock()
	envs := s.doc.Environments[projectID]
	if envs == nil {
		return nil
	}
	out := make([]Environment, len(envs))
	for i, e := range envs {
		out[i] = Environment{Name: e.Name}
		if e.BackendConfig != nil {
			out[i].BackendConfig = maps.Clone(e.BackendConfig)
		}
		if e.Vars != nil {
			out[i].Vars = maps.Clone(e.Vars)
		}
	}
	return out
}

// Environment returns one named environment (ok=false when absent).
func (s *Store) Environment(projectID, name string) (Environment, bool) {
	for _, e := range s.Environments(projectID) {
		if e.Name == name {
			return e, true
		}
	}
	return Environment{}, false
}

// SetEnvironments replaces a project's environment list. An empty list clears
// it (the project reverts to the single default workspace).
func (s *Store) SetEnvironments(projectID string, envs []Environment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(envs) == 0 {
		if _, ok := s.doc.Environments[projectID]; !ok {
			return nil
		}
		delete(s.doc.Environments, projectID)
		return s.persist()
	}
	if s.doc.Environments == nil {
		s.doc.Environments = map[string][]Environment{}
	}
	s.doc.Environments[projectID] = envs
	return s.persist()
}

// DeleteEnvironments removes a project's environments (used on project delete).
func (s *Store) DeleteEnvironments(projectID string) error {
	return s.SetEnvironments(projectID, nil)
}

// ApprovalRequired reports whether applies for a project are gated behind an
// explicit plan approval.
func (s *Store) ApprovalRequired(projectID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc.ApprovalRequired[projectID]
}

// SetApprovalRequired sets (or, when false, clears) a project's approval gate.
func (s *Store) SetApprovalRequired(projectID string, required bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !required {
		if _, ok := s.doc.ApprovalRequired[projectID]; !ok {
			return nil
		}
		delete(s.doc.ApprovalRequired, projectID)
		return s.persist()
	}
	if s.doc.ApprovalRequired == nil {
		s.doc.ApprovalRequired = map[string]bool{}
	}
	s.doc.ApprovalRequired[projectID] = true
	return s.persist()
}

// DeleteApprovalPolicy removes a project's approval gate (used on project delete).
func (s *Store) DeleteApprovalPolicy(projectID string) error {
	return s.SetApprovalRequired(projectID, false)
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
	if err != nil && s.legacyKey != nil {
		// Fall back to the pre-KDF key for a field not yet migrated.
		plain, err = secrets.Decrypt(s.legacyKey, blob)
	}
	if err != nil {
		return ""
	}
	return string(plain)
}
