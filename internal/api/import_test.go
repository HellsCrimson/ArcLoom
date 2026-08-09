package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// newImportTestServer builds a server with auth disabled and a seeded project.
func newImportTestServer(t *testing.T, p *model.Project) (http.Handler, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session"}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	srv := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil)
	return srv.Handler(), st
}

func postImport(t *testing.T, h http.Handler, projectID, body string) importResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/import/hcl", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	var resp importResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestImport_IntoModuleScope: importing with a module target lands the parsed
// blocks inside that local module, not the project root.
func TestImport_IntoModuleScope(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "vpc"}}}
	h, st := newImportTestServer(t, p)

	body := `{"module":"vpc","files":{"m.tf":"variable \"cidr\" { type = string }\nresource \"random_pet\" \"n\" { length = 2 }\n"}}`
	postImport(t, h, "p1", body)

	got, _ := st.Get("p1")
	if len(got.Resources) != 0 || len(got.Variables) != 0 {
		t.Fatalf("root scope must stay empty: res=%d vars=%d", len(got.Resources), len(got.Variables))
	}
	lm := got.LocalModules[0]
	if len(lm.Resources) != 1 || lm.Resources[0].Type != "random_pet" {
		t.Fatalf("module resources: %+v", lm.Resources)
	}
	if len(lm.Variables) != 1 || lm.Variables[0].Name != "cidr" {
		t.Fatalf("module variables: %+v", lm.Variables)
	}
	// The provider is still registered at the root.
	if len(got.Providers) != 1 || got.Providers[0].Key != "random" {
		t.Fatalf("provider should be at root: %+v", got.Providers)
	}
}

func TestImport_IntoMissingModule404(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu}
	h, _ := newImportTestServer(t, p)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/import/hcl",
		strings.NewReader(`{"module":"ghost","files":{"m.tf":"resource \"random_pet\" \"n\" {}"}}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// TestImport_LocalModuleCallNoDuplicate: importing a main.tf whose module call
// uses a local source creates a LocalModule (not a remote module), so a single
// `module` block is generated and a manual create of the same name is rejected.
func TestImport_LocalModuleCallNoDuplicate(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu}
	h, st := newImportTestServer(t, p)

	postImport(t, h, "p1", `{"files":{"main.tf":"module \"vpc\" {\n  source = \"./modules/vpc\"\n  cidr = \"10.0.0.0/16\"\n}\n"}}`)

	got, _ := st.Get("p1")
	if len(got.Modules) != 1 || got.Modules[0].LocalFolder() != "vpc" {
		t.Fatalf("expected one local call to vpc: %+v", got.Modules)
	}
	if len(got.LocalModules) != 1 || got.LocalModules[0].Name != "vpc" {
		t.Fatalf("expected one local module 'vpc': %+v", got.LocalModules)
	}
	if _, ok := got.Modules[0].Config["cidr"]; !ok {
		t.Fatalf("inputs not captured: %+v", got.Modules[0].Config)
	}

	// Generation must emit exactly one `module "vpc"` block.
	root, err := hclgen.Generate(got)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(root), `module "vpc"`); n != 1 {
		t.Fatalf("expected exactly one module block, got %d:\n%s", n, root)
	}

	// Manually creating a module with the same name is rejected (no dup).
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/modules/local",
		strings.NewReader(`{"name":"vpc"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("manual create of existing module: status %d, want 400", rec.Code)
	}
}

// Two entries with the same key would generate one HCL object with a duplicated
// attribute, which the engine rejects at plan time — catch it at the edit.
func TestAttrValueDTO_RejectsDuplicateKeys(t *testing.T) {
	dto := attrValueDTO{
		Mode: model.ModeLiteral,
		Entries: []attrEntryDTO{
			{Key: "a", Value: attrValueDTO{Mode: model.ModeLiteral, Literal: []byte(`"1"`)}},
			{Key: "a", Value: attrValueDTO{Mode: model.ModeLiteral, Literal: []byte(`"2"`)}},
		},
	}
	if _, err := dto.toModel(); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("expected a duplicate-key error, got %v", err)
	}

	// Nested objects are checked too, and distinct keys still pass.
	ok := attrValueDTO{
		Mode: model.ModeLiteral,
		Entries: []attrEntryDTO{
			{Key: "events", Value: attrValueDTO{Mode: model.ModeLiteral, Entries: []attrEntryDTO{
				{Key: "x", Value: attrValueDTO{Mode: model.ModeLiteral, Literal: []byte(`true`)}},
				{Key: "y", Value: attrValueDTO{Mode: model.ModeLiteral, Literal: []byte(`false`)}},
			}}},
		},
	}
	if _, err := ok.toModel(); err != nil {
		t.Fatalf("distinct keys should convert: %v", err)
	}
	bad := attrValueDTO{
		Mode: model.ModeLiteral,
		Entries: []attrEntryDTO{
			{Key: "events", Value: attrValueDTO{Mode: model.ModeLiteral, Entries: []attrEntryDTO{
				{Key: "x", Value: attrValueDTO{Mode: model.ModeLiteral, Literal: []byte(`true`)}},
				{Key: "x", Value: attrValueDTO{Mode: model.ModeLiteral, Literal: []byte(`false`)}},
			}}},
		},
	}
	if _, err := bad.toModel(); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("expected the nested duplicate to be caught, got %v", err)
	}
}
