package hclgen

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/matthiasrauline/arcloom/internal/model"
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

func TestGenerate_DependsOn(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{{Key: "random", Source: "hashicorp/random"}},
		Resources: []model.Resource{
			{Type: "random_pet", Name: "srv", Attrs: map[string]model.AttrValue{
				"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(2)},
			}},
			{Type: "random_password", Name: "db",
				Attrs:     map[string]model.AttrValue{"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(16)}},
				DependsOn: []string{"random_pet.srv"},
			},
		},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "depends_on = [random_pet.srv]") {
		t.Errorf("depends_on not rendered as a traversal list:\n%s", s)
	}
	// Must not be quoted as a string.
	if strings.Contains(s, `"random_pet.srv"`) {
		t.Errorf("depends_on emitted as a string literal:\n%s", s)
	}
}

func TestGenerate_SecretEmitsSensitiveVariable(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{{Key: "random", Source: "hashicorp/random"}},
		Resources: []model.Resource{
			{Type: "random_password", Name: "db", Attrs: map[string]model.AttrValue{
				"override_special": {Mode: model.ModeSecret, SecretRef: "db_pw"},
			}},
		},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Reference is var.db_pw, and a sensitive variable is declared for it.
	if !strings.Contains(s, "override_special = var.db_pw") {
		t.Errorf("secret not referenced as var.db_pw:\n%s", s)
	}
	if !strings.Contains(s, `variable "db_pw"`) || !strings.Contains(s, "sensitive = true") {
		t.Errorf("sensitive variable for secret not declared:\n%s", s)
	}
	// The secret value/name must not appear as a literal string anywhere.
	if strings.Contains(s, `"db_pw"`) && !strings.Contains(s, `variable "db_pw"`) {
		t.Errorf("unexpected literal secret name")
	}
	refs := CollectSecretRefs(p)
	if len(refs) != 1 || refs[0] != "db_pw" {
		t.Errorf("CollectSecretRefs: %v", refs)
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
