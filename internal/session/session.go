// Package session persists server-side login sessions behind a small interface
// so an in-memory implementation (tests) and a disk-backed one (production) are
// interchangeable, mirroring internal/store.
//
// Sessions are opaque random tokens (never JWTs): the token is the only thing
// the client holds (in an httpOnly cookie), and all session data lives
// server-side so logout/revocation is instant. Disk-backed sessions survive
// process restarts, which self-hosted deployments restart often.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when a token has no live (unexpired) session.
var ErrNotFound = errors.New("session not found")

// tokenBytes is the entropy of a session token (256 bits).
const tokenBytes = 32

// Session is a server-side login session.
type Session struct {
	Token     string    `yaml:"-"` // the map key / filename; never serialized into the body
	UserID    string    `yaml:"user_id"`
	Provider  string    `yaml:"provider"` // which auth provider minted this session
	CreatedAt time.Time `yaml:"created_at"`
	ExpiresAt time.Time `yaml:"expires_at"`
}

// Store persists sessions.
type Store interface {
	// Create mints a new session with a random token, expiring after ttl.
	Create(userID, provider string, ttl time.Duration) (*Session, error)
	// Get returns a session by token, or ErrNotFound if missing or expired.
	Get(token string) (*Session, error)
	// Delete revokes a single session (logout). Deleting a missing token is a
	// no-op.
	Delete(token string) error
	// DeleteByUser revokes all sessions for a user (ban / password change).
	DeleteByUser(userID string) error
	// GC purges expired sessions.
	GC() error
}

// newToken returns a URL-safe random session token.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the stored form of a session token.
//
// The token used to BE the filename, which meant anyone who could list the
// sessions directory — a backup, a stray tar, a copied volume — held live
// credentials outright, with no cracking step. internal/apikey and
// internal/oauth already stored a hash for exactly this reason; sessions were
// the odd one out rather than a considered exception.
//
// SHA-256 rather than a memory-hard KDF, for the same reason those two give: a
// token is 256 bits of CSPRNG output with no search space to slow down, and
// argon2 on every authenticated request would be a self-inflicted DoS.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
