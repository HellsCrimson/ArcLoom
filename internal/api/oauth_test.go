package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/oauth"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// oauthTestServer builds a server with the authorization server mounted.
func oauthTestServer(t *testing.T) (*httptest.Server, user.Store, session.Store, oauth.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "one", Engine: model.EngineTofu}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	users := user.NewMemory()
	sessions := session.NewMemory()
	oa, err := oauth.NewDisk(t.TempDir())
	if err != nil {
		t.Fatalf("oauth store: %v", err)
	}
	cfg := config.Config{
		AuthEnabled: true, CookieName: "arcloom_session", DefaultEngine: "tofu",
		MCPEnabled: true, OAuthEnabled: true, SessionTTL: time.Hour,
	}
	srv := New(cfg, nil, st, nil, nil, nil, auth.New(auth.NewLocalProvider(users)),
		sessions, users, testSettings(t), nil, nil, nil, apikey.NewMemory(), oa)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, users, sessions, oa
}

// loginCookie mints a session for a user, standing in for the browser the
// consent screen runs in.
func loginCookie(t *testing.T, sessions session.Store, userID string) *http.Cookie {
	t.Helper()
	sess, err := sessions.Create(userID, "local", time.Hour)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return &http.Cookie{Name: "arcloom_session", Value: sess.Token}
}

func getJSON(t *testing.T, ts *httptest.Server, path string, cookie *http.Cookie, out any) int {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// TestOAuthDiscovery: the two metadata documents a client fetches, at the root.
func TestOAuthDiscovery(t *testing.T) {
	ts, _, _, _ := oauthTestServer(t)

	var prm map[string]any
	if code := getJSON(t, ts, "/.well-known/oauth-protected-resource", nil, &prm); code != 200 {
		t.Fatalf("protected-resource metadata: got %d", code)
	}
	if !strings.HasSuffix(prm["resource"].(string), "/api/mcp") {
		t.Errorf("resource = %v, want the MCP endpoint", prm["resource"])
	}
	if len(prm["authorization_servers"].([]any)) == 0 {
		t.Error("no authorization_servers advertised")
	}

	var asm map[string]any
	if code := getJSON(t, ts, "/.well-known/oauth-authorization-server", nil, &asm); code != 200 {
		t.Fatalf("auth-server metadata: got %d", code)
	}
	methods := asm["code_challenge_methods_supported"].([]any)
	if len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("PKCE methods = %v, want S256 only (plain is not protection)", methods)
	}
}

