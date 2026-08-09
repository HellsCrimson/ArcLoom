// Package secrets stores and resolves secret values for injection into the
// Terraform/OpenTofu CLI at run time. Three backends are supported:
//
//   - "local": an AES-256-GCM encrypted file per project (never in Git). The
//     master key comes from ARCLOOM_SECRET_KEY.
//   - "env": values read from ARCLOOM_SECRET_<name> in the host environment.
//   - an external Backend (Vault / OpenBao), which REPLACES local as the
//     writable store when configured — see backend.go.
//
// Plaintext secret values never enter the generated HCL; they reach the CLI
// only as TF_VAR_<name> at plan/apply time.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// envPrefix is the environment prefix for env-backed secret values.
const envPrefix = "ARCLOOM_SECRET_"

// reserved env suffix that is the master key, not a secret value.
const masterKeySuffix = "KEY"

// Meta describes a secret without exposing its value.
type Meta struct {
	Name    string `json:"name"`
	Backend string `json:"backend"` // "local" | "env" | an external backend's name
}

// Store manages per-project secrets across the local + env backends.
type Store struct {
	dir string
	key []byte // 32-byte AES key, or nil if the local backend is disabled
	// legacyKey is the pre-KDF (bare sha256) key, kept so .enc files written
	// before the argon2id KDF landed can still be decrypted and migrated.
	legacyKey []byte
	// prevKey is the previous generation's key during a master-key rotation,
	// kept as a decrypt fallback until every blob verifies under the new one —
	// so an interrupted rotation reads as mixed, not as lost.
	prevKey []byte
	// remote is the optional external backend; when set it replaces the local
	// files as the writable store (guarded by mu, swappable at runtime).
	remote Backend
	mu     sync.Mutex
}

// NewStore builds a store. masterKey (from ARCLOOM_SECRET_KEY) enables the local
// encrypted backend when non-empty; it is stretched into a 32-byte AES key with
// argon2id (see DeriveKey) against a per-installation salt persisted in the
// secrets dir. Existing sha256-key .enc files are migrated to the new key on
// first run (best-effort; the legacy key stays available as a decrypt fallback).
func NewStore(dir, masterKey string) *Store {
	s := &Store{dir: dir}
	if masterKey == "" {
		return s // local backend disabled
	}
	s.legacyKey = LegacyKey(masterKey)
	meta, created, err := loadOrCreateKDF(dir)
	if err != nil {
		// Can't establish the argon2id salt (disk error): fall back to the legacy
		// derivation so existing secrets stay readable. Migration is retried on
		// the next start once the salt file can be written.
		slog.Warn("secrets: argon2id KDF unavailable; using legacy key derivation", "err", err)
		s.key = s.legacyKey
		return s
	}
	s.key = DeriveKey(masterKey, meta.Salt)
	if meta.Rotating && len(meta.PrevSalt) > 0 {
		// A master-key rotation was interrupted: some files may still be sealed
		// under the previous generation. Deriving that generation from THIS
		// passphrase only helps when the server was restarted with the old one,
		// which is exactly the case where nothing would otherwise open — the
		// other half is finished by re-running the rotation.
		s.prevKey = DeriveKey(masterKey, meta.PrevSalt)
		slog.Warn("secrets: an interrupted master-key rotation was found; re-run the rotation with the old and new keys to finish it",
			"dir", dir)
	}
	if created {
		s.migrateLegacy() // re-encrypt any pre-KDF files under the new key
	}
	return s
}

// LocalEnabled reports whether the local encrypted backend is usable.
func (s *Store) LocalEnabled() bool { return s.key != nil }

func (s *Store) path(projectID string) string {
	return filepath.Join(s.dir, projectID+".enc")
}

// validProjectID reports whether a project id is safe to interpolate into the
// secrets file path (letters, digits, underscore, hyphen — no '.', '/' or '\'),
// so a caller can never read or write a .enc file outside the secrets dir.
func validProjectID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// Set stores a secret value in the writable backend (the external store when
// one is configured, otherwise the local encrypted files).
func (s *Store) Set(projectID, name, value string) error {
	if !validName(name) {
		return fmt.Errorf("invalid secret name %q (use letters, digits, underscore)", name)
	}
	return s.mutate(projectID, func(m map[string]string) { m[name] = value })
}

// Delete removes a secret from the writable backend.
func (s *Store) Delete(projectID, name string) error {
	return s.mutate(projectID, func(m map[string]string) { delete(m, name) })
}

