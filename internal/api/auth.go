package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// OIDC transient cookies carry the CSRF state and replay nonce across the
// redirect to the identity provider and back.
const (
	oidcStateCookie = "arcloom_oidc_state"
	oidcNonceCookie = "arcloom_oidc_nonce"
)

// userCtxKey keys the authenticated *user.User in the request context.
type userCtxKey struct{}

// meDTO is the /auth/me payload. It reports auth state plus the enabled
// providers so the login screen knows which options to show (SSO button lands
// in slice 2 with no further backend change).
type meDTO struct {
	AuthEnabled   bool     `json:"authEnabled"`
	AuthProviders []string `json:"authProviders"`
	User          *userDTO `json:"user"`
}

type userDTO struct {
	Username    string   `json:"username"`
	Email       string   `json:"email,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Admin       bool     `json:"admin"`
	Permissions []string `json:"permissions"`
}

// meUserDTO builds the current-user payload the SPA uses to gate its UI.
func meUserDTO(u *user.User) *userDTO {
	perms := u.Permissions
	if perms == nil {
		perms = []string{}
	}
	return &userDTO{
		Username:    u.Username,
		Email:       u.Email,
		Provider:    u.Provider,
		Admin:       u.Admin,
		Permissions: perms,
	}
}

// requireAuth gates protected routes. It validates the session cookie, loads the
// user, and injects it into the request context, else replies 401. When auth is
// disabled it is a pass-through.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}
		u, cred, ok := s.currentUser(r)
		if !ok {
			s.unauthorized(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, u)
		if cred != nil {
			ctx = context.WithValue(ctx, credCtxKey{}, cred)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// currentUser resolves the request's credential to a live, enabled user. The
// returned *credential says which kind authenticated it: an `Authorization:
// Bearer` API key or OAuth access token (non-nil), or the session cookie (nil).
//
// Bearer is tried first and is *terminal*: a request that presents a malformed
// or revoked token is rejected outright rather than silently falling back to any
// cookie that happens to ride along, so a stale credential can never be masked
// by a browser session and appear to work.
//
// Within bearer, an API key is tried before an OAuth token purely because its
// `arcl_` prefix makes it cheap to recognise; neither can be mistaken for the
// other, so the order carries no meaning beyond that.
func (s *Server) currentUser(r *http.Request) (*user.User, *credential, bool) {
	if token, ok := bearerToken(r); ok {
		if u, key, ok := s.userForKey(token); ok {
			return u, credFromKey(key), true
		}
		if u, grant, ok := s.userForGrant(token); ok {
			return u, credFromGrant(grant), true
		}
		return nil, nil, false
	}
	c, err := r.Cookie(s.cfg.CookieName)
	if err != nil {
		return nil, nil, false
	}
	sess, err := s.sessions.Get(c.Value)
	if err != nil {
		return nil, nil, false
	}
	u, err := s.users.Get(sess.UserID)
	if err != nil || u.Disabled {
		return nil, nil, false
	}
	return u, nil, true
}

// unauthorized writes the 401. For the MCP endpoint it also carries the
// WWW-Authenticate header pointing at the protected-resource metadata, which is
// the entire trigger for the OAuth flow: a client with no credential gets told
// where to go and authorizes itself, instead of just failing.
func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request) {
	if s.oauthEnabled() && strings.HasPrefix(r.URL.Path, "/api/mcp") {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+s.issuer(r)+`/.well-known/oauth-protected-resource"`)
	}
	writeErr(w, http.StatusUnauthorized, "authentication required")
}

// userFromContext returns the user injected by requireAuth.
func userFromContext(r *http.Request) (*user.User, bool) {
	u, ok := r.Context().Value(userCtxKey{}).(*user.User)
	return u, ok
}

// requirePerm gates a route on a capability. It runs after requireAuth (so the
// user is in context). With auth disabled the context has no user and the app is
// open, so it passes through.
func (s *Server) requirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.cfg.AuthEnabled {
				next.ServeHTTP(w, r)
				return
			}
			u, ok := userFromContext(r)
			if !ok {
				writeErr(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !u.Can(perm) {
				writeErr(w, http.StatusForbidden, "you do not have permission to do that")
				return
			}
			if msg := credDenies(r, perm, ""); msg != "" {
				writeErr(w, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r.WithContext(r.Context()))
		})
	}
}

