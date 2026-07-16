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
	"encoding/base64"
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
