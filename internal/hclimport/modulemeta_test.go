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

	if len(res.Modules) != 1 {
		t.Fatalf("expected 1 registry module, got %d", len(res.Modules))
	}
	vpc := res.Modules[0]
	if vpc.Count != "var.enabled ? 1 : 0" {
		t.Errorf("registry module count not lifted: %q", vpc.Count)
	}
	if _, ok := vpc.Config["count"]; ok {
		t.Error("count leaked into Config as a fake module input")
	}
	if got := vpc.Config["cidr"]; got.Mode != model.ModeLiteral {
		t.Errorf("real inputs should be untouched, got %+v", got)
	}

	if len(res.LocalModules) != 1 {
		t.Fatalf("expected 1 local module, got %d", len(res.LocalModules))
	}
	team := res.LocalModules[0]
	if team.ForEach != "var.teams" {
		t.Errorf("local module for_each not carried to the call: %q", team.ForEach)
	}
	if _, ok := team.Inputs["for_each"]; ok {
		t.Error("for_each leaked into Inputs as a fake module input")
	}
	// each.value.* inputs must survive as references.
	if got := team.Inputs["name"]; got.Mode != model.ModeReference || got.Ref != "each.value.name" {
		t.Errorf("each.value input lost: %+v", got)
	}
}

// TestRoundTrip_ModuleMetaArgs proves the meta-args survive generate -> parse,
// which is what makes an imported for_each editable instead of merely retained.
func TestRoundTrip_ModuleMetaArgs(t *testing.T) {
	proj := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Modules: []model.Module{{
			Name: "vpc", Source: "terraform-aws-modules/vpc/aws", Version: "5.0.0",
			ForEach: "var.regions",
			Config:  map[string]model.AttrValue{"name": {Mode: model.ModeReference, Ref: "each.key"}},
		}},
		LocalModules: []model.LocalModule{{
			Name:      "team",
			Count:     "3",
			Variables: []model.Variable{{Name: "idx", Type: "number"}},
			Inputs:    map[string]model.AttrValue{"idx": {Mode: model.ModeReference, Ref: "count.index"}},
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
	if len(res.Modules) != 1 || res.Modules[0].ForEach != "var.regions" {
		t.Errorf("registry for_each lost: %+v", res.Modules)
	}
	if len(res.LocalModules) != 1 || res.LocalModules[0].Count != "3" {
		t.Errorf("local module count lost: %+v", res.LocalModules)
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
