// Package apikey persists scoped, non-interactive API credentials behind a
// small interface so an in-memory implementation (tests) and a disk-backed one
// (production) are interchangeable, mirroring internal/session and
// internal/user.
//
// Why it exists: every credential ArcLoom had was a browser session cookie, so
// nothing that is not a browser — CI, a script, an MCP client — could talk to
// the API at all. A key is presented as `Authorization: Bearer <token>`.
//
// A key is a *restriction* on its owner, never an escalation. The effective
// permission for a request is
//
//	user.Can(perm) AND key.HasScope(perm) AND project ACL
//
// so demoting the owner narrows every key they minted, and Admin is
// deliberately not grantable (see Scopes) — a leaked key can never reach
// /admin/*, mint users, rewrite SSO or rotate the master key.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
)

// ErrNotFound is returned when a key id has no stored key.
var ErrNotFound = errors.New("api key not found")

// ErrInvalid is returned when a presented token is malformed, unknown, does not
// match its stored hash, is disabled, or has expired. It is deliberately a
// single error: telling a caller *which* of those is true tells an attacker
// whether an id exists.
var ErrInvalid = errors.New("invalid api key")

// Token layout: prefix "arcl", a lookup id, then the secret.
//
//	arcl_<id>_<secret>
//
// The id is **hex**, not base64url, for one specific reason: base64url's
// alphabet contains '_', so a base64url id could not be split back out of the
// token unambiguously. Hex also satisfies the "safe path segment" rule the id
// needs as a filename (see Disk).
const (
	tokenPrefix = "arcl"
	idBytes     = 12 // 24 hex chars
	secretBytes = 32 // 256 bits, base64url
)

// Scope constants intentionally reuse user.Perm* values verbatim (resources /
// deploy / git / approve) rather than inventing a second vocabulary — that is
// what lets the existing requirePerm / requireProjectPerm gates stay unchanged.
// They are referenced as strings here so this package does not import
// internal/user (which would make the dependency cycle user->apikey->user if
// user ever grows key awareness).
//
// Reads need no scope: any authenticated caller may read today, so a key with
// no scopes at all is a read-only key.

// Key is a stored API credential. The secret itself is never stored — only a
// hash of it — so a compromised key file cannot be replayed.
type Key struct {
	// ID is the lookup handle and the filename; it travels in the token in
	// clear.
	ID string `yaml:"id"`
	// Hash is the SHA-256 (hex) of the secret half.
	//
	// Deliberately NOT argon2id, unlike user.PasswordHash. Argon2's cost exists
	// to slow a dictionary attack on a low-entropy human password; a key secret
	// is 256 bits of CSPRNG output, so there is no search space to slow down
	// and the only thing a memory-hard KDF would buy is a 64 MiB allocation on
	// every single request — which for an MCP client (one HTTP request per tool
	// call) is a self-inflicted DoS.
	Hash string `yaml:"hash"`
	// UserID is the owning account. The key can never do more than this user.
	UserID string `yaml:"user_id"`
	// Label is a human name shown in the UI ("laptop", "ci", "claude-code").
	Label string `yaml:"label"`
	// Scopes are the granted capabilities (user.Perm* values). Empty = read-only.
	Scopes []string `yaml:"scopes,omitempty"`
	// Projects restricts the key to specific project ids. Empty = every project
	// the owner can already see.
	Projects []string `yaml:"projects,omitempty"`

	CreatedAt  time.Time  `yaml:"created_at"`
	ExpiresAt  *time.Time `yaml:"expires_at,omitempty"`   // nil = never expires
	LastUsedAt *time.Time `yaml:"last_used_at,omitempty"` // nil = never used
	Disabled   bool       `yaml:"disabled,omitempty"`
}

// HasScope reports whether the key grants a capability.
func (k *Key) HasScope(scope string) bool { return slices.Contains(k.Scopes, scope) }

// AllowsProject reports whether the key may act on a project. An unrestricted
// key (no Projects) allows every project — the owner's own rights and the
// per-project ACL still apply on top.
func (k *Key) AllowsProject(id string) bool {
	return len(k.Projects) == 0 || slices.Contains(k.Projects, id)
}

// Expired reports whether the key's expiry has passed.
func (k *Key) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && now.After(*k.ExpiresAt)
}

// Usable reports whether the key may authenticate a request right now.
func (k *Key) Usable(now time.Time) bool { return !k.Disabled && !k.Expired(now) }

// Store persists API keys.
type Store interface {
	// Create mints a new key, storing only the hash of its secret, and returns
	// the one-time full token. The caller must show that token to the user
	// once; it is unrecoverable afterwards.
	Create(k *Key) (token string, err error)
	// Get returns a key by id (no secret verification).
	Get(id string) (*Key, error)
	// Verify resolves a presented token to its key, checking the hash, the
	// disabled flag and the expiry. Returns ErrInvalid for every failure mode.
	Verify(token string) (*Key, error)
	// List returns every key (order unspecified). Used by the admin view.
	List() ([]*Key, error)
	// ListByUser returns one user's keys.
	ListByUser(userID string) ([]*Key, error)
	// Delete revokes a key. Deleting a missing id is a no-op.
	Delete(id string) error
	// DeleteByUser revokes all of a user's keys (account deletion).
	DeleteByUser(userID string) error
	// Touch records that a key was just used. Best-effort: a failure here must
	// never fail the request it is annotating.
	Touch(id string, at time.Time) error
}

// newID returns a random hex lookup id.
func newID() (string, error) {
	b := make([]byte, idBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newSecret returns a random URL-safe secret.
func newSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSecret returns the stored representation of a secret.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// matches compares a presented secret against a stored hash in constant time.
func matches(storedHash, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashSecret(secret))) == 1
}

// formatToken assembles the one-time token handed to the user.
func formatToken(id, secret string) string {
	return tokenPrefix + "_" + id + "_" + secret
}

// ParseToken splits a presented token into its id and secret halves. It does no
// I/O and no verification; it exists so the transport layer can find the stored
// key before doing any comparison.
//
// SplitN with n=3 is load-bearing: the secret is base64url and may itself
// contain '_', so only the first two separators may be treated as structural.
func ParseToken(token string) (id, secret string, ok bool) {
	parts := strings.SplitN(token, "_", 3)
	if len(parts) != 3 || parts[0] != tokenPrefix || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	if !validID(parts[1]) {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// validID reports whether an id is safe to use as a filename. Mirrors
// session.validToken and api.validID: this id becomes a path segment
// (<root>/<id>.yaml), so a forged token must not be able to escape the
// directory. Minted ids are hex; nothing else is accepted.
func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// prepare fills in the generated fields of a new key and returns its one-time
// token. Shared by Disk and Memory so both mint identically.
func prepare(k *Key) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	k.ID = id
	k.Hash = hashSecret(secret)
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now()
	}
	return formatToken(id, secret), nil
}
