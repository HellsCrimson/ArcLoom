package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/audit"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// revealEnv builds a server with auth ON (the gate is the point of the feature)
// plus a real secret store holding one value, and a real audit log.
func revealEnv(t *testing.T) (http.Handler, audit.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewStore(t.TempDir(), "master-key-for-tests")
	if err := sec.Set("p1", "global_token", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	users := user.NewMemory()
	hash, _ := auth.Hash("password1")
	mk := func(name string, admin bool, perms ...string) {
		_ = users.Create(&user.User{ID: name, Username: name, PasswordHash: hash,
			Provider: auth.ProviderLocal, CreatedAt: time.Unix(0, 0), Admin: admin, Permissions: perms})
	}
	mk("admin", true)
	mk("writer", false, user.PermResources)
	mk("deployer", false, user.PermDeploy)
	mk("reader", false)

	auditLog, err := audit.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: true, CookieName: "arcloom_session", SessionTTL: time.Hour, DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(users))
	h := New(cfg, nil, st, nil, sec, nil, authn, session.NewMemory(), users, testSettings(t), nil, auditLog, nil, nil).Handler()
	return h, auditLog
}

const revealPath = "/api/projects/p1/secrets/global_token/reveal"

// Reveal is the only route that hands a secret value back, so who may call it
// IS the feature: the users who can already overwrite the value, and nobody
// else. A read-only or deploy-only user must not get it.
func TestRevealSecret_Permissions(t *testing.T) {
	h, _ := revealEnv(t)
	for _, tc := range []struct {
		who  string
		user string
		want int
	}{
		{"resources", "writer", http.StatusOK},
		{"admin", "admin", http.StatusOK},
		{"read-only", "reader", http.StatusForbidden},
		{"deploy-only", "deployer", http.StatusForbidden},
	} {
		rec := do(t, h, http.MethodPost, revealPath, login(t, h, tc.user, "password1"), "")
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.who, rec.Code, tc.want, rec.Body.String())
		}
		if tc.want != http.StatusOK && strings.Contains(rec.Body.String(), "s3cr3t-value") {
			t.Errorf("%s: the value leaked in a refusal: %s", tc.who, rec.Body.String())
		}
	}
	// Unauthenticated.
	if rec := do(t, h, http.MethodPost, revealPath, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", rec.Code)
	}
}

func TestRevealSecret_ValueAndAudit(t *testing.T) {
	h, auditLog := revealEnv(t)
	writer := login(t, h, "writer", "password1")

	rec := do(t, h, http.MethodPost, revealPath, writer, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal = %d (%s)", rec.Code, rec.Body.String())
	}
	var got struct{ Name, Value string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "global_token" || got.Value != "s3cr3t-value" {
		t.Fatalf("wrong payload: %+v", got)
	}

	// An unknown name is a 404, not an empty string: "" is a legitimate stored
	// value, and the two must not look alike in the UI.
	if rec := do(t, h, http.MethodPost, "/api/projects/p1/secrets/nope/reveal", writer, ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown secret = %d, want 404", rec.Code)
	}

	// Every reveal is recorded — it is the only way a value leaves the server —
	// and the record names the secret without containing it.
	entries, err := auditLog.List(audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Action != "secret.reveal" {
			continue
		}
		found = true
		if e.User != "writer" || e.ProjectID != "p1" {
			t.Errorf("audit entry lacks actor/project: %+v", e)
		}
		if strings.Contains(e.Detail, "s3cr3t-value") {
			t.Errorf("the value was written to the audit log: %+v", e)
		}
		if !strings.Contains(e.Detail, "global_token") {
			t.Errorf("audit entry does not name the secret: %+v", e)
		}
	}
	if !found {
		t.Errorf("no secret.reveal audit entry; got %+v", entries)
	}

	// The listing stays value-free: reveal is one name at a time, on purpose.
	if rec := do(t, h, http.MethodGet, "/api/projects/p1/secrets", writer, ""); strings.Contains(rec.Body.String(), "s3cr3t-value") {
		t.Errorf("the listing leaked a value: %s", rec.Body.String())
	}
}
