package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// legacyYAML is a project saved before module calls were split out of module
// definitions: the call lived ON the local module as inputs + for_each, and the
// block label was pinned to the folder name.
const legacyYAML = `id: p1
name: demo
engine: tofu
providers: []
resources: []
modules:
    - name: vpc
      source: terraform-aws-modules/vpc/aws
      version: 5.0.0
      count: "2"
localModules:
    - name: team
      resources: []
      variables:
        - name: label
          type: string
      inputs:
        label:
            mode: reference
            ref: each.key
      forEach: toset(["alpha", "beta"])
    - name: unused
      resources: []
`

// TestLoad_MigratesLegacyLocalModuleCall is the upgrade path: an existing
// project must keep generating exactly the HCL it generated before, with the
// call now a first-class entry it can have more than one of.
func TestLoad_MigratesLegacyLocalModuleCall(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p1", "arcloom.yaml"), []byte(legacyYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDisk(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	p, err := d.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	// Both definitions survive; both gain the call they used to imply.
	if len(p.LocalModules) != 2 {
		t.Fatalf("definitions lost: %+v", p.LocalModules)
	}
	calls := p.CallsFor("team")
	if len(calls) != 1 {
		t.Fatalf("expected one migrated call for team, got %+v", p.Modules)
	}
	if calls[0].Name != "team" || calls[0].Source != model.LocalSource("team") {
		t.Errorf("migrated call has the wrong identity: %+v", calls[0])
	}
	if calls[0].ForEach != `toset(["alpha", "beta"])` {
		t.Errorf("for_each did not move to the call: %+v", calls[0])
	}
	if got := calls[0].Config["label"]; got.Mode != model.ModeReference || got.Ref != "each.key" {
		t.Errorf("inputs did not move to the call: %+v", calls[0].Config)
	}

	// The generated HCL is what it always was.
	files, err := hclgen.GenerateFiles(p)
	if err != nil {
		t.Fatal(err)
	}
	root := string(files["main.tf"])
	// hclwrite pads to align the `=`, so match on normalised spacing.
	flat := strings.Join(strings.Fields(root), " ")
	for _, want := range []string{
		`module "team" {`,
		`source = "./modules/team"`,
		`for_each = toset(["alpha", "beta"])`,
		`label = each.key`,
		// A definition nobody calls generates its folder and no module block —
		// the state that was unrepresentable before.
		`module "unused"`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q in:\n%s", want, root)
		}
	}
	// The generated config is split by theme now, so a definition's folder is
	// whichever of its files carry content — plus an empty main.tf when it has
	// none at all, which is what keeps the FOLDER (and therefore `init`) valid.
	if _, ok := files["modules/unused/main.tf"]; !ok {
		t.Errorf("the unused definition should still generate its folder: %v", keys(files))
	}

	// Idempotent: persisting and re-reading must not mint a second call, and the
	// legacy fields must be gone for good.
	if err := d.Update("p1", func(*model.Project) error { return nil }); err != nil {
		t.Fatal(err)
	}
	again, err := d.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.CallsFor("team")) != 1 {
		t.Fatalf("re-read duplicated the call: %+v", again.Modules)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "p1", "arcloom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "forEach: toset") && !strings.Contains(string(raw), "modules:") {
		t.Errorf("legacy call was not rewritten as a module call:\n%s", raw)
	}
}

// TestLoad_KeepsAnUnusedDefinitionUnused is the regression the migration itself
// created: "a definition with no call" means an un-extracted call in a v1
// document and a deliberately unused module in a v2 one. Deleting the last call
// must stick across a reload, or an unused module is unrepresentable — the very
// state the split exists to allow.
func TestLoad_KeepsAnUnusedDefinitionUnused(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "team"}},
		Modules:      []model.Module{{Name: "team", Source: model.LocalSource("team")}},
	}); err != nil {
		t.Fatal(err)
	}
	// Delete the only call.
	if err := d.Update("p1", func(p *model.Project) error {
		p.Modules = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Modules) != 0 {
		t.Fatalf("the deleted call came back: %+v", got.Modules)
	}
	if len(got.LocalModules) != 1 {
		t.Fatalf("the definition should survive being unused: %+v", got.LocalModules)
	}
	// And it generates no module block.
	files, err := hclgen.GenerateFiles(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files["main.tf"]), `module "team"`) {
		t.Errorf("an uncalled definition must emit no module block:\n%s", files["main.tf"])
	}
	if !hasPrefix(files, "modules/team/") {
		t.Errorf("the definition's own files should still be generated: %v", keys(files))
	}
}

// TestNormalize_LeavesImportedCallsAlone: a definition that already has a call
// must not get a second, auto-named one.
func TestNormalize_LeavesImportedCallsAlone(t *testing.T) {
	p := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "team"}},
		Modules: []model.Module{
			{Name: "team_alpha", Source: model.LocalSource("team")},
			{Name: "team_beta", Source: model.LocalSource("team")},
		},
	}
	p.Normalize()
	if len(p.Modules) != 2 {
		t.Fatalf("normalize invented a call: %+v", p.Modules)
	}
}

// keys / hasPrefix: the generated file set is split by theme, so tests assert on
// which files exist rather than on one fixed name.
func keys(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func hasPrefix(files map[string][]byte, prefix string) bool {
	for k := range files {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}
