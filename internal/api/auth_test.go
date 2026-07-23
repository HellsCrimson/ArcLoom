package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// newAuthTestServer builds a Server with in-memory stores and one seeded user
// "alice"/"hunter2". Returns the wired http.Handler.
func newAuthTestServer(t *testing.T) http.Handler {
	t.Helper()
	users := user.NewMemory()
	hash, err := auth.Hash("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Create(&user.User{
		ID: "u1", Username: "alice", PasswordHash: hash,
		Provider: auth.ProviderLocal, CreatedAt: time.Unix(0, 0),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AuthEnabled: true,
		CookieName:  "arcloom_session",
		SessionTTL:  time.Hour,
	}
	authn := auth.New(auth.NewLocalProvider(users))
	srv := New(cfg, nil, store.NewMemory(), nil, nil, nil, authn, session.NewMemory(), users, testSettings(t), nil, nil, nil)
	return srv.Handler()
}

// testSettings builds an ephemeral settings store (no master key).
func testSettings(t *testing.T) *settings.Store {
	t.Helper()
	set, err := settings.NewStore(t.TempDir()+"/settings.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func login(t *testing.T, h http.Handler, username, password string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d, body %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "arcloom_session" {
			return c
		}
	}
	t.Fatal("no session cookie set on login")
	return nil
}

func TestLogin_SetsHttpOnlyCookie(t *testing.T) {
	h := newAuthTestServer(t)
	c := login(t, h, "alice", "hunter2")
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Error("session cookie must be SameSite=Lax")
	}
	if c.Value == "" {
		t.Error("session cookie must carry a token")
	}
}

func TestLogin_BadCredentialsGeneric(t *testing.T) {
	h := newAuthTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"alice","password":"wrong"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// Same generic message whether or not the user exists (no enumeration).
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"ghost","password":"x"}`))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec.Body.String() != rec2.Body.String() {
		t.Errorf("credential errors must be identical: %q vs %q", rec.Body.String(), rec2.Body.String())
	}
}

func TestProtectedRoute_RequiresCookie(t *testing.T) {
	h := newAuthTestServer(t)

	// No cookie → 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: status = %d, want 401", rec.Code)
	}

	// Valid cookie → 200.
	c := login(t, h, "alice", "hunter2")
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid cookie: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestSSEGate proves auth runs before the stream starts: an unauthenticated
// request to the SSE plan route gets a JSON 401, not a text/event-stream body.
func TestSSEGate_401BeforeStream(t *testing.T) {
	h := newAuthTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/whatever/plan", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "event-stream") {
		t.Fatalf("must not start SSE stream before auth: content-type %q", ct)
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	h := newAuthTestServer(t)
	c := login(t, h, "alice", "hunter2")

	// Logout.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}

	// The old cookie no longer works.
	req2 := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req2.AddCookie(c)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: status = %d, want 401", rec2.Code)
	}
}

func TestMe_ReportsAuthState(t *testing.T) {
	h := newAuthTestServer(t)

	// Unauthenticated: 200 with null user but providers listed.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"user":null`) {
		t.Errorf("unauthenticated me should have null user: %s", body)
	}
	if !strings.Contains(body, `"local"`) {
		t.Errorf("me should list the local provider: %s", body)
	}

	// Authenticated: user present.
	c := login(t, h, "alice", "hunter2")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"alice"`) {
		t.Errorf("authenticated me should include username: %s", rec.Body.String())
	}
}

// fakeRedirect is a stand-in RedirectProvider so SSO handlers can be tested
// without a live identity provider.
type fakeRedirect struct {
	id  auth.Identity
	err error
}

func (f *fakeRedirect) Name() string                          { return auth.ProviderOIDC }
func (f *fakeRedirect) AuthURL(state, nonce string) string {
	return "https://idp.example/authorize?state=" + state + "&nonce=" + nonce
}
func (f *fakeRedirect) Exchange(_ context.Context, _, _ string) (auth.Identity, error) {
	return f.id, f.err
}

func newSSOTestServer(t *testing.T, fake *fakeRedirect) (http.Handler, user.Store, session.Store) {
	t.Helper()
	users := user.NewMemory()
	sessions := session.NewMemory()
	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", SessionTTL: time.Hour}
	authn := auth.New(auth.NewLocalProvider(users), fake)
	return New(cfg, nil, store.NewMemory(), nil, nil, nil, authn, sessions, users, testSettings(t), nil, nil, nil).Handler(), users, sessions
}

func TestSSOLogin_RedirectsAndSetsCookies(t *testing.T) {
	h, _, _ := newSSOTestServer(t, &fakeRedirect{id: auth.Identity{Subject: "sub-1", Provider: auth.ProviderOIDC}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/sso/oidc/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example/authorize") {
		t.Fatalf("unexpected redirect: %q", loc)
	}
	var gotState, gotNonce bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "arcloom_oidc_state" {
			gotState = true
		}
		if c.Name == "arcloom_oidc_nonce" {
			gotNonce = true
		}
	}
	if !gotState || !gotNonce {
		t.Fatal("login must set both state and nonce cookies")
	}
}

func TestSSOLogin_UnknownProvider404(t *testing.T) {
	h, _, _ := newSSOTestServer(t, &fakeRedirect{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/sso/saml/login", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSSOCallback_StateMismatchRedirectsWithError(t *testing.T) {
	h, _, _ := newSSOTestServer(t, &fakeRedirect{id: auth.Identity{Subject: "sub-1"}})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/oidc/callback?state=attacker&code=x", nil)
	req.AddCookie(&http.Cookie{Name: "arcloom_oidc_state", Value: "real"})
	req.AddCookie(&http.Cookie{Name: "arcloom_oidc_nonce", Value: "n"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "sso_error") {
		t.Fatalf("expected sso_error redirect, got %q", rec.Header().Get("Location"))
	}
}

func TestSSOCallback_HappyPathProvisionsAndSetsSession(t *testing.T) {
	fake := &fakeRedirect{id: auth.Identity{
		Subject: "sub-42", Provider: auth.ProviderOIDC, Email: "eve@example.com", DisplayName: "eve",
	}}
	h, users, _ := newSSOTestServer(t, fake)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/oidc/callback?state=s&code=good", nil)
	req.AddCookie(&http.Cookie{Name: "arcloom_oidc_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "arcloom_oidc_nonce", Value: "n"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("status=%d location=%q, want 302 /", rec.Code, rec.Header().Get("Location"))
	}
	var session bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "arcloom_session" && c.Value != "" {
			session = true
		}
	}
	if !session {
		t.Fatal("callback must set a session cookie")
	}
	// The user was provisioned and linked by subject.
	u, err := users.BySubject(auth.ProviderOIDC, "sub-42")
	if err != nil {
		t.Fatalf("expected a provisioned user: %v", err)
	}
	if u.Email != "eve@example.com" || u.PasswordHash != "" {
		t.Fatalf("unexpected provisioned user: %+v", u)
	}

	// Second login with the same subject must reuse (not duplicate) the user.
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/sso/oidc/callback?state=s2&code=good", nil)
	req2.AddCookie(&http.Cookie{Name: "arcloom_oidc_state", Value: "s2"})
	req2.AddCookie(&http.Cookie{Name: "arcloom_oidc_nonce", Value: "n"})
	h.ServeHTTP(httptest.NewRecorder(), req2)
	all, _ := users.List()
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 user after repeat SSO login, got %d", len(all))
	}
}

func TestLogin_CrossOriginRejected(t *testing.T) {
	h := newAuthTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"alice","password":"hunter2"}`))
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login: status = %d, want 403", rec.Code)
	}
}
