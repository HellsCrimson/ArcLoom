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

// TestModuleMetaArgs covers count/for_each on both kinds of module call: they
// persist, they are mutually exclusive, and a malformed expression is rejected
// here (the store swallows generation errors, so a typo must fail at the API).
func TestModuleMetaArgs(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "team"}}}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil).Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	const callPath = "/api/projects/p1/modules/call"
	call := func(p *model.Project, name string) model.Module {
		t.Helper()
		for _, m := range p.Modules {
			if m.Name == name {
				return m
			}
		}
		t.Fatalf("no call %q in %+v", name, p.Modules)
		return model.Module{}
	}

	// Registry module: for_each persists.
	if rec := do(http.MethodPut, callPath,
		`{"name":"vpc","source":"terraform-aws-modules/vpc/aws","config":{},"forEach":"var.regions"}`); rec.Code != http.StatusOK {
		t.Fatalf("create with for_each = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if call(got, "vpc").ForEach != "var.regions" {
		t.Fatalf("registry for_each not stored: %+v", got.Modules)
	}

	// Local module call: count persists next to the inputs, through the very
	// same endpoint — one block type, one handler.
	if rec := do(http.MethodPut, callPath,
		`{"name":"team","source":"./modules/team","config":{"idx":{"mode":"expression","expr":"count.index"}},"count":"3"}`); rec.Code != http.StatusOK {
		t.Fatalf("set local call with count = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if c := call(got, "team"); c.Count != "3" || c.LocalFolder() != "team" {
		t.Fatalf("local module call not stored: %+v", c)
	}

	// Omitting them clears them — the "upsert replaces the call" contract that
	// the form has to honour by always resending.
	if rec := do(http.MethodPut, callPath, `{"name":"team","source":"./modules/team","config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if c := call(got, "team"); c.Count != "" {
		t.Fatalf("count should have been cleared: %+v", c)
	}

	// A local source with no matching definition is rejected, so generation
	// can never emit a call into a folder that is never written.
	if rec := do(http.MethodPut, callPath, `{"name":"ghost","source":"./modules/nope","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for an unknown local source, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Mutual exclusion and expression validity, for both kinds of source.
	bad := []struct{ path, body string }{
		{callPath, `{"name":"vpc","source":"a/b/c","config":{},"count":"1","forEach":"var.t"}`},
		{callPath, `{"name":"vpc","source":"a/b/c","config":{},"forEach":"var.["}`},
		{callPath, `{"name":"team","source":"./modules/team","config":{},"count":"1","forEach":"var.t"}`},
		{callPath, `{"name":"team","source":"./modules/team","config":{},"count":"1 +"}`},
	}
	for _, c := range bad {
		if rec := do(http.MethodPut, c.path, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %s, got %d (%s)", c.body, rec.Code, rec.Body.String())
		}
	}
}
