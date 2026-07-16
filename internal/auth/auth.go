// Package auth is the provider-agnostic authentication core. It knows how to
// turn a principal into an Identity; it does NOT know about HTTP, cookies, or
// sessions (those live in internal/api). Local password login and, later, OIDC
// SSO are two Providers that both converge on Identity — so adding SSO is a new
// Provider implementation plus two endpoints, with no change to the middleware,
// session store, or frontend gate.
package auth

import (
	"context"
	"errors"
)

// Provider names.
const (
	ProviderLocal = "local"
	ProviderOIDC  = "oidc"
)

var (
	// ErrInvalidCredentials is returned when a username/password pair is wrong.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrProviderDisabled is returned when a requested provider is not configured.
	ErrProviderDisabled = errors.New("provider disabled")
)

// Identity is the provider-neutral result of a successful authentication.
// LocalProvider fills it from a user record; a future OIDCProvider fills it
// from verified ID-token claims.
type Identity struct {
	Subject     string            // stable unique id within the provider
	Provider    string            // ProviderLocal | ProviderOIDC
	Email       string
	DisplayName string
	Claims      map[string]string // optional extras (groups, roles) for future authz
}

// Provider is the base type every auth mechanism implements.
type Provider interface {
	Name() string
}

// CredentialProvider verifies a direct username/password (LocalProvider). SSO
// providers do NOT implement this.
type CredentialProvider interface {
	Provider
	Authenticate(ctx context.Context, username, password string) (Identity, error)
}

// RedirectProvider is implemented by browser-redirect providers (OIDC/SAML).
// The handler owns state (CSRF, validated against a cookie); the provider owns
// nonce (ID-token replay protection, verified inside Exchange).
type RedirectProvider interface {
	Provider
	AuthURL(state, nonce string) string
	Exchange(ctx context.Context, code, nonce string) (Identity, error)
}

// Authenticator is the registry the API layer holds. It hides which providers
// exist so handlers and middleware never branch on provider type: adding OIDC
// is one more provider passed to New.
type Authenticator struct {
	providers []Provider
	byName    map[string]Provider
}

// New builds an Authenticator indexing providers by Name().
func New(providers ...Provider) *Authenticator {
	a := &Authenticator{byName: map[string]Provider{}}
	for _, p := range providers {
		if p == nil {
			continue
		}
		a.providers = append(a.providers, p)
		a.byName[p.Name()] = p
	}
	return a
}

// Local returns the credential (password) provider, if one is configured.
func (a *Authenticator) Local() (CredentialProvider, bool) {
	for _, p := range a.providers {
		if cp, ok := p.(CredentialProvider); ok {
			return cp, true
		}
	}
	return nil, false
}

// Redirect returns the named redirect (SSO) provider, if configured.
func (a *Authenticator) Redirect(name string) (RedirectProvider, bool) {
	p, ok := a.byName[name]
	if !ok {
		return nil, false
	}
	rp, ok := p.(RedirectProvider)
	return rp, ok
}

// Enabled lists the names of configured providers, in registration order. The
// frontend uses this to decide which login options to show.
func (a *Authenticator) Enabled() []string {
	out := make([]string, 0, len(a.providers))
	for _, p := range a.providers {
		out = append(out, p.Name())
	}
	return out
}
