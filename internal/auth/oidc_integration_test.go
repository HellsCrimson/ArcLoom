package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// mockIdP is a minimal OIDC identity provider: discovery doc, JWKS, and a token
// endpoint that mints a signed ID token. It exercises the real go-oidc
// verification path (discovery, JWKS fetch, signature, audience, nonce).
type mockIdP struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	nonce    string // nonce to embed in the minted id_token
	sub      string
	email    string
	extra    map[string]any // additional claims (e.g. groups) to mint
}

func newMockIdP(t *testing.T, clientID string) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &mockIdP{key: key, clientID: clientID, sub: "sub-123", email: "user@idp.test"}

	mux := http.NewServeMux()
	idp.server = httptest.NewServer(mux)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONRaw(w, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/auth",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &idp.key.PublicKey, KeyID: "1", Algorithm: "RS256", Use: "sig",
		}}}
		writeJSONRaw(w, set)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSONRaw(w, map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"id_token":     idp.mintIDToken(t),
		})
	})
	return idp
}

func (idp *mockIdP) mintIDToken(t *testing.T) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{
		"iss":                idp.server.URL,
		"sub":                idp.sub,
		"aud":                idp.clientID,
		"exp":                time.Now().Add(time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"nonce":              idp.nonce,
		"email":              idp.email,
		"preferred_username": "alice",
	}
	for k, v := range idp.extra {
		claims[k] = v
	}
	payload, _ := json.Marshal(claims)
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeJSONRaw(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestOIDCProvider_RealExchange(t *testing.T) {
	idp := newMockIdP(t, "my-client")
	defer idp.server.Close()
	idp.nonce = "n-42"

	ctx := context.Background()
	p, err := NewOIDCProvider(ctx, idp.server.URL, "my-client", "secret", "https://app/callback", []string{"email"}, "")
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}

	// AuthURL carries state + nonce and points at the IdP.
	u := p.AuthURL("st-1", "n-42")
	if !strings.Contains(u, "state=st-1") || !strings.Contains(u, "nonce=n-42") {
		t.Fatalf("AuthURL missing state/nonce: %s", u)
	}

	// Exchange verifies the signed ID token and maps claims.
	id, err := p.Exchange(ctx, "any-code", "n-42")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Provider != ProviderOIDC || id.Subject != "sub-123" || id.Email != "user@idp.test" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if id.DisplayName != "alice" {
		t.Errorf("display name from preferred_username expected, got %q", id.DisplayName)
	}
}

func TestOIDCProvider_NonceMismatchRejected(t *testing.T) {
	idp := newMockIdP(t, "my-client")
	defer idp.server.Close()
	idp.nonce = "server-nonce"

	p, err := NewOIDCProvider(context.Background(), idp.server.URL, "my-client", "secret", "https://app/callback", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// Caller expects a different nonce than the token carries → reject (replay).
	if _, err := p.Exchange(context.Background(), "code", "expected-different"); err == nil {
		t.Fatal("expected nonce mismatch to be rejected")
	}
}

// TestOIDCProvider_GroupsClaim checks group extraction against a real signed ID
// token: the default "groups" claim, a provider-specific claim name, and an
// absent claim (which must not fail the login).
func TestOIDCProvider_GroupsClaim(t *testing.T) {
	t.Run("default groups claim", func(t *testing.T) {
		idp := newMockIdP(t, "my-client")
		defer idp.server.Close()
		idp.nonce = "n"
		idp.extra = map[string]any{"groups": []any{"/platform-admins", "devs"}}

		p, err := NewOIDCProvider(context.Background(), idp.server.URL, "my-client", "s", "https://app/cb", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		id, err := p.Exchange(context.Background(), "code", "n")
		if err != nil {
			t.Fatal(err)
		}
		if len(id.Groups) != 2 || id.Groups[0] != "/platform-admins" || id.Groups[1] != "devs" {
			t.Fatalf("groups = %v", id.Groups)
		}
	})

	t.Run("custom claim name", func(t *testing.T) {
		idp := newMockIdP(t, "my-client")
		defer idp.server.Close()
		idp.nonce = "n"
		// Entra ID-style: roles rather than groups, and the default claim is empty.
		idp.extra = map[string]any{"roles": []any{"ops"}, "groups": []any{"ignored"}}

		p, err := NewOIDCProvider(context.Background(), idp.server.URL, "my-client", "s", "https://app/cb", nil, "roles")
		if err != nil {
			t.Fatal(err)
		}
		id, err := p.Exchange(context.Background(), "code", "n")
		if err != nil {
			t.Fatal(err)
		}
		if len(id.Groups) != 1 || id.Groups[0] != "ops" {
			t.Fatalf("groups = %v (should come from the configured claim only)", id.Groups)
		}
	})

	t.Run("absent claim still logs in", func(t *testing.T) {
		idp := newMockIdP(t, "my-client")
		defer idp.server.Close()
		idp.nonce = "n"

		p, err := NewOIDCProvider(context.Background(), idp.server.URL, "my-client", "s", "https://app/cb", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		id, err := p.Exchange(context.Background(), "code", "n")
		if err != nil {
			t.Fatalf("a missing groups claim must not break authentication: %v", err)
		}
		if len(id.Groups) != 0 {
			t.Fatalf("groups = %v, want none", id.Groups)
		}
	})
}
