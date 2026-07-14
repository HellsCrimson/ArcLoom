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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	mu  sync.Mutex
}

// NewStore builds a store. masterKey (from ARCLOOM_SECRET_KEY) enables the local
// encrypted backend when non-empty; it is hashed to a 32-byte AES key.
func NewStore(dir, masterKey string) *Store {
	var key []byte
	if masterKey != "" {
		h := sha256.Sum256([]byte(masterKey))
		key = h[:]
	}
	return &Store{dir: dir, key: key}
}

// LocalEnabled reports whether the local encrypted backend is usable.
func (s *Store) LocalEnabled() bool { return s.key != nil }

func (s *Store) path(projectID string) string {
	return filepath.Join(s.dir, projectID+".enc")
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
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets (wrong ARCLOOM_SECRET_KEY?): %w", err)
	}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) saveLocal(projectID string, m map[string]string) error {
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
