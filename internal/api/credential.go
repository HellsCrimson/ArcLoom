package api

import (
	"net/http"
	"slices"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/oauth"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// credential describes the non-browser credential that authenticated a request,
// whichever kind it is. An API key and an OAuth grant differ completely in how
// they are obtained and revoked, but they answer the same three questions once
// a request is in flight: which capabilities, which projects, and what do we
// call this in the audit log.
//
// Collapsing them here is what lets OAuth reuse every gate unchanged — the
// perm middleware, the project ACL, the admin refusal, the MCP tool filter and
// the audit attribution all took a key before and take a credential now,
// with no second implementation of the "restricts, never escalates" rule.
type credential struct {
	// Kind is credKindAPIKey or credKindOAuth — reported in the audit trail so
	// a pasted key and an approved grant are distinguishable after the fact.
	Kind string
	// ID is the key id or grant id: what an admin revokes.
	ID string
	// Label is what a human reads — the key's label, or the OAuth client's name.
	Label string
	// Scopes are the granted capabilities (user.Perm* values). Empty = read-only.
	Scopes []string
	// Projects restricts the credential to specific project ids. Empty = every
	// project the owner can already see.
	Projects []string
}

const (
	credKindAPIKey = "apikey"
	credKindOAuth  = "oauth"
)

// HasScope reports whether the credential grants a capability.
func (c *credential) HasScope(scope string) bool { return slices.Contains(c.Scopes, scope) }

// AllowsProject reports whether the credential may act on a project.
func (c *credential) AllowsProject(id string) bool {
	return len(c.Projects) == 0 || slices.Contains(c.Projects, id)
}

// credFromKey adapts a stored API key.
func credFromKey(k *apikey.Key) *credential {
	return &credential{
		Kind: credKindAPIKey, ID: k.ID, Label: k.Label,
		Scopes: k.Scopes, Projects: k.Projects,
	}
}

// credFromGrant adapts an OAuth grant. The label is the client's registered
// name ("Claude Code"), which is what the consent screen showed the user and so
// what they will recognise in the audit log and the connected-apps list.
func credFromGrant(g *oauth.Grant) *credential {
	label := g.ClientName
	if label == "" {
		label = g.ClientID
	}
	return &credential{
		Kind: credKindOAuth, ID: g.ID, Label: label,
		Scopes: g.Scopes, Projects: g.Projects,
	}
}

// credCtxKey keys the *credential that authenticated a request. Absent for a
// cookie session — which is what every "is this a non-browser caller?" check
// keys off.
type credCtxKey struct{}

// credentialFromContext returns the credential that authenticated the request.
func credentialFromContext(r *http.Request) (*credential, bool) {
	c, ok := r.Context().Value(credCtxKey{}).(*credential)
	return c, ok
}

// credDenies returns a human-readable refusal when the request's credential does
// not permit an action, or "" when it does (including when the request is a
// plain browser session).
//
// perm may be "" for a read, which needs no scope but still honours a project
// restriction. projectID may be "" for a route with no {id}.
//
// This is the whole of the "a credential restricts, never escalates" rule: it
// runs *after* the user's own permission and the project ACL have already
// passed, so it can only ever subtract.
func credDenies(r *http.Request, perm, projectID string) string {
	c, ok := credentialFromContext(r)
	if !ok {
		return ""
	}
	noun := "API key"
	if c.Kind == credKindOAuth {
		noun = "authorization"
	}
	if perm != "" && !c.HasScope(perm) {
		return "this " + noun + " does not have the \"" + perm + "\" scope"
	}
	if projectID != "" && !c.AllowsProject(projectID) {
		return "this " + noun + " is not scoped to this project"
	}
	return ""
}

// effectiveUser returns what a credential can actually do: for a cookie session
// the user unchanged, otherwise the intersection of the user's permissions with
// the credential's scopes, with Admin cleared.
//
// It is a *view* for reporting (/auth/me, and the MCP tool filter), not the
// enforcement path — enforcement stays in the middleware, where a mistake fails
// closed. Both must agree, which is why they share this one definition of the
// intersection.
func effectiveUser(u *user.User, c *credential) *user.User {
	if u == nil || c == nil {
		return u
	}
	cp := *u
	cp.Admin = false
	perms := []string{}
	for _, p := range user.AllPermissions() {
		if u.Can(p) && c.HasScope(p) {
			perms = append(perms, p)
		}
	}
	cp.Permissions = perms
	return &cp
}
