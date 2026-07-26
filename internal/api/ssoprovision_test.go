package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/audit"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// ssoGroupServer wires an SSO test server with a real audit store and a stored
// group→role mapping, so provisioning can be observed end to end.
func ssoGroupServer(t *testing.T, fake *fakeRedirect, sso settings.SSO) (http.Handler, user.Store, audit.Store) {
	t.Helper()
	users := user.NewMemory()
	set := testSettings(t)
	sso.Enabled = true
	if err := set.SetSSO(sso); err != nil {
		t.Fatal(err)
	}
	auditLog, err := audit.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", SessionTTL: time.Hour}
	authn := auth.New(auth.NewLocalProvider(users), fake)
	h := New(cfg, nil, store.NewMemory(), nil, nil, nil, authn, session.NewMemory(), users, set, nil, auditLog, nil).Handler()
	return h, users, auditLog
}

// ssoCallback drives one full SSO callback with matching state/nonce cookies.
func ssoCallback(t *testing.T, h http.Handler, state string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/oidc/callback?state="+state+"&code=good", nil)
	req.AddCookie(&http.Cookie{Name: "arcloom_oidc_state", Value: state})
	req.AddCookie(&http.Cookie{Name: "arcloom_oidc_nonce", Value: "n"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSSOProvisionsAdminFromGroup(t *testing.T) {
	fake := &fakeRedirect{id: auth.Identity{
		Subject: "sub-admin", Provider: auth.ProviderOIDC, DisplayName: "ada",
		Groups: []string{"/platform-admins", "everyone"},
	}}
	h, users, _ := ssoGroupServer(t, fake, settings.SSO{
		GroupRoles: []settings.GroupRole{{Group: "platform-admins", Admin: true}},
	})
	ssoCallback(t, h, "s1")

	u, err := users.BySubject(auth.ProviderOIDC, "sub-admin")
	if err != nil {
		t.Fatalf("expected a provisioned user: %v", err)
	}
	if !u.Admin {
		t.Fatalf("group mapping should have made this user an admin: %+v", u)
	}
}

func TestSSOProvisionsPermsFromGroup(t *testing.T) {
	fake := &fakeRedirect{id: auth.Identity{
		Subject: "sub-dev", Provider: auth.ProviderOIDC, DisplayName: "dev",
		Groups: []string{"developers"},
	}}
	h, users, _ := ssoGroupServer(t, fake, settings.SSO{
		GroupRoles: []settings.GroupRole{
			{Group: "platform-admins", Admin: true},
			{Group: "developers", Perms: []string{user.PermResources, user.PermDeploy}},
		},
	})
	ssoCallback(t, h, "s1")

	u, _ := users.BySubject(auth.ProviderOIDC, "sub-dev")
	if u.Admin {
		t.Fatalf("developers must not be admins: %+v", u)
	}
	if !u.Can(user.PermResources) || !u.Can(user.PermDeploy) || u.Can(user.PermGit) {
		t.Fatalf("unexpected permissions: %v", u.Permissions)
	}
}

func TestSSOUnmappedUserStaysReadOnly(t *testing.T) {
	fake := &fakeRedirect{id: auth.Identity{
		Subject: "sub-guest", Provider: auth.ProviderOIDC, DisplayName: "guest",
		Groups: []string{"interns"},
	}}
	h, users, _ := ssoGroupServer(t, fake, settings.SSO{
		GroupRoles: []settings.GroupRole{{Group: "platform-admins", Admin: true}},
	})
	ssoCallback(t, h, "s1")

	u, _ := users.BySubject(auth.ProviderOIDC, "sub-guest")
	if u.Admin || len(u.Permissions) > 0 {
		t.Fatalf("an unmapped SSO user must stay read-only: %+v", u)
	}
}

func TestSSOProvisionIsAudited(t *testing.T) {
	fake := &fakeRedirect{id: auth.Identity{
		Subject: "sub-audit", Provider: auth.ProviderOIDC, DisplayName: "zoe",
		Groups: []string{"platform-admins"},
	}}
	h, _, auditLog := ssoGroupServer(t, fake, settings.SSO{
		GroupRoles: []settings.GroupRole{{Group: "platform-admins", Admin: true}},
	})
	ssoCallback(t, h, "s1")

	entries, err := auditLog.List(audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(entries, func(e *audit.Entry) bool { return e.Action == "user.sso.provision" })
	if idx < 0 {
		t.Fatalf("SSO account creation must be audited, got %+v", entries)
	}
	e := entries[idx]
	if e.User != "zoe" {
		t.Errorf("entry should name the provisioned user, got %q", e.User)
	}
	if !strings.Contains(e.Detail, "admin") || !strings.Contains(e.Detail, "platform-admins") {
		t.Errorf("detail should record the granted role and matched groups, got %q", e.Detail)
	}

	// A returning user is not a creation, so it must not add another entry.
	ssoCallback(t, h, "s2")
	entries, _ = auditLog.List(audit.Filter{})
	provisions := 0
	for _, e := range entries {
		if e.Action == "user.sso.provision" {
			provisions++
		}
	}
	if provisions != 1 {
		t.Fatalf("want exactly one provision entry, got %d", provisions)
	}
}

func TestSSOSyncOnLoginReappliesGroups(t *testing.T) {
	id := auth.Identity{
		Subject: "sub-sync", Provider: auth.ProviderOIDC, DisplayName: "sam",
		Groups: []string{"developers"},
	}
	fake := &fakeRedirect{id: id}
	h, users, auditLog := ssoGroupServer(t, fake, settings.SSO{
		SyncGroupsOnLogin: true,
		GroupRoles: []settings.GroupRole{
			{Group: "developers", Perms: []string{user.PermResources}},
			{Group: "platform-admins", Admin: true},
		},
	})
	ssoCallback(t, h, "s1")
	u, _ := users.BySubject(auth.ProviderOIDC, "sub-sync")
	if !u.Can(user.PermResources) || u.Admin {
		t.Fatalf("first login: %+v", u)
	}

	// The IdP promotes them; the next login reflects it.
	fake.id.Groups = []string{"platform-admins"}
	ssoCallback(t, h, "s2")
	u, _ = users.BySubject(auth.ProviderOIDC, "sub-sync")
	if !u.Admin {
		t.Fatalf("sync-on-login should have promoted the user: %+v", u)
	}
	entries, _ := auditLog.List(audit.Filter{})
	if !slices.ContainsFunc(entries, func(e *audit.Entry) bool { return e.Action == "user.sso.sync" }) {
		t.Errorf("a role change on login must be audited, got %+v", entries)
	}

	// And a demotion is reflected too — that is the cost of IdP-as-truth.
	fake.id.Groups = []string{"interns"}
	ssoCallback(t, h, "s3")
	u, _ = users.BySubject(auth.ProviderOIDC, "sub-sync")
	if u.Admin || len(u.Permissions) > 0 {
		t.Fatalf("sync-on-login should have demoted the user: %+v", u)
	}
}

func TestSSOWithoutSyncLeavesManualGrants(t *testing.T) {
	fake := &fakeRedirect{id: auth.Identity{
		Subject: "sub-manual", Provider: auth.ProviderOIDC, DisplayName: "mo",
		Groups: []string{"interns"},
	}}
	h, users, _ := ssoGroupServer(t, fake, settings.SSO{ // SyncGroupsOnLogin off (default)
		GroupRoles: []settings.GroupRole{{Group: "platform-admins", Admin: true}},
	})
	ssoCallback(t, h, "s1")
	u, _ := users.BySubject(auth.ProviderOIDC, "sub-manual")

	// An admin grants access by hand in the Users tab.
	if err := users.Update(u.ID, func(stored *user.User) error {
		stored.Permissions = []string{user.PermDeploy}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ssoCallback(t, h, "s2")

	u, _ = users.BySubject(auth.ProviderOIDC, "sub-manual")
	if !u.Can(user.PermDeploy) {
		t.Fatalf("without sync-on-login a manual grant must survive: %+v", u)
	}
}
