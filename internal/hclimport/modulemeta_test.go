package hclimport

import (
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// TestParse_ModuleCountAndForEach covers the meta-args on a module *call*.
// Before Module/LocalModule carried Count/ForEach these landed in Config/Inputs
// as if they were module variables, where the inputs form later deleted them.
func TestParse_ModuleCountAndForEach(t *testing.T) {
	src := `
module "team" {
  source   = "./modules/team"
  for_each = var.teams

  name = each.value.name
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
  count   = var.enabled ? 1 : 0
  cidr    = "10.0.0.0/16"
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Both blocks are calls — local and registry live in one list.
	if len(res.Modules) != 2 {
		t.Fatalf("expected 2 module calls, got %d", len(res.Modules))
	}
	byName := map[string]model.Module{}
	for _, m := range res.Modules {
		byName[m.Name] = m
	}
	vpc := byName["vpc"]
	if vpc.Count != "var.enabled ? 1 : 0" {
		t.Errorf("registry module count not lifted: %q", vpc.Count)
	}
	if _, ok := vpc.Config["count"]; ok {
		t.Error("count leaked into Config as a fake module input")
	}
	if got := vpc.Config["cidr"]; got.Mode != model.ModeLiteral {
		t.Errorf("real inputs should be untouched, got %+v", got)
	}

	team := byName["team"]
	if team.ForEach != "var.teams" {
		t.Errorf("local module for_each not carried to the call: %q", team.ForEach)
	}
	if _, ok := team.Config["for_each"]; ok {
		t.Error("for_each leaked into Config as a fake module input")
	}
	// each.value.* inputs must survive as references.
	if got := team.Config["name"]; got.Mode != model.ModeReference || got.Ref != "each.value.name" {
		t.Errorf("each.value input lost: %+v", got)
	}

	// The local call gets a definition to point at, even though the module's
	// own files were not part of this import.
	if len(res.LocalModules) != 1 || res.LocalModules[0].Name != "team" {
		t.Fatalf("expected a team definition, got %+v", res.LocalModules)
	}
}

// TestParse_MultipleCallsOneDefinition is the point of splitting calls from
// definitions: two `module` blocks on the same folder are two calls and ONE
// definition. The old importer merged them into a single local module, so the
// second call vanished from the model (and from the generated HCL).
func TestParse_MultipleCallsOneDefinition(t *testing.T) {
	src := `
module "team_alpha" {
  source = "./modules/team"
  name   = "alpha"
}

module "team_beta" {
  source   = "./modules/team"
  name     = "beta"
  for_each = toset(["a", "b"])
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Modules) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(res.Modules), res.Modules)
	}
	if len(res.LocalModules) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(res.LocalModules))
	}
	for _, m := range res.Modules {
		if m.LocalFolder() != "team" {
			t.Errorf("call %q should resolve to the team definition, got %q", m.Name, m.LocalFolder())
		}
	}
}

// TestRoundTrip_ModuleMetaArgs proves the meta-args survive generate -> parse,
// which is what makes an imported for_each editable instead of merely retained.
func TestRoundTrip_ModuleMetaArgs(t *testing.T) {
	proj := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Modules: []model.Module{
			{
				Name: "vpc", Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0",
				ForEach: "var.regions",
				Config:  map[string]model.AttrValue{"name": {Mode: model.ModeReference, Ref: "each.key"}},
			},
			{
				Name: "team", Source: model.LocalSource("team"), Count: "3",
				Config: map[string]model.AttrValue{"idx": {Mode: model.ModeReference, Ref: "count.index"}},
			},
		},
		LocalModules: []model.LocalModule{{
			Name:      "team",
			Variables: []model.Variable{{Name: "idx", Type: "number"}},
		}},
	}

	files, err := hclgen.GenerateFiles(proj)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	root := string(files["main.tf"])
	if !strings.Contains(root, "for_each = var.regions") {
		t.Errorf("registry module for_each not emitted:\n%s", root)
	}
	// hclwrite pads to align the `=`, so match on the normalised spacing.
	if !strings.Contains(strings.Join(strings.Fields(root), " "), "count = 3") {
		t.Errorf("local module count not emitted:\n%s", root)
	}

	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Modules) != 2 {
		t.Fatalf("expected both calls back, got %+v", res.Modules)
	}
	back := map[string]model.Module{}
	for _, m := range res.Modules {
		back[m.Name] = m
	}
	if back["vpc"].ForEach != "var.regions" {
		t.Errorf("registry for_each lost: %+v", back["vpc"])
	}
	if back["team"].Count != "3" {
		t.Errorf("local module call count lost: %+v", back["team"])
	}
	if len(res.LocalModules) != 1 || res.LocalModules[0].Name != "team" {
		t.Errorf("local module definition lost: %+v", res.LocalModules)
	}
}

// TestGenerate_ModuleCountForEachMutuallyExclusive: the generator is the last
// line of defence (the store swallows generation errors, so the API validates
// too) — setting both must be an error, not silently invalid HCL.
func TestGenerate_ModuleCountForEachMutuallyExclusive(t *testing.T) {
	proj := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Modules: []model.Module{{
			Name: "vpc", Source: "x/y/z", Count: "1", ForEach: "var.t",
		}},
	}
	if _, err := hclgen.GenerateFiles(proj); err == nil {
		t.Fatal("expected count+for_each on a module to be rejected")
	}
}
