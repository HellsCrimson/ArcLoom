package auth

import (
	"context"

	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// LocalProvider authenticates users against password hashes in a user.Store.
// It implements CredentialProvider.
type LocalProvider struct {
	users user.Store
}

// NewLocalProvider builds a local password provider backed by users.
func NewLocalProvider(users user.Store) *LocalProvider {
	return &LocalProvider{users: users}
}

func (p *LocalProvider) Name() string { return ProviderLocal }

// Authenticate verifies a username/password against the user store. It returns
// ErrInvalidCredentials for any failure (unknown user, wrong password, disabled
// account, or SSO-only user) so callers cannot enumerate valid usernames.
func (p *LocalProvider) Authenticate(_ context.Context, username, password string) (Identity, error) {
	u, err := p.users.ByUsername(username)
	if err != nil || u == nil {
		return Identity{}, ErrInvalidCredentials
	}
	if u.Disabled || u.PasswordHash == "" {
		return Identity{}, ErrInvalidCredentials
	}
	ok, err := Verify(u.PasswordHash, password)
	if err != nil || !ok {
		return Identity{}, ErrInvalidCredentials
	}
	return Identity{
		Subject:     u.ID,
		Provider:    ProviderLocal,
		Email:       u.Email,
		DisplayName: u.Username,
	}, nil
}
