package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// newACLServer seeds an admin plus two non-admin users with resources+deploy,
// and one project "p1". Returned as an enterprise install so the rbac feature
// is unlocked.
func newACLServer(t *testing.T) *Server {
	t.Helper()
	users := user.NewMemory()
	hash, _ := auth.Hash("password1")
	mk := func(id, name string, admin bool, perms []string) {
		_ = users.Create(&user.User{ID: id, Username: name, PasswordHash: hash,
			Provider: auth.ProviderLocal, CreatedAt: time.Unix(0, 0), Admin: admin, Permissions: perms})
	}
	mk("admin", "admin", true, nil)
	mk("alice", "alice", false, []string{user.PermResources, user.PermDeploy})
	mk("bob", "bob", false, []string{user.PermResources, user.PermDeploy})

	st := store.NewMemory()
	_ = st.Create(&model.Project{ID: "p1", Name: "proj-one", Engine: model.EngineTofu})

	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", SessionTTL: time.Hour, DefaultEngine: "tofu"}
	srv := New(cfg, nil, st, nil, nil, nil, auth.New(auth.NewLocalProvider(users)), session.NewMemory(), users, testSettings(t), nil, nil, nil)
	srv.license.Install(&license.License{Tier: license.TierEnterprise})
	return srv
}

func TestACL_RestrictsAccess(t *testing.T) {
	srv := newACLServer(t)
	h := srv.Handler()
	admin := login(t, h, "admin", "password1")
	alice := login(t, h, "alice", "password1")
	bob := login(t, h, "bob", "password1")

	// No ACL yet: bob can read the project.
	if rec := do(t, h, http.MethodGet, "/api/projects/p1", bob, ""); rec.Code != http.StatusOK {
		t.Fatalf("bob pre-ACL read: %d, want 200", rec.Code)
	}

	// Admin restricts p1 to alice with resources only.
	if rec := do(t, h, http.MethodPut, "/api/projects/p1/acl", admin,
		`{"entries":[{"userId":"alice","perms":["resources"]}]}`); rec.Code != http.StatusOK {
		t.Fatalf("set acl: %d %s", rec.Code, rec.Body.String())
	}

	// Member alice can read.
	if rec := do(t, h, http.MethodGet, "/api/projects/p1", alice, ""); rec.Code != http.StatusOK {
		t.Fatalf("alice read: %d, want 200", rec.Code)
	}
	// Non-member bob is denied read and the project vanishes from his list.
	if rec := do(t, h, http.MethodGet, "/api/projects/p1", bob, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("bob read: %d, want 403", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/projects", bob, ""); strings.Contains(rec.Body.String(), "proj-one") {
		t.Fatalf("restricted project leaked into bob's list: %s", rec.Body.String())
	}
	// bob also can't mutate it.
	if rec := do(t, h, http.MethodPost, "/api/projects/p1/resources", bob, `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("bob upsert: %d, want 403", rec.Code)
	}
	// alice was granted resources but NOT deploy → plan is denied by the ACL even
	// though she holds the global deploy permission.
	if rec := do(t, h, http.MethodGet, "/api/projects/p1/plan", alice, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("alice deploy via ACL: %d, want 403", rec.Code)
	}
	// Admin always has access.
	if rec := do(t, h, http.MethodGet, "/api/projects/p1", admin, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin read: %d, want 200", rec.Code)
	}
}

func TestACL_IgnoredWithoutLicense(t *testing.T) {
	srv := newACLServer(t)
	// Restrict p1 to alice directly.
	if err := srv.settings.SetProjectACL("p1", []settings.ACLEntry{{UserID: "alice", Perms: []string{"resources"}}}); err != nil {
		t.Fatal(err)
	}
	// License without the rbac feature: ACL enforcement is disabled.
	srv.license.Install(&license.License{Tier: license.TierEnterprise, Features: []string{"sso"}})
	h := srv.Handler()
	bob := login(t, h, "bob", "password1")
	if rec := do(t, h, http.MethodGet, "/api/projects/p1", bob, ""); rec.Code != http.StatusOK {
		t.Fatalf("bob read unlicensed: %d, want 200 (ACL must be ignored)", rec.Code)
	}
}

func TestACL_SetRequiresAdminAndLicense(t *testing.T) {
	srv := newACLServer(t)
	h := srv.Handler()
	alice := login(t, h, "alice", "password1")
	// Non-admin cannot set an ACL.
	if rec := do(t, h, http.MethodPut, "/api/projects/p1/acl", alice,
		`{"entries":[{"userId":"bob","perms":["resources"]}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("alice set acl: %d, want 403", rec.Code)
	}

	// Admin, but the license lacks rbac → setting a non-empty ACL is refused,
	// while clearing (empty entries) is always allowed.
	srv.license.Install(&license.License{Tier: license.TierEnterprise, Features: []string{"sso"}})
	admin := login(t, h, "admin", "password1")
	if rec := do(t, h, http.MethodPut, "/api/projects/p1/acl", admin,
		`{"entries":[{"userId":"bob","perms":["resources"]}]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("admin set acl unlicensed: %d, want 403", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/projects/p1/acl", admin, `{"entries":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("admin clear acl unlicensed: %d, want 200", rec.Code)
	}
}
