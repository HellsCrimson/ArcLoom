package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
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
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Provider string `json:"provider,omitempty"`
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
		u, ok := s.currentUser(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// currentUser resolves the request's session cookie to a live, enabled user.
func (s *Server) currentUser(r *http.Request) (*user.User, bool) {
	c, err := r.Cookie(s.cfg.CookieName)
	if err != nil {
		return nil, false
	}
	sess, err := s.sessions.Get(c.Value)
	if err != nil {
		return nil, false
	}
	u, err := s.users.Get(sess.UserID)
	if err != nil || u.Disabled {
		return nil, false
	}
	return u, true
}

// userFromContext returns the user injected by requireAuth.
func userFromContext(r *http.Request) (*user.User, bool) {
	u, ok := r.Context().Value(userCtxKey{}).(*user.User)
	return u, ok
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	local, ok := s.auth.Local()
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
	sess, err := s.sessions.Create(id.Subject, id.Provider, s.cfg.SessionTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, s.sessionCookie(sess.Token, sess.ExpiresAt))
	writeJSON(w, http.StatusOK, meDTO{
		AuthEnabled:   true,
		AuthProviders: s.auth.Enabled(),
		User:          &userDTO{Username: id.DisplayName, Email: id.Email, Provider: id.Provider},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cfg.CookieName); err == nil {
		_ = s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, s.expiredCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := meDTO{AuthEnabled: s.cfg.AuthEnabled, AuthProviders: s.auth.Enabled()}
	if !s.cfg.AuthEnabled {
		// Auth off: the app is open; report a placeholder user so the gate opens.
		resp.User = &userDTO{Username: "local"}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if u, ok := s.currentUser(r); ok {
		resp.User = &userDTO{Username: u.Username, Email: u.Email, Provider: u.Provider}
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
	if !s.sameOrigin(r) {
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
	ok2, err := auth.Verify(u.PasswordHash, req.CurrentPassword)
	if err != nil || !ok2 {
		writeErr(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
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

// sameOrigin is a lightweight CSRF check for state-changing requests: if an
// Origin (or Referer) header is present, its host must match the request Host.
// Combined with SameSite=Lax and JSON-only bodies this blocks cross-site forgery
// without a separate CSRF token (documented assumption: SPA and API are
// same-origin).
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		// No Origin/Referer (e.g. curl, same-origin navigations): allow. Browsers
		// attach Origin to cross-site fetch/XHR, which is the case we block.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
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
	rp, ok := s.auth.Redirect(name)
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
	rp, ok := s.auth.Redirect(name)
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
func (s *Server) linkOrProvision(id auth.Identity) (*user.User, error) {
	if u, err := s.users.BySubject(id.Provider, id.Subject); err == nil {
		if u.Disabled {
			return nil, user.ErrNotFound
		}
		return u, nil
	}
	u := &user.User{
		ID:        uuid.NewString(),
		Username:  s.uniqueUsername(id),
		Email:     id.Email,
		Provider:  id.Provider,
		Subject:   id.Subject,
		CreatedAt: time.Now(),
	}
	if err := s.users.Create(u); err != nil {
		return nil, err
	}
	return u, nil
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
