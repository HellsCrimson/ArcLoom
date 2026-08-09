package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// newRBACServer seeds an admin, an editor (resources only) and a viewer (no
// perms), all with password "password1".
func newRBACServer(t *testing.T) (http.Handler, user.Store, session.Store) {
	t.Helper()
	users := user.NewMemory()
	hash, _ := auth.Hash("password1")
	mk := func(id, name string, admin bool, perms []string) {
		_ = users.Create(&user.User{ID: id, Username: name, PasswordHash: hash,
			Provider: auth.ProviderLocal, CreatedAt: time.Unix(0, 0), Admin: admin, Permissions: perms})
	}
	mk("admin", "admin", true, nil)
	mk("editor", "editor", false, []string{user.PermResources})
	mk("viewer", "viewer", false, nil)

	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", SessionTTL: time.Hour, DefaultEngine: "tofu"}
	sessions := session.NewMemory()
	authn := auth.New(auth.NewLocalProvider(users))
	srv := New(cfg, nil, store.NewMemory(), nil, nil, nil, authn, sessions, users, testSettings(t), nil, nil, nil, nil)
	// Run RBAC tests as an enterprise install so the community seat limit doesn't
	// block creating additional users.
	return srv.Handler(), users, sessions
}

func do(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRBAC_ReadAllowedWriteGated(t *testing.T) {
	h, _, _ := newRBACServer(t)
	viewer := login(t, h, "viewer", "password1")
	editor := login(t, h, "editor", "password1")

	// Any authenticated user can read.
	if rec := do(t, h, http.MethodGet, "/api/projects", viewer, ""); rec.Code != http.StatusOK {
		t.Fatalf("viewer read: %d, want 200", rec.Code)
	}
	// Viewer cannot create (no resources perm).
	if rec := do(t, h, http.MethodPost, "/api/projects", viewer, `{"name":"x","engine":"tofu"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create: %d, want 403", rec.Code)
	}
	// Editor passes the permission gate (reaches the handler; 400 here because no
	// runner is configured in the test — the point is it's not 403).
	if rec := do(t, h, http.MethodPost, "/api/projects", editor, `{"name":"x","engine":"tofu"}`); rec.Code == http.StatusForbidden {
		t.Fatalf("editor create must not be 403")
	}
	// Editor lacks deploy + git.
	if rec := do(t, h, http.MethodGet, "/api/projects/pid/plan", editor, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("editor plan: %d, want 403", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/projects/pid/git/commit", editor, `{"message":"m"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("editor git: %d, want 403", rec.Code)
	}
}

func TestRBAC_AdminOnlyEndpoints(t *testing.T) {
	h, _, _ := newRBACServer(t)
	viewer := login(t, h, "viewer", "password1")
	admin := login(t, h, "admin", "password1")

	if rec := do(t, h, http.MethodGet, "/api/admin/users", viewer, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer admin list: %d, want 403", rec.Code)
	}
	rec := do(t, h, http.MethodGet, "/api/admin/users", admin, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: %d, want 200", rec.Code)
	}
	var list []adminUserDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 3 {
		t.Fatalf("expected 3 users, got %d", len(list))
	}
	// Hashes never leak.
	if strings.Contains(rec.Body.String(), "argon2id") {
		t.Fatal("password hash leaked in admin list")
	}
}

func TestRBAC_CreateAndDisableRevokesSessions(t *testing.T) {
	h, _, _ := newRBACServer(t)
	admin := login(t, h, "admin", "password1")

	// Create a new editor.
	rec := do(t, h, http.MethodPost, "/api/admin/users", admin,
		`{"username":"newbie","password":"password1","permissions":["resources"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: %d (%s)", rec.Code, rec.Body.String())
	}
	var created adminUserDTO
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// The new user can log in and read.
	nb := login(t, h, "newbie", "password1")
	if rec := do(t, h, http.MethodGet, "/api/projects", nb, ""); rec.Code != http.StatusOK {
		t.Fatalf("newbie read: %d", rec.Code)
	}
	// Admin disables them → their session is revoked immediately.
	if rec := do(t, h, http.MethodPut, "/api/admin/users/"+created.ID, admin, `{"disabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodGet, "/api/projects", nb, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user session should be revoked: %d, want 401", rec.Code)
	}
}

func TestRBAC_CannotRemoveLastAdmin(t *testing.T) {
	h, _, _ := newRBACServer(t)
	admin := login(t, h, "admin", "password1")

	// Demote self (only admin) → 400.
	if rec := do(t, h, http.MethodPut, "/api/admin/users/admin", admin, `{"admin":false}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("demote last admin: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	// Delete self (only admin) → 400.
	if rec := do(t, h, http.MethodDelete, "/api/admin/users/admin", admin, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete last admin: %d, want 400", rec.Code)
	}
	// After promoting a second admin, demoting the first is allowed.
	if rec := do(t, h, http.MethodPut, "/api/admin/users/viewer", admin, `{"admin":true}`); rec.Code != http.StatusOK {
		t.Fatalf("promote viewer: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/admin/users/admin", admin, `{"admin":false}`); rec.Code != http.StatusOK {
		t.Fatalf("demote with another admin present: %d, want 200", rec.Code)
	}
}
