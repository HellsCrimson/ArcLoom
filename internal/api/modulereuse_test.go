package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Two calls to the SAME local module under different names is the shape the
// call/definition split exists for. Both must survive an import, generate two
// module blocks, and round-trip back through a second import.
func TestImport_TwoCallsSameLocalModule(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu}
	h, st := newImportTestServer(t, p)

	files := map[string]string{
		"main.tf": `module "team_a" {
  source = "./modules/team"
  name   = "a"
}

module "team_b" {
  source = "./modules/team"
  name   = "b"
}
`,
		"modules/team/main.tf": `variable "name" { type = string }
resource "null_resource" "x" { }
`,
	}
	body, _ := json.Marshal(map[string]any{"files": files})
	resp := postImport(t, h, "p1", string(body))

	got, _ := st.Get("p1")
	if len(got.Modules) != 2 {
		t.Fatalf("want 2 module calls, got %d (%+v); warnings=%v", len(got.Modules), got.Modules, resp.Warnings)
	}
	names := []string{got.Modules[0].Name, got.Modules[1].Name}
	if names[0] != "team_a" || names[1] != "team_b" {
		t.Errorf("call names = %v, want [team_a team_b]", names)
	}
	for _, m := range got.Modules {
		if m.Source != "./modules/team" {
			t.Errorf("call %q source = %q", m.Name, m.Source)
		}
	}
	if len(got.LocalModules) != 1 {
		t.Fatalf("want 1 definition, got %d", len(got.LocalModules))
	}
	if n := len(got.LocalModules[0].Resources); n != 1 {
		t.Errorf("definition should carry its resource, got %d", n)
	}

	// Both calls must reach the generated HCL.
	out, err := hclgen.GenerateFiles(got)
	if err != nil {
		t.Fatal(err)
	}
	main := string(out["main.tf"])
	if !strings.Contains(main, `module "team_a"`) || !strings.Contains(main, `module "team_b"`) {
		t.Errorf("generated main.tf lost a call:\n%s", main)
	}

	// Re-importing the generated files into a fresh project must produce the
	// same two calls (the export/import round trip a user actually performs).
	p2 := &model.Project{ID: "p2", Name: "y", Engine: model.EngineTofu}
	h2, st2 := newImportTestServer(t, p2)
	again := map[string]string{}
	for name, content := range out {
		again[name] = string(content)
	}
	body2, _ := json.Marshal(map[string]any{"files": again})
	resp2 := postImport(t, h2, "p2", string(body2))
	got2, _ := st2.Get("p2")
	if len(got2.Modules) != 2 {
		t.Fatalf("round trip: want 2 calls, got %d (%+v); warnings=%v", len(got2.Modules), got2.Modules, resp2.Warnings)
	}
}

// A block whose name is already taken is dropped WHOLE (resources are renamed
// to `x_2` instead), so the import must say which ones — a silent skip is
// indistinguishable from a parse failure, and is what makes a second `module`
// call "just vanish".
func TestImport_ReportsSkippedBlocks(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		Modules:      []model.Module{{Name: "team_a", Source: "./modules/team"}},
		LocalModules: []model.LocalModule{{Name: "team"}},
		Variables:    []model.Variable{{Name: "region"}},
	}
	h, st := newImportTestServer(t, p)

	files := map[string]string{
		"main.tf": `variable "region" { type = string }
module "team_a" { source = "./modules/team" }
module "team_b" { source = "./modules/team" }
`,
	}
	body, _ := json.Marshal(map[string]any{"files": files})
	resp := postImport(t, h, "p1", string(body))

	got, _ := st.Get("p1")
	if len(got.Modules) != 2 {
		t.Fatalf("the free call must still import: %+v", got.Modules)
	}
	joined := strings.Join(resp.Warnings, "\n")
	for _, want := range []string{`module call "team_a" already exists`, `variable "region" already exists`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "team_b") {
		t.Errorf("team_b imported fine, it must not be reported:\n%s", joined)
	}
}

// Two module blocks sharing ONE label is invalid HCL that the parser still
// accepts; the second is dropped on merge, and that must be reported rather
// than looking like a successful import of both.
func TestImport_DuplicateCallNameReported(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu}
	h, st := newImportTestServer(t, p)

	files := map[string]string{
		"main.tf": `module "team" {
  source = "./modules/team"
  name   = "a"
}
module "team" {
  source = "./modules/team"
  name   = "b"
}
`,
	}
	body, _ := json.Marshal(map[string]any{"files": files})
	resp := postImport(t, h, "p1", string(body))

	got, _ := st.Get("p1")
	if len(got.Modules) != 1 {
		t.Fatalf("want the one surviving call, got %d: %+v", len(got.Modules), got.Modules)
	}
	if resp.AddedModules != 1 {
		t.Errorf("addedModules = %d, want 1", resp.AddedModules)
	}
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), `module call "team" already exists`) {
		t.Errorf("the dropped duplicate was not reported: %v", resp.Warnings)
	}
}
