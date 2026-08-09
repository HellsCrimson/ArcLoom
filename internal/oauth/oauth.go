// Package oauth implements the authorization-server half of MCP's auth spec:
// dynamic client registration, PKCE authorization codes, and access/refresh
// tokens. It exists so an MCP client can say "let me in" and have the user
// approve in ArcLoom's own UI, instead of the user pasting a long-lived API key
// into a config file.
//
// Three deliberate limits keep this small enough to be auditable:
//
//   - **Public clients only.** Clients register dynamically and hold no secret,
//     so PKCE is mandatory rather than optional. A confidential-client flow
//     would add a credential to store and rotate for no gain here.
//   - **Opaque tokens, never JWTs** — the same call internal/session made. All
//     token state lives server-side, so revoking a grant is instant and total
//     rather than "wait for it to expire".
//   - **Scopes are ArcLoom's own vocabulary** (resources/deploy/git/approve),
//     chosen by the *user* on the consent screen rather than requested by the
//     client. A generic MCP client has no idea what ArcLoom's capabilities are;
//     the person approving does.
package oauth

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

var (
	// ErrNotFound is returned when a client, grant or token does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is a single error for every "this credential is no good"
	// case — wrong, expired, revoked, already used. Distinguishing them for the
	// caller would tell an attacker which half they got right.
	ErrInvalid = errors.New("invalid")
	// ErrRefreshReused reports that a refresh token from a PREVIOUS generation
	// was presented, which means two parties hold the grant. It is internal:
	// the token endpoint answers with the same invalid_grant either way, for the
	// reason above, but the grant is revoked and the event is logged.
	ErrRefreshReused = errors.New("refresh token reused")
)

// Lifetimes. The access token is short so a leaked one is a small window; the
// refresh token is long because the alternative is asking a human to
// re-authorize an agent every day, which they will route around.
const (
	CodeTTL    = 2 * time.Minute
	AccessTTL  = time.Hour
	RefreshTTL = 90 * 24 * time.Hour
)

// Client is a dynamically registered OAuth client (RFC 7591). It carries no
// secret: registration is open, so a client id proves nothing on its own and
// security rests on PKCE plus the user's explicit consent.
type Client struct {
	ID           string    `yaml:"id"`
	Name         string    `yaml:"name"`
	RedirectURIs []string  `yaml:"redirect_uris"`
	CreatedAt    time.Time `yaml:"created_at"`
}

// AllowsRedirect reports whether uri was registered. Redirect URIs are matched
// **exactly**, never by prefix: a prefix match is the classic open-redirect that
// turns an authorization code into someone else's.
func (c *Client) AllowsRedirect(uri string) bool { return slices.Contains(c.RedirectURIs, uri) }

// Grant is one user's standing authorization for one client: what it may do,
// and the refresh token that lets it keep doing it. Revoking the grant revokes
// every token derived from it.
type Grant struct {
	ID       string   `yaml:"id"`
	ClientID string   `yaml:"client_id"`
	UserID   string   `yaml:"user_id"`
	Scopes   []string `yaml:"scopes,omitempty"`
	Projects []string `yaml:"projects,omitempty"`
	// RefreshHash is the SHA-256 of the current refresh token. Rotated on every
	// use (OAuth 2.1), so a replayed refresh token is detectably stale.
	RefreshHash string `yaml:"refresh_hash"`
	// PrevRefreshHash is the generation before it, kept so that presenting an
	// already-rotated token is DETECTED rather than merely failing. Rotation
	// alone makes a stolen token stop working; it does not say that a theft
	// happened, and whichever party redeems second is the one locked out — which
	// is as likely to be the legitimate client as the thief. Seeing the previous
	// generation is the signal that two parties hold the same grant, and the
	// only safe response is to revoke it (see ErrRefreshReused).
	PrevRefreshHash string `yaml:"prev_refresh_hash,omitempty"`
	CreatedAt   time.Time  `yaml:"created_at"`
	ExpiresAt   time.Time  `yaml:"expires_at"`
	LastUsedAt  *time.Time `yaml:"last_used_at,omitempty"`
	ClientName  string     `yaml:"client_name,omitempty"`
}

