package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/oauth"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// OAuth 2.1 authorization server for the MCP endpoint.
//
// What it buys over an API key: nothing is pasted anywhere. `claude mcp add`
// carries no credential, the client discovers these endpoints, the user approves
// in ArcLoom's own UI (where they are already signed in), and the client holds a
// rotating refresh token instead of a permanent secret. The decisive case is
// several people sharing one connector URL: each authenticates as themselves, so
// the audit trail names the right human — which a shared key cannot do.
//
// What it does NOT change: a grant reduces to the same *credential* an API key
// does, so every downstream gate — perms, project ACL, the admin refusal, the
// MCP tool filter, audit attribution — is untouched.

// oauthEnabled reports whether the authorization server is mounted. It needs the
// store and the MCP endpoint it exists to serve.
func (s *Server) oauthEnabled() bool { return s.oauth != nil && s.mcpEnabled() }

// issuer derives the externally reachable base URL from the request. It is taken
// from the request rather than configured because a homelab install is reached
// by whatever name the user typed, and a configured issuer that disagrees with
// it fails discovery in a way nobody can debug.
//
// X-Forwarded-* is honoured so a reverse proxy terminating TLS advertises https,
// which is what its clients must be told to use.
func issuer(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host := r.Host
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		host = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	return scheme + "://" + host
}

// handleProtectedResourceMetadata is RFC 9728: it tells a client which
// authorization server guards this MCP endpoint. This is the document the 401's
// WWW-Authenticate header points at, and the first step of the whole flow.
func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := issuer(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + "/api/mcp",
		"authorization_servers":    []string{base},
		"scopes_supported":         user.AllPermissions(),
		"bearer_methods_supported": []string{"header"},
	})
}

// handleAuthServerMetadata is RFC 8414.
func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := issuer(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                   base,
		"authorization_endpoint":   base + "/oauth/authorize",
		"token_endpoint":           base + "/api/oauth/token",
		"registration_endpoint":    base + "/api/oauth/register",
		"response_types_supported": []string{"code"},
		"grant_types_supported":    []string{"authorization_code", "refresh_token"},
		// S256 only. "plain" offers no protection against an intercepted code;
		// OAuth 2.1 drops it and so do we, rather than letting a client negotiate
		// its way down to it.
		"code_challenge_methods_supported": []string{"S256"},
		"scopes_supported":                 user.AllPermissions(),
		// Public clients only — registration is open, so a client secret would
		// be a secret everyone has.
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

// handleRegisterClient is RFC 7591 dynamic client registration.
//
// Registration is deliberately open and unauthenticated: MCP clients cannot
// pre-register, and an id issued here grants nothing on its own — every request
// still needs a user to sit at the consent screen and approve. What it must do
// is pin the redirect URIs, because those are the only thing standing between an
// authorization code and whoever asked for it.
func (s *Server) handleRegisterClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_client_metadata", "invalid body")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthErr(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			oauthErr(w, http.StatusBadRequest, "invalid_redirect_uri", "unsupported redirect uri: "+u)
			return
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "Unnamed client"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	c := &oauth.Client{Name: name, RedirectURIs: req.RedirectURIs}
	if err := s.oauth.RegisterClient(c); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not register client")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// validRedirectURI accepts the two shapes a local MCP client uses: a loopback
// HTTP callback (the documented pattern — the client runs a one-shot listener on
// 127.0.0.1) and a custom scheme for a desktop app. Anything else — a remote
// http(s) URL — is refused, because a registered redirect is where the code goes
// and open registration means anyone could name any destination.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" && u.Scheme == "" {
		return false
	}
	switch u.Scheme {
	case "http":
		host := u.Hostname()
		return host == "127.0.0.1" || host == "localhost" || host == "::1"
	case "https":
		// Allowed only for loopback; a public https redirect would let a
		// registrant point the code at their own server.
		host := u.Hostname()
		return host == "127.0.0.1" || host == "localhost" || host == "::1"
	case "":
		return false
	default:
		// A custom scheme (e.g. "myapp://callback") is a desktop app claiming
		// its own scheme; there is no server to redirect to.
		return u.Scheme != "javascript" && u.Scheme != "data"
	}
}

// authorizeInfoDTO is what the consent screen renders.
type authorizeInfoDTO struct {
	ClientName string   `json:"clientName"`
	ClientID   string   `json:"clientId"`
	Redirect   string   `json:"redirect"`
	Scopes     []string `json:"scopes"`    // what this user could grant
	Requested  []string `json:"requested"` // what the client asked for, if anything
}

