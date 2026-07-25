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

func TestGenerate_Backend(t *testing.T) {
	p := &model.Project{
		Backend: &model.Backend{Type: "s3", Config: map[string]model.AttrValue{
			"bucket": {Mode: model.ModeLiteral, Literal: cty.StringVal("my-state")},
			"key":    {Mode: model.ModeLiteral, Literal: cty.StringVal("infra.tfstate")},
		}},
		Providers: []model.ProviderInstance{{Key: "random", Source: "hashicorp/random"}},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `backend "s3"`) || !strings.Contains(s, `bucket = "my-state"`) {
		t.Errorf("backend block not emitted:\n%s", s)
	}
	// Backend must be inside the terraform block, before required_providers.
	if strings.Index(s, `backend "s3"`) > strings.Index(s, "required_providers") {
		t.Errorf("backend should precede required_providers:\n%s", s)
	}
	// Schema-fetch config must never include the backend.
	pc, err := ProvidersConfig(p.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(pc), "backend") {
		t.Errorf("ProvidersConfig leaked the backend:\n%s", pc)
	}
}

func TestGenerate_CountAndForEach(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{{Key: "random", Source: "hashicorp/random"}},
		Resources: []model.Resource{
			{Type: "random_pet", Name: "srv",
				Count: "var.replicas",
				Attrs: map[string]model.AttrValue{"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(2)}},
			},
			{Type: "random_password", Name: "db",
				ForEach: `toset(["a", "b"])`,
				Attrs:   map[string]model.AttrValue{"length": {Mode: model.ModeLiteral, Literal: cty.NumberIntVal(16)}},
			},
		},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "count  = var.replicas") {
		t.Errorf("count not emitted as an expression:\n%s", s)
	}
	if !strings.Contains(s, `for_each = toset(["a", "b"])`) {
		t.Errorf("for_each not emitted as an expression:\n%s", s)
	}
	// count must come before the regular attributes.
	if strings.Index(s, "count") > strings.Index(s, "length = 2") {
		t.Errorf("count should lead the block:\n%s", s)
	}

	// Both set on one resource is an error.
	bad := &model.Project{Resources: []model.Resource{{Type: "random_pet", Name: "x", Count: "1", ForEach: "var.m"}}}
	if _, err := Generate(bad); err == nil {
		t.Error("expected error when both count and for_each are set")
	}
	// A malformed count expression is rejected.
	badExpr := &model.Project{Resources: []model.Resource{{Type: "random_pet", Name: "x", Count: "]] nope"}}}
	if _, err := Generate(badExpr); err == nil {
		t.Error("expected error for invalid count expression")
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

// required_version leads the terraform block when set (tflint's
// terraform_required_version rule wants it), and is absent when empty.
func TestGenerate_RequiredVersion(t *testing.T) {
	p := &model.Project{
		RequiredVersion: ">= 1.6.0",
		Backend:         &model.Backend{Type: "local"},
		Providers:       []model.ProviderInstance{{Key: "random", Source: "hashicorp/random"}},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `required_version = ">= 1.6.0"`) {
		t.Fatalf("required_version not emitted:\n%s", s)
	}
	if i := strings.Index(s, "required_version"); i > strings.Index(s, `backend "local"`) || i > strings.Index(s, "required_providers") {
		t.Errorf("required_version should lead the terraform block:\n%s", s)
	}
	// A schema fetch must not inherit the project's constraint: it runs on
	// whatever CLI the server has.
	pc, err := ProvidersConfig(p.Providers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(pc), "required_version") {
		t.Errorf("ProvidersConfig leaked required_version:\n%s", pc)
	}

	p.RequiredVersion = ""
	out, err = Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "required_version") {
		t.Errorf("empty constraint should omit the attribute:\n%s", out)
	}
}