// requireAdmin gates a route on the admin flag.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}
		u, ok := userFromContext(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		// An API key never carries admin, whoever owns it. This is the boundary
		// that keeps a leaked key away from user management, the SSO config, the
		// secret store settings and master-key rotation — the actions that could
		// be used to escalate out of the key's own scopes.
		if _, viaCred := credentialFromContext(r); viaCred {
			writeErr(w, http.StatusForbidden, "api keys cannot be used for admin actions")
			return
		}
		if !u.Admin {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireBrowserSession refuses any request authenticated by a non-browser
// credential (an API key or an OAuth grant), whatever its scopes.
//
// It guards the routes that MINT and REVOKE credentials. Every other gate in
// this package enforces "a credential restricts, never escalates" by
// intersecting scopes with the owner's permissions — but credential *creation*
// is the one action where that rule cannot be expressed as a subtraction: the
// owner's permissions are the input, so a key with no scopes at all could ask
// for a key carrying everything its owner holds, over every project, and the
// intersection would never be consulted. A narrow credential could therefore
// manufacture a wide one and escape its own restriction entirely.
//
// The refusal is absolute rather than an intersection because managing
// credentials is a deliberate human action: the person doing it is signed into
// the UI, which is also where the resulting token is shown exactly once. This
// mirrors requireAdmin's refusal for the same underlying reason — a leaked
// credential must not be able to widen itself.
func (s *Server) requireBrowserSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}
		if _, viaCred := credentialFromContext(r); viaCred {
			writeErr(w, http.StatusForbidden,
				"credentials cannot manage credentials — sign in to ArcLoom to create or revoke one")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.csrfSafe(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Throttle before the expensive argon2 verify: gate per source IP and per
	// username so neither a brute-force nor a burst of hashes can run unbounded.
	ipKey := "login-ip:" + clientIP(r)
	userKey := "login-user:" + req.Username
	if ok, retry := s.authLimiter.allow(ipKey); !ok {
		tooManyAttempts(w, retry)
		return
	}
	if req.Username != "" {
		if ok, retry := s.authLimiter.allow(userKey); !ok {
			tooManyAttempts(w, retry)
			return
		}
	}
	local, ok := s.authn().Local()
	if !ok {
		writeErr(w, http.StatusBadRequest, "password login is disabled")
		return
	}
	id, err := local.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		// Generic message: never reveal whether the username exists.
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	// Success: refund both buckets so legitimate logins never accumulate toward
	// a lockout (only failing attempts deplete).
	s.authLimiter.reset(ipKey)
	s.authLimiter.reset(userKey)
	sess, err := s.sessions.Create(id.Subject, id.Provider, s.cfg.SessionTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, s.sessionCookie(sess.Token, sess.ExpiresAt))
	u, _ := s.users.Get(id.Subject)
	resp := meDTO{AuthEnabled: true, AuthProviders: s.authn().Enabled()}
	if u != nil {
		resp.User = meUserDTO(u)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cfg.CookieName); err == nil {
		_ = s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, s.expiredCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := meDTO{AuthEnabled: s.cfg.AuthEnabled, AuthProviders: s.authn().Enabled()}
	if !s.cfg.AuthEnabled {
		// Auth off: the app is open; report a full-access placeholder user so the
		// gate opens and every action is available.
		resp.User = &userDTO{Username: "local", Admin: true, Permissions: []string{}}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if u, cred, ok := s.currentUser(r); ok {
		// For a key-authenticated request report what the *credential* can do,
		// not what its owner could do in a browser: the intersection of the
		// user's permissions with the key's scopes, and never admin. That makes
		// /auth/me a usable "what is this key allowed to do" probe for a
		// non-browser client, and stops the SPA-facing DTO from overstating a
		// restricted credential.
		resp.User = meUserDTO(effectiveUser(u, cred))
	}
	writeJSON(w, http.StatusOK, resp)
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// handlePasswordChange lets the logged-in user rotate their own password. It
// revokes the user's other sessions so a leaked old session cannot linger.
func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	if !s.csrfSafe(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	u, ok := userFromContext(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req passwordChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.NewPassword) < 8 {
		writeErr(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if u.PasswordHash == "" {
		writeErr(w, http.StatusBadRequest, "account has no password to change")
		return
	}
	// Throttle the argon2 verify by user (an authenticated attacker could
	// otherwise hammer it); refunded on success below.
	pwKey := "pw:" + u.ID
	if ok, retry := s.authLimiter.allow(pwKey); !ok {
		tooManyAttempts(w, retry)
		return
	}
	ok2, err := auth.Verify(u.PasswordHash, req.CurrentPassword)
	if err != nil || !ok2 {
		writeErr(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	s.authLimiter.reset(pwKey)
	newHash, err := auth.Hash(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := s.users.Update(u.ID, func(uu *user.User) error {
		uu.PasswordHash = newHash
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not update password")
		return
	}
	// Revoke every session (including this one); the client must log in again.
	_ = s.sessions.DeleteByUser(u.ID)
	http.SetCookie(w, s.expiredCookie())
	w.WriteHeader(http.StatusNoContent)
}

// sessionCookie builds the login cookie. HttpOnly blocks JS access; SameSite=Lax
// is the primary CSRF defense while still allowing top-level SSO redirects back.
func (s *Server) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	}
}

func (s *Server) expiredCookie() *http.Cookie {
	return &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// csrfSafe reports whether a state-changing (or side-effectful) request is safe
// from cross-site forgery. It prefers the browser-controlled Sec-Fetch-Site
// header, which a cross-site page can neither forge nor suppress: `same-origin`
// (the SPA's own fetch/EventSource) and `none` (a direct user navigation, i.e.
// the user themselves) pass; `same-site` and `cross-site` are rejected. This is
// what closes the GET-SSE hole — SameSite=Lax still sends the cookie on a
// cross-site top-level GET navigation, and an attacker can strip Origin/Referer
// via Referrer-Policy, so the Origin check alone is not enough.
//
// When Fetch Metadata is absent (older browsers, curl, tests) it falls back to
// matching the Origin/Referer host against the request Host, allowing requests
// that carry neither (non-browser clients).
func (s *Server) csrfSafe(r *http.Request) bool {
	// A request authenticated by an API key carries no ambient authority: the
	// credential is a header the caller had to set deliberately, not a cookie
	// the browser attaches on its own, so there is nothing for a cross-site page
	// to forge. (requireAuth runs on the parent group, so the key is already in
	// context by the time csrfGuard calls this.) Without this, every non-browser
	// client — CI, scripts, MCP — would be rejected by the Origin fallback the
	// moment it sent an Origin header.
	if _, viaCred := credentialFromContext(r); viaCred {
		return true
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "same-site", "cross-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// csrfGuard rejects cross-site requests. It is mounted on the mutating groups
// (resources / git / admin) and on the deploy group, whose GET SSE plan /
// validate / lint routes have server-side side effects a bare cross-site
// navigation could otherwise trigger.
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.csrfSafe(r) {
			writeErr(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- OIDC SSO (browser redirect flow) ---
//
// handleSSOLogin and handleSSOCallback are registered in the PUBLIC route group
// (they must be reachable while logged out). They are additive: the session,
// cookie, and middleware are identical to local login — an SSO login just mints
// the same session.Store token via a different Provider.

// handleSSOLogin starts the OIDC flow: it mints a CSRF state + replay nonce,
// stashes them in short-lived cookies, and redirects to the IdP.
func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	rp, ok := s.authn().Redirect(name)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown SSO provider")
		return
	}
	state := randToken()
	nonce := randToken()
	http.SetCookie(w, s.transientCookie(oidcStateCookie, state))
	http.SetCookie(w, s.transientCookie(oidcNonceCookie, nonce))
	http.Redirect(w, r, rp.AuthURL(state, nonce), http.StatusFound)
}

// handleSSOCallback completes the OIDC flow: it validates state against the
// cookie, exchanges the code (which verifies the ID token + nonce), links or
// provisions the user, mints a session, and redirects to the app.
func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "provider")
	rp, ok := s.authn().Redirect(name)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown SSO provider")
		return
	}
	// Clear the transient cookies regardless of outcome.
	defer func() {
		http.SetCookie(w, s.expiredNamedCookie(oidcStateCookie))
		http.SetCookie(w, s.expiredNamedCookie(oidcNonceCookie))
	}()

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		s.ssoFail(w, r, "identity provider returned an error")
		return
	}
	stateCookie, err := r.Cookie(oidcStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		s.ssoFail(w, r, "invalid SSO state")
		return
	}
	nonceCookie, err := r.Cookie(oidcNonceCookie)
	if err != nil || nonceCookie.Value == "" {
		s.ssoFail(w, r, "missing SSO nonce")
		return
	}
	id, err := rp.Exchange(r.Context(), r.URL.Query().Get("code"), nonceCookie.Value)
	if err != nil {
		s.ssoFail(w, r, "SSO authentication failed")
		return
	}
	u, err := s.linkOrProvision(id)
	if err != nil {
		s.ssoFail(w, r, "could not provision user")
		return
	}
	sess, err := s.sessions.Create(u.ID, auth.ProviderOIDC, s.cfg.SessionTTL)
	if err != nil {
		s.ssoFail(w, r, "could not create session")
		return
	}
	http.SetCookie(w, s.sessionCookie(sess.Token, sess.ExpiresAt))
	http.Redirect(w, r, "/", http.StatusFound)
}

// linkOrProvision finds the user previously linked to this SSO subject, or
// creates one on first login. Disabled accounts are rejected.
//
// A provisioned user is read-only by default; the SSO config's group→role
// mapping is what can make them an admin (or grant permissions) from the start,
// so an IdP group is enough to onboard someone. An existing user is left alone
// unless SyncGroupsOnLogin is on, in which case the IdP is the source of truth
// and the mapping is re-applied on every login.
func (s *Server) linkOrProvision(id auth.Identity) (*user.User, error) {
	sso := s.settings.SSO()
	if u, err := s.users.BySubject(id.Provider, id.Subject); err == nil {
		if u.Disabled {
			return nil, user.ErrNotFound
		}
		if !sso.SyncGroupsOnLogin {
			return u, nil
		}
		admin, perms := rolesForGroups(id.Groups, sso.GroupRoles)
		if u.Admin == admin && slices.Equal(u.Permissions, perms) {
			return u, nil
		}
		if err := s.users.Update(u.ID, func(stored *user.User) error {
			stored.Admin = admin
			stored.Permissions = perms
			return nil
		}); err != nil {
			return nil, err
		}
		u, err = s.users.Get(u.ID)
		if err != nil {
			return nil, err
		}
		s.recordAuditAs(u.Username, "user.sso.sync", ssoAuditDetail(u, id, admin, perms))
		return u, nil
	}
	admin, perms := rolesForGroups(id.Groups, sso.GroupRoles)
	u := &user.User{
		ID:          uuid.NewString(),
		Username:    s.uniqueUsername(id),
		Email:       id.Email,
		Provider:    id.Provider,
		Subject:     id.Subject,
		CreatedAt:   time.Now(),
		Admin:       admin,
		Permissions: perms,
	}
	if err := s.users.Create(u); err != nil {
		return nil, err
	}
	// Account creation is an administrative event even though nobody clicked it,
	// and it is not covered by auditMiddleware (the SSO callback is in the public
	// route group, which the middleware does not wrap).
	s.recordAuditAs(u.Username, "user.sso.provision", ssoAuditDetail(u, id, admin, perms))
	return u, nil
}

// rolesForGroups resolves an identity's IdP groups against the configured
// mapping, unioning every matching row: admin wins outright (it implies all
// permissions), otherwise the granted permissions accumulate. No match leaves
// the user read-only, which is the pre-existing default.
func rolesForGroups(groups []string, roles []settings.GroupRole) (admin bool, perms []string) {
	for _, gr := range roles {
		if !matchesGroup(groups, gr.Group) {
			continue
		}
		if gr.Admin {
			admin = true
		}
		for _, p := range gr.Perms {
			if !slices.Contains(perms, p) {
				perms = append(perms, p)
			}
		}
	}
	if admin {
		return true, nil // Admin implies everything; storing perms too would be noise
	}
	return false, perms
}

// matchesGroup compares an IdP group against a configured name, case-insensitively
// and ignoring a leading "/" — Keycloak reports groups as paths ("/platform-admins")
// while admins naturally type the bare name.
func matchesGroup(groups []string, want string) bool {
	want = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(want), "/"))
	if want == "" {
		return false
	}
	for _, g := range groups {
		if strings.ToLower(strings.TrimPrefix(strings.TrimSpace(g), "/")) == want {
			return true
		}
	}
	return false
}

// ssoAuditDetail describes what the IdP produced, without inventing a subject
// value that could identify more than the username already does.
func ssoAuditDetail(u *user.User, id auth.Identity, admin bool, perms []string) string {
	role := "read-only"
	switch {
	case admin:
		role = "admin"
	case len(perms) > 0:
		role = strings.Join(perms, "+")
	}
	detail := fmt.Sprintf("%s via %s → %s", u.Username, id.Provider, role)
	if len(id.Groups) > 0 {
		detail += " (groups: " + strings.Join(id.Groups, ", ") + ")"
	}
	return detail
}

// uniqueUsername derives a login handle from the identity, appending a short
// suffix if the preferred handle is already taken by another account.
func (s *Server) uniqueUsername(id auth.Identity) string {
	base := id.DisplayName
	if base == "" {
		base = id.Subject
	}
	if _, err := s.users.ByUsername(base); err != nil {
		return base // free
	}
	return base + "-" + id.Subject[:min(6, len(id.Subject))]
}

// ssoFail redirects back to the app with an error query so the login screen can
// show a message (the SPA reads ?sso_error=...).
func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(msg), http.StatusFound)
}

// transientCookie builds a short-lived (10 min) cookie for the OIDC round-trip.
func (s *Server) transientCookie(name, value string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	}
}

func (s *Server) expiredNamedCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// randToken returns a URL-safe random token for OIDC state/nonce.
func randToken() string {
	b := make([]byte, 24)
	_, _ = io.ReadFull(rand.Reader, b)
	return base64.RawURLEncoding.EncodeToString(b)
}
