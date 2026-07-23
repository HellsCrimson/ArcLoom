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

func TestRegistryModuleCRUD(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "net"}}}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil).Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/modules/registry", strings.NewReader(body)))
		return rec
	}

	// Create with an expression input.
	rec := put(`{"name":"vpc","source":"terraform-aws-modules/vpc/aws","version":"5.0.0","config":{"cidr":{"mode":"expression","expr":"\"10.0.0.0/16\""}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create registry module = %d (body %s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.Modules) != 1 || got.Modules[0].Source != "terraform-aws-modules/vpc/aws" || got.Modules[0].Version != "5.0.0" {
		t.Fatalf("module not stored: %+v", got.Modules)
	}
	if _, ok := got.Modules[0].Config["cidr"]; !ok {
		t.Fatalf("config not stored: %+v", got.Modules[0].Config)
	}

	// Upsert by name replaces, not duplicates.
	if rec := put(`{"name":"vpc","source":"terraform-aws-modules/vpc/aws","version":"5.1.0","config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("update = %d", rec.Code)
	}
	got, _ = st.Get("p1")
	if len(got.Modules) != 1 || got.Modules[0].Version != "5.1.0" || got.Modules[0].Config != nil {
		t.Fatalf("upsert did not replace: %+v", got.Modules)
	}

	// Name colliding with a local module is rejected.
	if rec := put(`{"name":"net","source":"foo/bar/baz","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("local-name collision = %d, want 400", rec.Code)
	}
	// Invalid expression input is rejected.
	if rec := put(`{"name":"v2","source":"foo/bar/baz","config":{"x":{"mode":"expression","expr":"]] nope"}}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad expression = %d, want 400", rec.Code)
	}
	// Missing source is rejected.
	if rec := put(`{"name":"v3","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing source = %d, want 400", rec.Code)
	}

	// Delete.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/p1/modules/registry/vpc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
	if got, _ = st.Get("p1"); len(got.Modules) != 0 {
		t.Fatalf("module not deleted: %+v", got.Modules)
	}
}