// handleAuthorizeInfo validates a pending authorization request and describes it
// for the consent screen. It requires a logged-in user (it is in the protected
// group), which is exactly the "you are already signed into ArcLoom" step that
// makes the flow one click.
func (s *Server) handleAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, redirect, msg := s.validateAuthRequest(q)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	u, _ := userFromContext(r)
	grantable := []string{}
	for _, p := range user.AllPermissions() {
		if u == nil || u.Can(p) {
			grantable = append(grantable, p)
		}
	}
	writeJSON(w, http.StatusOK, authorizeInfoDTO{
		ClientName: client.Name,
		ClientID:   client.ID,
		Redirect:   redirect,
		Scopes:     grantable,
		Requested:  oauth.ParseScopes(q.Get("scope")),
	})
}

// validateAuthRequest checks the query parameters common to both the info and
// approve steps.
func (s *Server) validateAuthRequest(q url.Values) (*oauth.Client, string, string) {
	if q.Get("response_type") != "code" {
		return nil, "", "only response_type=code is supported"
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		return nil, "", "PKCE with S256 is required"
	}
	client, err := s.oauth.GetClient(q.Get("client_id"))
	if err != nil {
		return nil, "", "unknown client"
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" || !client.AllowsRedirect(redirect) {
		return nil, "", "redirect_uri does not match the registered client"
	}
	return client, redirect, ""
}

// handleAuthorizeApprove records the user's decision and mints an authorization
// code. The response carries the redirect URL for the SPA to navigate to, rather
// than issuing a 302, because the consent screen is a client-side page.
func (s *Server) handleAuthorizeApprove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query    string   `json:"query"`    // the raw authorize query string
		Scopes   []string `json:"scopes"`   // what the user ticked
		Projects []string `json:"projects"` // optional project restriction
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	q, err := url.ParseQuery(strings.TrimPrefix(req.Query, "?"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid authorization request")
		return
	}
	client, redirect, msg := s.validateAuthRequest(q)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	u, ok := userFromContext(r)
	if !ok || u == nil {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// The same rule API keys follow: a grant may only ever be narrower than its
	// owner, checked here rather than at every later call.
	scopes, err := normalizeScopes(req.Scopes, u)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	projects, err := normalizeKeyProjects(req.Projects)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	code, err := oauth.NewToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue code")
		return
	}
	if err := s.oauth.SaveCode(&oauth.Code{
		Hash:      oauth.HashToken(code),
		ClientID:  client.ID,
		UserID:    u.ID,
		Scopes:    scopes,
		Projects:  projects,
		Redirect:  redirect,
		Challenge: q.Get("code_challenge"),
		ExpiresAt: time.Now().Add(oauth.CodeTTL),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue code")
		return
	}

	// Enrich the audit entry the middleware is about to write: the path says
	// nothing about which client got which scopes, and that is the whole point
	// of recording a grant.
	granted := strings.Join(scopes, ",")
	if granted == "" {
		granted = "read-only"
	}
	*r = *withAuditDetail(r, client.Name+" scopes="+granted)

	target, _ := url.Parse(redirect)
	rq := target.Query()
	rq.Set("code", code)
	if st := q.Get("state"); st != "" {
		rq.Set("state", st)
	}
	target.RawQuery = rq.Encode()
	writeJSON(w, http.StatusOK, map[string]string{"redirect": target.String()})
}

// handleToken is the token endpoint: authorization_code and refresh_token.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "could not parse form")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.tokenFromCode(w, r)
	case "refresh_token":
		s.tokenFromRefresh(w, r)
	default:
		oauthErr(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

func (s *Server) tokenFromCode(w http.ResponseWriter, r *http.Request) {
	code, err := s.oauth.ConsumeCode(r.Form.Get("code"))
	if err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "invalid or expired code")
		return
	}
	// Both bindings matter: the verifier proves the redeemer is the client that
	// started the flow, and the redirect_uri match stops a code issued for one
	// destination being redeemed against another.
	if !oauth.VerifyPKCE(code.Challenge, r.Form.Get("code_verifier")) {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	if r.Form.Get("redirect_uri") != code.Redirect {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if cid := r.Form.Get("client_id"); cid != "" && cid != code.ClientID {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	clientName := code.ClientID
	if c, err := s.oauth.GetClient(code.ClientID); err == nil {
		clientName = c.Name
	}
	grant := &oauth.Grant{
		ClientID:   code.ClientID,
		ClientName: clientName,
		UserID:     code.UserID,
		Scopes:     code.Scopes,
		Projects:   code.Projects,
		ExpiresAt:  time.Now().Add(oauth.RefreshTTL),
	}
	s.issueTokens(w, grant)
}

func (s *Server) tokenFromRefresh(w http.ResponseWriter, r *http.Request) {
	grant, err := s.oauth.GrantByRefresh(r.Form.Get("refresh_token"))
	if err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "invalid or expired refresh token")
		return
	}
	// The owner is re-checked on refresh, so disabling an account stops its
	// agents at the next hour boundary rather than in 90 days.
	if u, err := s.users.Get(grant.UserID); err != nil || u.Disabled {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "account is no longer active")
		return
	}
	s.issueTokens(w, grant)
}