// TestOAuthUnauthenticatedMCPPointsAtMetadata: the 401 is what starts the flow.
func TestOAuthUnauthenticatedMCPPointsAtMetadata(t *testing.T) {
	ts, _, _, _ := oauthTestServer(t)
	resp, err := http.Post(ts.URL+"/api/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
	h := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(h, "resource_metadata=") {
		t.Errorf("401 does not point at the metadata: %q", h)
	}
}

// TestOAuthFullFlow runs registration → consent → code → token → an authenticated
// API call, then refresh, then revoke.
func TestOAuthFullFlow(t *testing.T) {
	ts, users, sessions, _ := oauthTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	cookie := loginCookie(t, sessions, "u1")

	// 1. Dynamic client registration (unauthenticated, by design).
	var reg map[string]any
	body := `{"client_name":"Test Agent","redirect_uris":["http://127.0.0.1:9876/callback"]}`
	resp, err := http.Post(ts.URL+"/api/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatalf("no client_id issued: %v", reg)
	}

	// 2. The consent screen's info call.
	verifier := "test-verifier-that-is-long-enough-to-be-valid-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1:9876/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
	}
	var info authorizeInfoDTO
	if code := getJSON(t, ts, "/api/oauth/authorize/info?"+q.Encode(), cookie, &info); code != 200 {
		t.Fatalf("authorize info: got %d", code)
	}
	if info.ClientName != "Test Agent" {
		t.Errorf("client name = %q", info.ClientName)
	}
	if !slicesContains(info.Scopes, "resources") || slicesContains(info.Scopes, "deploy") {
		t.Errorf("grantable scopes should mirror the user's own: %v", info.Scopes)
	}

	// 3. Approve, ticking one scope.
	approve, _ := json.Marshal(map[string]any{"query": "?" + q.Encode(), "scopes": []string{"resources"}})
	req, _ := http.NewRequest("POST", ts.URL+"/api/oauth/authorize", strings.NewReader(string(approve)))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	var appr map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&appr)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("approve: got %d (%v)", resp.StatusCode, appr)
	}
	redirect, err := url.Parse(appr["redirect"])
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	authCode := redirect.Query().Get("code")
	if authCode == "" {
		t.Fatalf("no code in redirect %q", appr["redirect"])
	}
	if redirect.Query().Get("state") != "xyz" {
		t.Error("state not echoed back")
	}

	// 4. Exchange the code. A wrong verifier must fail first.
	bad := url.Values{
		"grant_type": {"authorization_code"}, "code": {authCode},
		"redirect_uri":  {"http://127.0.0.1:9876/callback"},
		"code_verifier": {"wrong-verifier-wrong-verifier-wrong-verifier"},
	}
	resp, _ = http.PostForm(ts.URL+"/api/oauth/token", bad)
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("a wrong PKCE verifier was accepted")
	}
	// ...and the code is now consumed, so even the right verifier cannot reuse it.
	good := url.Values{
		"grant_type": {"authorization_code"}, "code": {authCode},
		"redirect_uri": {"http://127.0.0.1:9876/callback"}, "code_verifier": {verifier},
	}
	resp, _ = http.PostForm(ts.URL+"/api/oauth/token", good)
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("an authorization code was redeemable twice")
	}

	// Run the flow again cleanly to get a usable token.
	tok := freshToken(t, ts, cookie, clientID, verifier, challenge)

	// 5. The access token authenticates an API call, with the granted scope.
	req, _ = http.NewRequest("POST", ts.URL+"/api/projects/p1/resources",
		strings.NewReader(`{"type":"null_resource","name":"viaoauth"}`))
	req.Header.Set("Authorization", "Bearer "+tok["access_token"].(string))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OAuth-authenticated write: got %d", resp.StatusCode)
	}

	// ...but not a scope it was not granted.
	req, _ = http.NewRequest("GET", ts.URL+"/api/projects/p1/plan", nil)
	req.Header.Set("Authorization", "Bearer "+tok["access_token"].(string))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("ungranted deploy scope: got %d, want 403", resp.StatusCode)
	}

	// ...and never admin, whoever the owner is.
	req, _ = http.NewRequest("GET", ts.URL+"/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+tok["access_token"].(string))
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin via OAuth: got %d, want 403", resp.StatusCode)
	}

	// 6. Refresh rotates the refresh token: the old one stops working.
	oldRefresh := tok["refresh_token"].(string)
	resp, _ = http.PostForm(ts.URL+"/api/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh},
	})
	var refreshed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&refreshed)
	resp.Body.Close()
	if refreshed["access_token"] == nil {
		t.Fatalf("refresh failed: %v", refreshed)
	}
	if refreshed["refresh_token"] == oldRefresh {
		t.Error("refresh token was not rotated")
	}
	// Replaying oldRefresh is checked at the end of this test: it does not just
	// fail, it revokes the grant (two parties hold it), which would leave the
	// listing and revocation steps below with nothing to look at.

	// 7. The grant is listed, and revoking kills the live access token at once.
	var grants []grantDTO
	if code := getJSON(t, ts, "/api/oauth/grants", cookie, &grants); code != 200 || len(grants) == 0 {
		t.Fatalf("grants: code=%d n=%d", code, len(grants))
	}
	newAccess := refreshed["access_token"].(string)
	req, _ = http.NewRequest("DELETE", ts.URL+"/api/oauth/grants/"+grants[0].ID, nil)
	req.AddCookie(cookie)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	req, _ = http.NewRequest("GET", ts.URL+"/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+newAccess)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked grant still authenticated: got %d", resp.StatusCode)
	}
}

