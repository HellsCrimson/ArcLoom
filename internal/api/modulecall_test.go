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

// TestModuleCallCRUD covers the one endpoint both kinds of module call go
// through: a registry source and a local "./modules/<folder>" one are the same
// block and share the `module "<name>"` namespace.
func TestModuleCallCRUD(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "net"}}}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil).Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/modules/call", strings.NewReader(body)))
		return rec
	}
	call := func(name string) model.Module {
		t.Helper()
		got, _ := st.Get("p1")
		for _, m := range got.Modules {
			if m.Name == name {
				return m
			}
		}
		t.Fatalf("no call %q in %+v", name, got.Modules)
		return model.Module{}
	}

	// Create with an expression input.
	rec := put(`{"name":"vpc","source":"terraform-aws-modules/vpc/aws","version":"5.0.0","config":{"cidr":{"mode":"expression","expr":"\"10.0.0.0/16\""}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create registry module = %d (body %s)", rec.Code, rec.Body.String())
	}
	if c := call("vpc"); c.Source != "terraform-aws-modules/vpc/aws" || c.Version != "5.0.0" {
		t.Fatalf("module not stored: %+v", c)
	}
	if _, ok := call("vpc").Config["cidr"]; !ok {
		t.Fatalf("config not stored: %+v", call("vpc").Config)
	}

	// Upsert by name replaces, not duplicates.
	if rec := put(`{"name":"vpc","source":"terraform-aws-modules/vpc/aws","version":"5.1.0","config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("update = %d", rec.Code)
	}
	got, _ := st.Get("p1")
	if len(got.Modules) != 1 || got.Modules[0].Version != "5.1.0" || got.Modules[0].Config != nil {
		t.Fatalf("upsert did not replace: %+v", got.Modules)
	}

	// Two calls to ONE local definition — the whole point of the split.
	if rec := put(`{"name":"net_a","source":"./modules/net","config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("first local call = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := put(`{"name":"net_b","source":"./modules/net","config":{},"forEach":"toset([\"x\"])"}`); rec.Code != http.StatusOK {
		t.Fatalf("second local call = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if n := len(got.CallsFor("net")); n != 2 {
		t.Fatalf("expected 2 calls to net, got %d: %+v", n, got.Modules)
	}
	if len(got.LocalModules) != 1 {
		t.Fatalf("the definition must not be duplicated: %+v", got.LocalModules)
	}

	// Rename moves the call instead of creating a second one.
	if rec := put(`{"name":"net_c","rename":"net_b","source":"./modules/net","config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if len(got.CallsFor("net")) != 2 {
		t.Fatalf("rename duplicated the call: %+v", got.Modules)
	}
	call("net_c")

	// Renaming onto an existing call name is rejected (duplicate labels are
	// invalid HCL).
	if rec := put(`{"name":"net_a","rename":"net_c","source":"./modules/net","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("rename onto an existing name = %d, want 400", rec.Code)
	}

	// A local source with no definition is rejected.
	if rec := put(`{"name":"ghost","source":"./modules/nope","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown local source = %d, want 400", rec.Code)
	}
	// Invalid expression input is rejected.
	if rec := put(`{"name":"v2","source":"foo/bar/baz","config":{"x":{"mode":"expression","expr":"]] nope"}}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad expression = %d, want 400", rec.Code)
	}
	// Missing source is rejected.
	if rec := put(`{"name":"v3","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing source = %d, want 400", rec.Code)
	}

	// Deleting a definition while a call still uses it is refused...
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/p1/modules/local/net", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete used definition = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// ...and goes through with ?force=true, taking the calls with it.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/p1/modules/local/net?force=true", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("forced delete = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if len(got.LocalModules) != 0 || len(got.CallsFor("net")) != 0 {
		t.Fatalf("forced delete left something behind: mods=%+v calls=%+v", got.LocalModules, got.Modules)
	}

	// Delete a call.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/p1/modules/call/vpc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
	if got, _ = st.Get("p1"); len(got.Modules) != 0 {
		t.Fatalf("module not deleted: %+v", got.Modules)
	}
}

// TestCreateLocalModuleAddsCall: defining a module also uses it once, so the
// common case stays one step — but `call:false` leaves the definition unused,
// which is the state that used to be unrepresentable.
func TestCreateLocalModuleAddsCall(t *testing.T) {
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil).Handler()

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/projects/p1/modules/local", strings.NewReader(body)))
		return rec
	}

	if rec := post(`{"name":"team"}`); rec.Code != http.StatusOK {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.CallsFor("team")) != 1 {
		t.Fatalf("expected an auto-call for team: %+v", got.Modules)
	}

	if rec := post(`{"name":"shared","call":false}`); rec.Code != http.StatusOK {
		t.Fatalf("create without call = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if len(got.CallsFor("shared")) != 0 {
		t.Fatalf("shared should be defined but unused: %+v", got.Modules)
	}
	if len(got.LocalModules) != 2 {
		t.Fatalf("expected both definitions: %+v", got.LocalModules)
	}
}
