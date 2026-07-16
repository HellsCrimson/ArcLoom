// Package user persists application user accounts behind a small interface so an
// in-memory implementation (tests) and a disk-backed one (production) are
// interchangeable, mirroring internal/store.
//
// A user may originate from local password login (Provider "local", with a
// PasswordHash) or, later, from an SSO provider (Provider "oidc", PasswordHash
// empty, Subject set for account linking). The model accommodates both without
// migration.
package user

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a user does not exist.
var ErrNotFound = errors.New("user not found")

// ErrExists is returned when creating a user whose username already exists.
var ErrExists = errors.New("user already exists")

// User is an application account.
type User struct {
	ID           string    `yaml:"id"`
	Username     string    `yaml:"username"`
	Email        string    `yaml:"email"`
	PasswordHash string    `yaml:"password_hash"` // argon2id encoded; empty for SSO-only users
	Provider     string    `yaml:"provider"`      // "local" | "oidc" (origin of the account)
	Subject      string    `yaml:"subject"`       // external subject for SSO linkage (empty for pure-local)
	CreatedAt    time.Time `yaml:"created_at"`
	Disabled     bool      `yaml:"disabled"`
}

// Store persists users.
type Store interface {
	// Create stores a new user; returns ErrExists if the username is taken.
	Create(u *User) error
	// Get returns a user by id.
	Get(id string) (*User, error)
	// ByUsername returns a user by username.
	ByUsername(username string) (*User, error)
	// BySubject returns a user by (provider, subject) for SSO account linking.
	BySubject(provider, subject string) (*User, error)
	// List returns all users (order unspecified).
	List() ([]*User, error)
	// Update runs fn against the stored user under a write lock and persists any
	// mutations fn makes.
	Update(id string, fn func(*User) error) error
	// Delete removes a user.
	Delete(id string) error
}
