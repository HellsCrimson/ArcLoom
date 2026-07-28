package hclimport

import (
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/zclconf/go-cty/cty"
)

// TestRoundTrip_LocalModule proves import inverts generation: a project with a
// local module, rendered to files by hclgen and parsed back by hclimport,
// reconstructs the local module with its variables and the root's inputs.
func TestRoundTrip_LocalModule(t *testing.T) {
	proj := &model.Project{
		ID:     "p1",
		Name:   "demo",
		Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "random", Source: "hashicorp/random"},
		},
		LocalModules: []model.LocalModule{{
			Name: "vpc",
			Variables: []model.Variable{
				{Name: "cidr", Type: "string"},
			},
			Resources: []model.Resource{
				{ID: "r1", Type: "random_pet", Name: "n", Attrs: map[string]model.AttrValue{
					"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(2)},
				}},
			},
			Outputs: []model.Output{
				{Name: "pet", Value: model.AttrValue{Mode: model.ModeReference, Ref: "random_pet.n.id"}},
			},
		}},
		Modules: []model.Module{{
			Name: "vpc", Source: model.LocalSource("vpc"),
			Config: map[string]model.AttrValue{
				"cidr": {Mode: model.ModeLiteral, Literal: cty.StringVal("10.0.0.0/16")},
			},
		}},
	}

	files, err := hclgen.GenerateFiles(proj)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, ok := files["modules/vpc/main.tf"]; !ok {
		t.Fatalf("expected a modules/vpc/main.tf, got files: %v", keys(files))
	}

	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.LocalModules) != 1 {
		t.Fatalf("expected the local module to round-trip, got %d (modules=%+v warnings=%v)", len(res.LocalModules), res.Modules, res.Warnings)
	}
	lm := res.LocalModules[0]
	if lm.Name != "vpc" || len(lm.Variables) != 1 || lm.Variables[0].Name != "cidr" {
		t.Errorf("variables lost: %+v", lm)
	}
	if len(res.Modules) != 1 || res.Modules[0].LocalFolder() != "vpc" {
		t.Fatalf("the call lost its definition: %+v", res.Modules)
	}
	if got := res.Modules[0].Config["cidr"]; got.Mode != model.ModeLiteral || got.Literal.AsString() != "10.0.0.0/16" {
		t.Errorf("input lost: %+v", res.Modules[0].Config)
	}
	if len(lm.Resources) != 1 || len(lm.Outputs) != 1 {
		t.Errorf("module body lost: %+v", lm)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
