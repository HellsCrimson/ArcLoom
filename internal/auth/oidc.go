package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCProvider authenticates users via an OpenID Connect identity provider. It
// implements RedirectProvider, so the API layer treats it exactly like any
// other login mechanism: it produces an Identity, which is linked to (or
// provisions) a user.User and mints the same server-side session as local login.
type OIDCProvider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	// groupsClaim is the ID-token claim holding group membership; DefaultGroupsClaim
	// when empty.
	groupsClaim string
}

// DefaultGroupsClaim is the claim consulted for group membership when none is
// configured.
const DefaultGroupsClaim = "groups"

// NewOIDCProvider performs OIDC discovery against issuer and builds the provider.
// scopes are appended to the mandatory "openid" scope. groupsClaim names the
// claim to read group membership from ("groups" when empty).
func NewOIDCProvider(ctx context.Context, issuer, clientID, clientSecret, redirectURL string, scopes []string, groupsClaim string) (*OIDCProvider, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery on %q: %w", issuer, err)
	}
	if groupsClaim == "" {
		groupsClaim = DefaultGroupsClaim
	}
	return &OIDCProvider{
		oauth: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       append([]string{oidc.ScopeOpenID}, scopes...),
		},
		verifier:    provider.Verifier(&oidc.Config{ClientID: clientID}),
		groupsClaim: groupsClaim,
	}, nil
}

func (p *OIDCProvider) Name() string { return ProviderOIDC }

// AuthURL builds the IdP authorization URL. state guards against CSRF (the
// handler checks it against a cookie); nonce guards against ID-token replay
// (verified in Exchange).
func (p *OIDCProvider) AuthURL(state, nonce string) string {
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce))
}

// oidcClaims are the ID-token claims we consume.
type oidcClaims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

// Exchange swaps the authorization code for tokens, verifies the ID token
// (signature, audience, expiry) and the nonce, and maps claims to an Identity.
func (p *OIDCProvider) Exchange(ctx context.Context, code, nonce string) (Identity, error) {
	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Identity{}, errors.New("no id_token in token response")
	}
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return Identity{}, fmt.Errorf("verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return Identity{}, errors.New("nonce mismatch")
	}
	var c oidcClaims
	if err := idToken.Claims(&c); err != nil {
		return Identity{}, fmt.Errorf("parse claims: %w", err)
	}
	if c.Subject == "" {
		return Identity{}, errors.New("id_token has no subject")
	}
	// Groups come from a configurable claim, so decode the raw set too. A failure
	// here is not fatal: authentication succeeded, the user just gets no
	// group-derived access.
	var raw map[string]any
	_ = idToken.Claims(&raw)

	display := firstNonEmpty(c.PreferredUsername, c.Name, c.Email, c.Subject)
	return Identity{
		Subject:     c.Subject,
		Provider:    ProviderOIDC,
		Email:       c.Email,
		DisplayName: display,
		Groups:      GroupsFromClaim(raw[p.groupsClaim]),
		Claims: map[string]string{
			"preferred_username": c.PreferredUsername,
			"email":              c.Email,
			"name":               c.Name,
		},
	}, nil
}

// GroupsFromClaim normalizes a groups claim value. IdPs are inconsistent here:
// most send a JSON array of strings, some a single string, and some a
// space/comma-separated list — all three are accepted. Anything else yields no
// groups rather than an error, since a missing or odd claim must not break login.
func GroupsFromClaim(v any) []string {
	switch t := v.(type) {
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return t
	case string:
		return strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' })
	default:
		return nil
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