// mutate applies a read-modify-write to a project's secret document. Both
// backends work this way (one encrypted file / one KV document per project), so
// the two paths differ only in where the document lives.
func (s *Store) mutate(projectID string, fn func(map[string]string)) error {
	if r := s.Remote(); r != nil {
		ctx, cancel := context.WithTimeout(context.Background(), backendTimeout)
		defer cancel()
		// Serialized with the local path's lock so two concurrent edits to the
		// same project cannot read-modify-write over each other.
		s.mu.Lock()
		defer s.mu.Unlock()
		m, err := r.Load(ctx, projectID)
		if err != nil {
			return fmt.Errorf("%s: %w", r.Name(), err)
		}
		if m == nil {
			m = map[string]string{}
		}
		fn(m)
		if err := r.Store(ctx, projectID, m); err != nil {
			return fmt.Errorf("%s: %w", r.Name(), err)
		}
		return nil
	}
	if s.key == nil {
		return ErrNoWritableBackend
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadLocal(projectID)
	if err != nil {
		return err
	}
	fn(m)
	return s.saveLocal(projectID, m)
}

// List returns secret metadata (names + backend) with no values.
func (s *Store) List(projectID string) ([]Meta, error) {
	primary, env, err := s.load(projectID)
	if err != nil {
		return nil, err
	}
	name := s.BackendName()
	if name == "" {
		name = "local"
	}
	return metasFrom(primary, env, name), nil
}

// Resolve returns a secret's plaintext value: the writable backend first, then
// the environment.
func (s *Store) Resolve(projectID, name string) (string, bool, error) {
	primary, env, err := s.load(projectID)
	if err != nil {
		return "", false, err
	}
	if v, ok := primary[name]; ok {
		return v, true, nil
	}
	if v, ok := env[name]; ok {
		return v, true, nil
	}
	return "", false, nil
}

// envSecrets returns env-provided secrets (ARCLOOM_SECRET_<name>, excluding the
// reserved master key ARCLOOM_SECRET_KEY).
func envSecrets() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(k, envPrefix) {
			continue
		}
		name := strings.TrimPrefix(k, envPrefix)
		if name == "" || name == masterKeySuffix {
			continue
		}
		out[name] = v
	}
	return out
}

func (s *Store) loadLocal(projectID string) (map[string]string, error) {
	m := map[string]string{}
	if !validProjectID(projectID) {
		return nil, fmt.Errorf("invalid project id %q", projectID)
	}
	if s.key == nil {
		return m, nil
	}
	data, err := os.ReadFile(s.path(projectID))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	// Fall back to the previous generation's key (an unfinished rotation) and to
	// the pre-KDF key (an unfinished sha256 → argon2id migration) so a file the
	// current key can't open is still served rather than reported as lost.
	plain, ok := tryDecrypt(data, s.decryptKeys())
	if !ok {
		// Names the project, not the file path: this reaches the API response.
		return nil, fmt.Errorf("decrypt secrets for project %s (wrong ARCLOOM_SECRET_KEY?)", projectID)
	}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// migrateLegacy re-encrypts any pre-KDF (.enc files sealed with the bare sha256
// key) under the new argon2id key. Best-effort: a file that can't be read or
// rewritten is left in place — loadLocal still decrypts it via the legacy-key
// fallback, and it is re-sealed on the next edit. Runs only during NewStore
// (before the store is shared), so it needs no lock.
func (s *Store) migrateLegacy() {
	if s.legacyKey == nil {
		return
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".enc") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".enc")
		if !validProjectID(id) {
			continue
		}
		data, err := os.ReadFile(s.path(id))
		if err != nil {
			continue
		}
		if _, err := decrypt(s.key, data); err == nil {
			continue // already under the new key
		}
		plain, err := decrypt(s.legacyKey, data)
		if err != nil {
			continue // unreadable with either key; leave it untouched
		}
		enc, err := encrypt(s.key, plain)
		if err != nil {
			continue
		}
		_ = writeFileAtomic(s.path(id), enc, 0o600)
	}
}

func (s *Store) saveLocal(projectID string, m map[string]string) error {
	if !validProjectID(projectID) {
		return fmt.Errorf("invalid project id %q", projectID)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	enc, err := encrypt(s.key, plain)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path(projectID), enc, 0o600)
}

// argon2id KDF parameters for stretching the master passphrase into the AES
// key. Same cost as password hashing (internal/auth); it runs once per store at
// startup, not per secret operation.
const (
	kdfMemory  = 64 * 1024 // 64 MiB
	kdfTime    = 3
	kdfThreads = 4
	kdfKeyLen  = 32
	kdfSaltLen = 16
)

// kdfFile is the per-store sidecar that persists the KDF salt so the derived key
// is reproducible across restarts.
const kdfFile = "kdf.json"

// kdfMeta is the persisted key-derivation record. The version lets the argon2id
// parameters evolve later without breaking existing stores.
type kdfMeta struct {
	Version int    `json:"version"`
	Salt    []byte `json:"salt"` // json encodes []byte as base64
	// PrevSalt is the previous generation's salt, recorded while a master-key
	// rotation is in flight so the old key stays derivable (from the old
	// passphrase) and the rotation can be resumed rather than half-lost.
	PrevSalt []byte `json:"prev_salt,omitempty"`
	// Rotating marks a rotation that has not finished verifying. Salt is then
	// the NEW generation's salt and PrevSalt the one blobs may still be under.
	Rotating bool `json:"rotating,omitempty"`
}

