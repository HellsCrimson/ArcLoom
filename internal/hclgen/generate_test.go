package hclgen

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func TestGenerate_LiteralReferenceExpression(t *testing.T) {
	p := &model.Project{
		Name:   "demo",
		Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "random", Source: "hashicorp/random", Version: "~> 3.0"},
		},
		Resources: []model.Resource{
			{
				Type: "random_pet", Name: "server",
				Attrs: map[string]model.AttrValue{
					"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(2)},
				},
			},
			{
				Type: "random_password", Name: "db",
				Attrs: map[string]model.AttrValue{
					"length":  {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(16)},
					"keepers": {Mode: model.ModeReference, Ref: "random_pet.server.keepers"},
					"special": {Mode: model.ModeExpression, Expr: "true"},
				},
			},
		},
	}

	out, err := Generate(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	s := string(out)

	wants := []string{
		`required_providers`,
		`source  = "hashicorp/random"`,
		`version = "~> 3.0"`,
		`resource "random_pet" "server"`,
		`resource "random_password" "db"`,
		`length  = 16`,
		`keepers = random_pet.server.keepers`, // reference -> traversal, not a string
		`special = true`,
	}
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("generated HCL missing %q\n---\n%s", w, s)
		}
	}
	// Reference must not be quoted as a string literal.
	if strings.Contains(s, `"random_pet.server.keepers"`) {
		t.Errorf("reference was emitted as a string literal:\n%s", s)
	}
}

func TestGenerate_InvalidExpressionFails(t *testing.T) {
	p := &model.Project{
		Resources: []model.Resource{
			{
				Type: "random_pet", Name: "x",
				Attrs: map[string]model.AttrValue{
					"length": {Mode: model.ModeExpression, Expr: "this is ]] not valid"},
				},
			},
		},
	}
	if _, err := Generate(p); err == nil {
		t.Error("expected error for invalid expression, got nil")
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{{Key: "random", Source: "hashicorp/random"}},
		Resources: []model.Resource{
			{
				Type: "random_password", Name: "db",
				Attrs: map[string]model.AttrValue{
					"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(16)},
					"lower":  {Mode: model.ModeLiteral, Literal: cty.True},
					"upper":  {Mode: model.ModeLiteral, Literal: cty.True},
				},
			},
		},
	}
	first, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		next, err := Generate(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatal("output not deterministic across runs")
		}
	}
}