// issueTokens mints an access/refresh pair and persists the grant.
//
// The refresh token is **rotated** on every use (OAuth 2.1): the stored hash is
// replaced, so a refresh token that is replayed after use no longer resolves —
// which is what turns a stolen token into a detectable dead end rather than
// permanent access.
func (s *Server) issueTokens(w http.ResponseWriter, grant *oauth.Grant) {
	access, err := oauth.NewToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	refresh, err := oauth.NewToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	now := time.Now()
	grant.RefreshHash = oauth.HashToken(refresh)
	grant.LastUsedAt = &now
	if err := s.oauth.SaveGrant(grant); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save grant")
		return
	}
	if err := s.oauth.SaveAccess(&oauth.AccessToken{
		Hash:      oauth.HashToken(access),
		GrantID:   grant.ID,
		ExpiresAt: now.Add(oauth.AccessTTL),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save token")
		return
	}
	// Token responses must never be cached — they are credentials.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauth.AccessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         strings.Join(grant.Scopes, " "),
	})
}

// userForGrant resolves an OAuth access token to its owner. Like the API-key
// path, the owner is re-read every request so disabling an account takes every
// grant with it and needs no grant-side bookkeeping.
func (s *Server) userForGrant(token string) (*user.User, *oauth.Grant, bool) {
	if s.oauth == nil {
		return nil, nil, false
	}
	grant, err := s.oauth.GrantByAccess(token)
	if err != nil {
		return nil, nil, false
	}
	u, err := s.users.Get(grant.UserID)
	if err != nil || u.Disabled {
		return nil, nil, false
	}
	return u, grant, true
}

// oauthErr writes an RFC 6749 error body, which is what a client parses.
func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// --- connected apps (the user-facing grant list) ---

type grantDTO struct {
	ID         string   `json:"id"`
	ClientName string   `json:"clientName"`
	Scopes     []string `json:"scopes"`
	Projects   []string `json:"projects"`
	CreatedAt  string   `json:"createdAt"`
	LastUsedAt string   `json:"lastUsedAt,omitempty"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
}

// handleListGrants returns the caller's connected apps.
func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeJSON(w, http.StatusOK, []grantDTO{})
		return
	}
	u, _ := userFromContext(r)
	if u == nil {
		writeJSON(w, http.StatusOK, []grantDTO{})
		return
	}
	grants, err := s.oauth.ListGrants(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list grants: "+err.Error())
		return
	}
	slices.SortFunc(grants, func(a, b *oauth.Grant) int { return b.CreatedAt.Compare(a.CreatedAt) })
	out := make([]grantDTO, 0, len(grants))
	for _, g := range grants {
		d := grantDTO{
			ID: g.ID, ClientName: g.ClientName, Scopes: g.Scopes, Projects: g.Projects,
			CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
		}
		if d.Scopes == nil {
			d.Scopes = []string{}
		}
		if d.Projects == nil {
			d.Projects = []string{}
		}
		if g.LastUsedAt != nil {
			d.LastUsedAt = g.LastUsedAt.UTC().Format(time.RFC3339)
		}
		if !g.ExpiresAt.IsZero() {
			d.ExpiresAt = g.ExpiresAt.UTC().Format(time.RFC3339)
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevokeGrant revokes one of the caller's grants (or any, for an admin).
// Revoking drops the access tokens too, so it takes effect immediately rather
// than at the next expiry.
func (s *Server) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeErr(w, http.StatusServiceUnavailable, "OAuth is not enabled on this server")
		return
	}
	id := chi.URLParam(r, "gid")
	g, err := s.oauth.GetGrant(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "authorization not found")
		return
	}
	if u, _ := userFromContext(r); u != nil && !u.Admin && g.UserID != u.ID {
		writeErr(w, http.StatusNotFound, "authorization not found")
		return
	}
	if err := s.oauth.DeleteGrant(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "revoke: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