// MinMasterKeyLen is the shortest master passphrase accepted. It is not a login
// credential (there is no online guessing surface) and it is stretched with
// argon2id, but it protects every stored secret, so a two-character key is
// worth refusing outright.
const MinMasterKeyLen = 12

// DevMasterKey is the placeholder that shipped in the example docker-compose.yml.
// It is refused BY NAME rather than merely being changed in that file, because
// it sat in a public repository: an install already running on it has its secret
// store, SSO client secret, git tokens, Vault token and notification URLs sealed
// under a key anyone can read, and refusing to boot is the only way that install
// ever finds out.
const DevMasterKey = "test_arcloom_secret_dev"

// ValidateMasterKey reports whether a master passphrase is fit to seal data.
//
// An empty key is valid and untouched: it means "no local secret store", which
// is a supported configuration (an external store, or a project with no
// secrets). The checks apply only once a key is actually supplied.
//
// This lives here, next to DeriveKey, because all three entry points that take a
// master key must agree: the server at boot, `arcloom rekey`, and the rotation
// endpoint. The length rule previously existed only on the rotation path, so a
// key too weak to rotate *to* could still be booted *with*.
func ValidateMasterKey(key string) error {
	if key == "" {
		return nil
	}
	if key == DevMasterKey {
		return errors.New("this is the placeholder from the example docker-compose.yml and is public; " +
			"generate one with `openssl rand -base64 32`, then re-seal existing data with `arcloom rekey`")
	}
	if len(key) < MinMasterKeyLen {
		return fmt.Errorf("key is %d characters; at least %d are required "+
			"(generate one with `openssl rand -base64 32`)", len(key), MinMasterKeyLen)
	}
	return nil
}

// DeriveKey stretches a master passphrase into a 32-byte AES key with argon2id
// against salt, or returns nil when the passphrase is empty. Exposed so other
// app-level encrypted stores (e.g. internal/settings) share one KDF.
func DeriveKey(masterKey string, salt []byte) []byte {
	if masterKey == "" {
		return nil
	}
	return argon2.IDKey([]byte(masterKey), salt, kdfTime, kdfMemory, kdfThreads, kdfKeyLen)
}

// LegacyKey is the pre-KDF derivation — a bare sha256 of the passphrase, with no
// salt — retained ONLY to decrypt and migrate stores written before argon2id
// landed. Never use it to seal new data.
func LegacyKey(masterKey string) []byte {
	if masterKey == "" {
		return nil
	}
	h := sha256.Sum256([]byte(masterKey))
	return h[:]
}

// NewSalt returns a fresh random KDF salt.
func NewSalt() ([]byte, error) {
	salt := make([]byte, kdfSaltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	return salt, nil
}

// readKDF reads <dir>/kdf.json. ok is false when the store has no record yet
// (a fresh install, or one written before the argon2id KDF landed).
func readKDF(dir string) (m kdfMeta, ok bool, err error) {
	data, rerr := os.ReadFile(filepath.Join(dir, kdfFile))
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return kdfMeta{}, false, nil
		}
		return kdfMeta{}, false, rerr
	}
	if jerr := json.Unmarshal(data, &m); jerr != nil || len(m.Salt) == 0 {
		return kdfMeta{}, false, fmt.Errorf("corrupt KDF metadata in %s", kdfFile)
	}
	return m, true, nil
}

// writeKDF persists the key-derivation record, replacing it atomically so a
// crash mid-write can never leave a store with no salt at all.
func writeKDF(dir string, m kdfMeta) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	blob, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, kdfFile), blob, 0o600)
}

// loadOrCreateKDF reads the store's key-derivation record, minting and
// persisting a fresh salt on first use. created is true only when a new salt was
// generated, signalling the caller to migrate any pre-KDF ciphertext.
func loadOrCreateKDF(dir string) (m kdfMeta, created bool, err error) {
	m, ok, err := readKDF(dir)
	if err != nil {
		return kdfMeta{}, false, err
	}
	if ok {
		return m, false, nil
	}
	// First use: mint and persist a salt before any data is sealed with the key
	// derived from it, so a crash can never orphan already-encrypted files.
	salt, err := NewSalt()
	if err != nil {
		return kdfMeta{}, false, err
	}
	m = kdfMeta{Version: 1, Salt: salt}
	if err := writeKDF(dir, m); err != nil {
		return kdfMeta{}, false, err
	}
	return m, true, nil
}

// writeFileAtomic writes data to a temporary sibling and renames it into place,
// so a reader never observes a half-written file and a crash leaves either the
// old content or the new one.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Encrypt/Decrypt expose the AES-256-GCM primitives for app-level encrypted
// values outside the per-project secret store.
func Encrypt(key, plaintext []byte) ([]byte, error) { return encrypt(key, plaintext) }
func Decrypt(key, data []byte) ([]byte, error)      { return decrypt(key, data) }

func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && i > 0)
		if !ok {
			return false
		}
	}
	return true
}