// freshToken runs authorize+exchange once more and returns the token response.
func freshToken(t *testing.T, ts *httptest.Server, cookie *http.Cookie, clientID, verifier, challenge string) map[string]any {
	t.Helper()
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri":   {"http://127.0.0.1:9876/callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	approve, _ := json.Marshal(map[string]any{"query": "?" + q.Encode(), "scopes": []string{"resources"}})
	req, _ := http.NewRequest("POST", ts.URL+"/api/oauth/authorize", strings.NewReader(string(approve)))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	var appr map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&appr)
	resp.Body.Close()
	redirect, _ := url.Parse(appr["redirect"])
	resp, err = http.PostForm(ts.URL+"/api/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")},
		"redirect_uri": {"http://127.0.0.1:9876/callback"}, "code_verifier": {verifier},
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	var tok map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if tok["access_token"] == nil {
		t.Fatalf("no access token: %v", tok)
	}
	return tok
}

// TestOAuthRefreshReplayRevokesGrant: rotation makes a stolen refresh token stop
// working, but says nothing about WHY it stopped, and whichever party redeems
// second is locked out — as likely the real client as the thief. Presenting an
// already-rotated token means two parties hold the grant, so the grant goes.
func TestOAuthRefreshReplayRevokesGrant(t *testing.T) {
	ts, users, sessions, _ := oauthTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	cookie := loginCookie(t, sessions, "u1")

	var reg map[string]any
	resp0, err := http.Post(ts.URL+"/api/oauth/register", "application/json",
		strings.NewReader(`{"client_name":"Replay Test","redirect_uris":["http://127.0.0.1:9876/callback"]}`))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_ = json.NewDecoder(resp0.Body).Decode(&reg)
	resp0.Body.Close()
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatalf("no client_id issued: %v", reg)
	}

	verifier := "verifier-for-replay-test-000000000000000000"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	tok := freshToken(t, ts, cookie, clientID, verifier, challenge)
	oldRefresh := tok["refresh_token"].(string)

	// Normal rotation.
	resp, _ := http.PostForm(ts.URL+"/api/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh},
	})
	var refreshed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&refreshed)
	resp.Body.Close()
	newAccess, _ := refreshed["access_token"].(string)
	if newAccess == "" {
		t.Fatalf("refresh failed: %v", refreshed)
	}

	// The replay must fail...
	resp, _ = http.PostForm(ts.URL+"/api/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {oldRefresh},
	})
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("a rotated-away refresh token still worked")
	}
	// ...and take the whole grant with it, including the access token the other
	// party is holding right now.
	req, _ := http.NewRequest("GET", ts.URL+"/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+newAccess)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("access token survived the replay: got %d, want 401", resp.StatusCode)
	}
	var grants []grantDTO
	if code := getJSON(t, ts, "/api/oauth/grants", cookie, &grants); code != 200 || len(grants) != 0 {
		t.Errorf("grant survived the replay: code=%d n=%d", code, len(grants))
	}
}

// TestOAuthRejectsBadRedirects: open registration means the redirect allowlist
// is the only thing keeping a code from being sent to a stranger.
func TestOAuthRejectsBadRedirects(t *testing.T) {
	ts, _, _, _ := oauthTestServer(t)
	for _, uri := range []string{
		"http://evil.example.com/callback", // remote http
		"https://evil.example.com/cb",      // remote https
		"javascript:alert(1)",
	} {
		body, _ := json.Marshal(map[string]any{"client_name": "x", "redirect_uris": []string{uri}})
		resp, err := http.Post(ts.URL+"/api/oauth/register", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			t.Errorf("registered a client with redirect %q", uri)
		}
	}
}

// TestOAuthConsentRequiresLogin: the whole model rests on a human approving in
// a session, so the consent endpoints must refuse an anonymous caller.
func TestOAuthConsentRequiresLogin(t *testing.T) {
	ts, _, _, _ := oauthTestServer(t)
	if code := getJSON(t, ts, "/api/oauth/authorize/info?response_type=code", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous consent info: got %d, want 401", code)
	}
	resp, err := http.Post(ts.URL+"/api/oauth/authorize", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous approve: got %d, want 401", resp.StatusCode)
	}
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
