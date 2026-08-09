package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// keyTestServer builds a server with auth on, one project, and a key store.
func keyTestServer(t *testing.T) (http.Handler, *apikey.Memory, user.Store, *store.Memory) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "one", Engine: model.EngineTofu}); err != nil {
		t.Fatalf("seed project p1: %v", err)
	}
	if err := st.Create(&model.Project{ID: "p2", Name: "two", Engine: model.EngineTofu}); err != nil {
		t.Fatalf("seed project p2: %v", err)
	}
	users := user.NewMemory()
	keys := apikey.NewMemory()
	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", DefaultEngine: "tofu"}
	srv := New(cfg, nil, st, nil, nil, nil, auth.New(auth.NewLocalProvider(users)),
		session.NewMemory(), users, testSettings(t), nil, nil, nil, keys)
	return srv.Handler(), keys, users, st
}

// mkUser creates an account with the given capabilities.
func mkUser(t *testing.T, users user.Store, id string, admin bool, perms ...string) *user.User {
	t.Helper()
	u := &user.User{ID: id, Username: id, Provider: "local", Admin: admin, Permissions: perms}
	if err := users.Create(u); err != nil {
		t.Fatalf("create user %s: %v", id, err)
	}
	return u
}

// mkKey mints a key and returns its bearer token.
func mkKey(t *testing.T, keys *apikey.Memory, userID string, scopes []string, projects []string) string {
	t.Helper()
	token, err := keys.Create(&apikey.Key{UserID: userID, Label: "t", Scopes: scopes, Projects: projects})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	return token
}

// do issues a request bearing a token.
func doKey(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestKeyScopeIntersection is the core of the "a key restricts, never
// escalates" rule: effective access is user ∩ key, so neither half alone is
// enough.
func TestKeyScopeIntersection(t *testing.T) {
	h, keys, users, _ := keyTestServer(t)
	mkUser(t, users, "u-rw", false, user.PermResources) // no deploy
	mkUser(t, users, "u-all", false, user.PermResources, user.PermDeploy)

	cases := []struct {
		name       string
		userID     string
		scopes     []string
		wantWrite  int // POST /projects/{id}/resources
		wantDeploy int // GET /projects/{id}/plan
	}{
		{
			name:   "read-only key (no scopes) cannot write or deploy",
			userID: "u-all", scopes: nil,
			wantWrite: http.StatusForbidden, wantDeploy: http.StatusForbidden,
		},
		{
			name:   "resources-scoped key can write but not deploy",
			userID: "u-all", scopes: []string{user.PermResources},
			wantWrite: http.StatusOK, wantDeploy: http.StatusForbidden,
		},
		{
			name: "key cannot grant what the owner lacks",
			// The scope says deploy, but the owner has no deploy permission —
			// the user check runs first and refuses.
			userID: "u-rw", scopes: []string{user.PermResources, user.PermDeploy},
			wantWrite: http.StatusOK, wantDeploy: http.StatusForbidden,
		},
		{
			name:   "fully scoped key of a fully permitted user passes both gates",
			userID: "u-all", scopes: []string{user.PermResources, user.PermDeploy},
			wantWrite: http.StatusOK, wantDeploy: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := mkKey(t, keys, tc.userID, tc.scopes, nil)
			w := doKey(h, "POST", "/api/projects/p1/resources", token,
				`{"type":"null_resource","name":"a"}`)
			if w.Code != tc.wantWrite {
				t.Errorf("write: got %d, want %d (%s)", w.Code, tc.wantWrite, w.Body.String())
			}
			// The deploy gate is what we are asserting; a permitted request then
			// fails later for want of an engine, which is not a 403.
			w = doKey(h, "GET", "/api/projects/p1/plan", token, "")
			if tc.wantDeploy == http.StatusForbidden {
				if w.Code != http.StatusForbidden {
					t.Errorf("deploy: got %d, want 403 (%s)", w.Code, w.Body.String())
				}
			} else if w.Code == http.StatusForbidden {
				t.Errorf("deploy: unexpectedly forbidden (%s)", w.Body.String())
			}
		})
	}
}

