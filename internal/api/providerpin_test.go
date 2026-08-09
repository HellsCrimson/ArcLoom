package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// TestProviderSourceVersionEdit covers the three-way `version` semantics the
// provider form needs: absent leaves the pin alone (every caller before it did
// that), "" unpins, and a value pins. Source edits are validated because the
// value becomes an HCL identifier via providerKeyFromSource.
func TestProviderSourceVersionEdit(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{{Key: "null", Source: "hashicorp/null", Version: "3.2.1"}}}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil, nil).Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/providers/null", strings.NewReader(body)))
		return rec
	}
	pin := func() string {
		got, _ := st.Get("p1")
		return got.Providers[0].Version
	}

	// Absent version: the pin survives a plain config save.
	if rec := put(`{"config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("config-only save = %d (%s)", rec.Code, rec.Body.String())
	}
	if pin() != "3.2.1" {
		t.Fatalf("omitting version must not change the pin, got %q", pin())
	}

	// A value pins; a constraint expression is accepted too.
	if rec := put(`{"config":{},"version":"~> 3.2"}`); rec.Code != http.StatusOK {
		t.Fatalf("pin = %d (%s)", rec.Code, rec.Body.String())
	}
	if pin() != "~> 3.2" {
		t.Fatalf("version not stored, got %q", pin())
	}

	// Empty string unpins — impossible before, since "" meant "leave alone".
	if rec := put(`{"config":{},"version":""}`); rec.Code != http.StatusOK {
		t.Fatalf("unpin = %d (%s)", rec.Code, rec.Body.String())
	}
	if pin() != "" {
		t.Fatalf("version should have been cleared, got %q", pin())
	}

	// Source edits apply and are validated.
	if rec := put(`{"config":{},"source":"integrations/github"}`); rec.Code != http.StatusOK {
		t.Fatalf("source edit = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if got.Providers[0].Source != "integrations/github" {
		t.Fatalf("source not stored: %+v", got.Providers[0])
	}

	for _, bad := range []string{
		`{"config":{},"source":"../../etc/passwd"}`,
		`{"config":{},"source":"has space/x"}`,
		`{"config":{},"version":"not a constraint"}`,
	} {
		if rec := put(bad); rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %s, got %d (%s)", bad, rec.Code, rec.Body.String())
		}
	}
}
