// Package secrets stores and resolves secret values for injection into the
// Terraform/OpenTofu CLI at run time. Two backends are supported:
//
//   - "local": an AES-256-GCM encrypted file per project (never in Git). The
//     master key comes from ARCLOOM_SECRET_KEY.
//   - "env": values read from ARCLOOM_SECRET_<name> in the host environment.
//
// Plaintext secret values never enter the generated HCL; they reach the CLI
// only as TF_VAR_<name> at plan/apply time.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	Backend string `json:"backend"` // "local" | "env"
}

// Store manages per-project secrets across the local + env backends.
type Store struct {
	dir string
	key []byte // 32-byte AES key, or nil if the local backend is disabled
	// legacyKey is the pre-KDF (bare sha256) key, kept so .enc files written
	// before the argon2id KDF landed can still be decrypted and migrated.
	legacyKey []byte
	mu        sync.Mutex
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
	salt, created, err := loadOrCreateSalt(dir)
	if err != nil {
		// Can't establish the argon2id salt (disk error): fall back to the legacy
		// derivation so existing secrets stay readable. Migration is retried on
		// the next start once the salt file can be written.
		log.Printf("secrets: argon2id KDF unavailable (%v); using legacy key derivation", err)
		s.key = s.legacyKey
		return s
	}
	s.key = DeriveKey(masterKey, salt)
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

// Set stores a secret value in the local encrypted backend.
func (s *Store) Set(projectID, name, value string) error {
	if !validName(name) {
		return fmt.Errorf("invalid secret name %q (use letters, digits, underscore)", name)
	}
	if s.key == nil {
		return fmt.Errorf("local secret store disabled: set ARCLOOM_SECRET_KEY")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadLocal(projectID)
	if err != nil {
		return err
	}
	m[name] = value
	return s.saveLocal(projectID, m)
}

// Delete removes a secret from the local backend.
func (s *Store) Delete(projectID, name string) error {
	if s.key == nil {
		return fmt.Errorf("local secret store disabled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.loadLocal(projectID)
	if err != nil {
		return err
	}
	delete(m, name)
	return s.saveLocal(projectID, m)
}

// List returns secret metadata (names + backend) with no values.
func (s *Store) List(projectID string) ([]Meta, error) {
	s.mu.Lock()
	local, err := s.loadLocal(projectID)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Meta
	for name := range local {
		out = append(out, Meta{Name: name, Backend: "local"})
		seen[name] = true
	}
	for name := range envSecrets() {
		if seen[name] {
			continue
		}
		out = append(out, Meta{Name: name, Backend: "env"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve returns a secret's plaintext value (local store first, then env).
func (s *Store) Resolve(projectID, name string) (string, bool, error) {
	s.mu.Lock()
	local, err := s.loadLocal(projectID)
	s.mu.Unlock()
	if err != nil {
		return "", false, err
	}
	if v, ok := local[name]; ok {
		return v, true, nil
	}
	if v, ok := envSecrets()[name]; ok {
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
	plain, err := decrypt(s.key, data)
	if err != nil && s.legacyKey != nil {
		// Fall back to the pre-KDF key for a file not yet migrated (e.g. after an
		// interrupted migration).
		if p, e2 := decrypt(s.legacyKey, data); e2 == nil {
			plain, err = p, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets (wrong ARCLOOM_SECRET_KEY?): %w", err)
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
		_ = os.WriteFile(s.path(id), enc, 0o600)
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
	return os.WriteFile(s.path(projectID), enc, 0o600)
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

// loadOrCreateSalt reads the store's KDF salt from <dir>/kdf.json, minting and
// persisting a fresh one on first use. created is true only when a new salt was
// generated, signalling the caller to migrate any pre-KDF ciphertext.
func loadOrCreateSalt(dir string) (salt []byte, created bool, err error) {
	path := filepath.Join(dir, kdfFile)
	data, rerr := os.ReadFile(path)
	if rerr == nil {
		var m kdfMeta
		if jerr := json.Unmarshal(data, &m); jerr != nil || len(m.Salt) == 0 {
			return nil, false, fmt.Errorf("corrupt KDF metadata in %s", kdfFile)
		}
		return m.Salt, false, nil
	}
	if !os.IsNotExist(rerr) {
		return nil, false, rerr
	}
	// First use: mint and persist a salt before any data is sealed with the key
	// derived from it, so a crash can never orphan already-encrypted files.
	salt, err = NewSalt()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	blob, err := json.Marshal(kdfMeta{Version: 1, Salt: salt})
	if err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return nil, false, err
	}
	return salt, true, nil
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