// TestKeyProjectRestriction covers the second dimension: a key pinned to one
// project must be refused on another, on reads as well as writes, and the
// project listing must agree with the gates.
func TestKeyProjectRestriction(t *testing.T) {
	h, keys, users, _ := keyTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, []string{"p1"})

	if w := doKey(h, "GET", "/api/projects/p1", token, ""); w.Code != http.StatusOK {
		t.Errorf("read in-scope project: got %d, want 200", w.Code)
	}
	if w := doKey(h, "GET", "/api/projects/p2", token, ""); w.Code != http.StatusForbidden {
		t.Errorf("read out-of-scope project: got %d, want 403", w.Code)
	}
	w := doKey(h, "POST", "/api/projects/p2/resources", token, `{"type":"null_resource","name":"a"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("write out-of-scope project: got %d, want 403", w.Code)
	}

	// The listing must not advertise a project every follow-up call would 403 on.
	w = doKey(h, "GET", "/api/projects", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: got %d", w.Code)
	}
	var list []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0]["id"] != "p1" {
		t.Errorf("list = %v, want only p1", list)
	}
}

// TestKeyNeverReachesAdmin is the escalation boundary: admin is not grantable,
// so even an admin's own key cannot reach /admin/*.
func TestKeyNeverReachesAdmin(t *testing.T) {
	h, keys, users, _ := keyTestServer(t)
	mkUser(t, users, "root", true)
	token := mkKey(t, keys, "root", []string{user.PermResources, user.PermDeploy}, nil)

	for _, path := range []string{"/api/admin/users", "/api/admin/audit", "/api/admin/instance"} {
		if w := doKey(h, "GET", path, token, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s via key: got %d, want 403", path, w.Code)
		}
	}
	// ...while the same admin's *session* still reaches them (asserted
	// elsewhere); here we only need to know the key is the thing being refused.
}

// TestBearerRejections covers the credential itself.
func TestBearerRejections(t *testing.T) {
	h, keys, users, _ := keyTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	good := mkKey(t, keys, "u1", []string{user.PermResources}, nil)

	for name, token := range map[string]string{
		"garbage":      "not-a-token",
		"wrong secret": "arcl_" + strings.SplitN(good, "_", 3)[1] + "_bogus",
		"unknown id":   "arcl_aaaaaaaaaaaaaaaaaaaaaaaa_bogus",
	} {
		if w := doKey(h, "GET", "/api/projects", token, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", name, w.Code)
		}
	}

	// A revoked key stops working immediately.
	id := strings.SplitN(good, "_", 3)[1]
	if err := keys.Delete(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if w := doKey(h, "GET", "/api/projects", good, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("revoked key: got %d, want 401", w.Code)
	}
}

// TestDisabledOwnerDisablesKey: the key can never outlive its owner's access.
func TestDisabledOwnerDisablesKey(t *testing.T) {
	h, keys, users, _ := keyTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)

	if w := doKey(h, "GET", "/api/projects", token, ""); w.Code != http.StatusOK {
		t.Fatalf("live key: got %d", w.Code)
	}
	if err := users.Update("u1", func(u *user.User) error { u.Disabled = true; return nil }); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if w := doKey(h, "GET", "/api/projects", token, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("key of disabled user: got %d, want 401", w.Code)
	}
}

// TestCreateKeyCannotExceedOwner: the 400 at creation is what stops a key that
// looks like it grants deploy but 403s on every call.
func TestCreateKeyCannotExceedOwner(t *testing.T) {
	h, keys, users, _ := keyTestServer(t)
	mkUser(t, users, "u1", false, user.PermResources)
	token := mkKey(t, keys, "u1", []string{user.PermResources}, nil)

	w := doKey(h, "POST", "/api/keys", token, `{"label":"x","scopes":["deploy"]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("granting an unheld scope: got %d, want 400 (%s)", w.Code, w.Body.String())
	}
	w = doKey(h, "POST", "/api/keys", token, `{"label":"x","scopes":["bogus"]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown scope: got %d, want 400", w.Code)
	}
	w = doKey(h, "POST", "/api/keys", token, `{"label":"","scopes":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing label: got %d, want 400", w.Code)
	}

	// A valid creation returns the token exactly once.
	w = doKey(h, "POST", "/api/keys", token, `{"label":"ci","scopes":["resources"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: got %d (%s)", w.Code, w.Body.String())
	}
	var resp createAPIKeyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("no token returned")
	}
	// ...and never again.
	w = doKey(h, "GET", "/api/keys", token, "")
	if strings.Contains(w.Body.String(), resp.Token) {
		t.Error("listing leaked the token")
	}
	_ = keys
}

// TestEffectiveUser is the reporting view the MCP tool filter also uses; it must
// agree with what the middleware enforces.
func TestEffectiveUser(t *testing.T) {
	admin := &user.User{ID: "a", Admin: true}
	key := &apikey.Key{Scopes: []string{user.PermResources, user.PermDeploy}}
	eff := effectiveUser(admin, key)
	if eff.Admin {
		t.Error("effectiveUser must clear Admin for a key")
	}
	if !eff.Can(user.PermResources) || !eff.Can(user.PermDeploy) {
		t.Error("an admin's key should keep its scoped permissions")
	}
	if eff.Can(user.PermGit) {
		t.Error("unscoped permission leaked through")
	}

	limited := &user.User{ID: "b", Permissions: []string{user.PermResources}}
	eff = effectiveUser(limited, key)
	if eff.Can(user.PermDeploy) {
		t.Error("key scope must not grant what the user lacks")
	}
	if !eff.Can(user.PermResources) {
		t.Error("intersection dropped a held permission")
	}

	// No key: unchanged.
	if got := effectiveUser(limited, nil); got != limited {
		t.Error("a cookie session must pass through unchanged")
	}
}