// HasScope reports whether the grant carries a capability.
func (g *Grant) HasScope(s string) bool { return slices.Contains(g.Scopes, s) }

// AllowsProject reports whether the grant covers a project (empty = all).
func (g *Grant) AllowsProject(id string) bool {
	return len(g.Projects) == 0 || slices.Contains(g.Projects, id)
}

// Code is a pending authorization code: single-use, short-lived, and bound to
// both the PKCE challenge and the exact redirect URI it was issued for.
type Code struct {
	Hash      string    `yaml:"hash"`
	ClientID  string    `yaml:"client_id"`
	UserID    string    `yaml:"user_id"`
	Scopes    []string  `yaml:"scopes,omitempty"`
	Projects  []string  `yaml:"projects,omitempty"`
	Redirect  string    `yaml:"redirect"`
	Challenge string    `yaml:"challenge"` // PKCE S256 code_challenge
	ExpiresAt time.Time `yaml:"expires_at"`
}

// AccessToken is a live bearer token derived from a grant.
type AccessToken struct {
	Hash      string    `yaml:"hash"`
	GrantID   string    `yaml:"grant_id"`
	ExpiresAt time.Time `yaml:"expires_at"`
}

// Store persists the authorization server's state.
type Store interface {
	// RegisterClient stores a dynamically registered client.
	RegisterClient(c *Client) error
	// GetClient returns a client by id.
	GetClient(id string) (*Client, error)
	// CountClients reports how many clients are registered, so the open
	// registration endpoint can refuse to grow without bound.
	CountClients() (int, error)

	// SaveCode stores a pending authorization code.
	SaveCode(c *Code) error
	// ConsumeCode atomically fetches and deletes a code. Single-use is enforced
	// here, not by the caller: an authorization code that can be redeemed twice
	// is an account takeover.
	ConsumeCode(code string) (*Code, error)

	// SaveGrant creates or updates a grant.
	SaveGrant(g *Grant) error
	// GetGrant returns a grant by id.
	GetGrant(id string) (*Grant, error)
	// GrantByRefresh resolves a refresh token to its grant.
	GrantByRefresh(refresh string) (*Grant, error)
	// ListGrants returns one user's grants (the "connected apps" view).
	ListGrants(userID string) ([]*Grant, error)
	// DeleteGrant revokes a grant and every token derived from it.
	DeleteGrant(id string) error
	// DeleteGrantsByUser revokes all of a user's grants (account deletion).
	DeleteGrantsByUser(userID string) error

	// SaveAccess stores an issued access token.
	SaveAccess(t *AccessToken) error
	// GrantByAccess resolves a bearer token to its grant, honouring expiry.
	GrantByAccess(token string) (*Grant, error)

	// GC purges expired codes and access tokens.
	GC() error
}

// --- token helpers ---

// NewToken returns a URL-safe random token with 256 bits of entropy. Exported
// because the HTTP layer mints the codes and tokens; the store only stores their
// hashes.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newID returns a random hex id, safe as a filename path segment (the same rule
// internal/apikey follows, for the same reason).
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken is the stored form of every token here. SHA-256 rather than argon2
// for the reason internal/apikey documents: these are 256-bit CSPRNG values
// with no search space to slow down, and a memory-hard KDF on every request
// would be a self-inflicted DoS.
func HashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// subtleEqual compares two hashes in constant time.
func subtleEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// VerifyPKCE checks an S256 code verifier against the stored challenge.
//
// Only S256 is accepted. The spec also defines "plain", which offers no
// protection at all against an intercepted code — OAuth 2.1 drops it, and so do
// we, rather than letting a client negotiate its way down.
func VerifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(challenge), []byte(want)) == 1
}

// validID reports whether an id is safe as a path segment.
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

// ParseScopes splits a space-separated scope string.
func ParseScopes(s string) []string {
	out := []string{}
	for _, f := range strings.Fields(s) {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}
