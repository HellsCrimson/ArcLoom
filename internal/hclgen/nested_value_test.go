package hclgen

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// TestGenerate_NestedListWithReference verifies a list literal whose elements
// mix a plain literal, a reference, and a secret is emitted as a tuple
// expression (not a cty value), so references/secrets can live inside a list.
func TestGenerate_NestedListWithReference(t *testing.T) {
	p := &model.Project{
		Name:   "demo",
		Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "aws", Source: "hashicorp/aws"},
		},
		Resources: []model.Resource{
			{
				Type: "aws_security_group", Name: "sg",
				Attrs: map[string]model.AttrValue{
					"cidrs": {
						Mode: model.ModeLiteral,
						Items: []model.AttrValue{
							{Mode: model.ModeLiteral, Literal: cty.StringVal("10.0.0.0/8")},
							{Mode: model.ModeReference, Ref: "aws_vpc.main.cidr_block"},
							{Mode: model.ModeSecret, SecretRef: "extra-cidr"},
						},
					},
				},
			},
		},
	}

	out, err := Generate(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(out)
	// The list must contain the literal string, the bare traversal, and the
	// secret's var reference — all inside one bracketed expression.
	for _, want := range []string{
		`"10.0.0.0/8"`,
		`aws_vpc.main.cidr_block`,
		`var.extra_cidr`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "cidrs = [") {
		t.Errorf("expected a tuple expression for cidrs; got:\n%s", got)
	}
	// The reference must NOT be quoted (it would be a string, not a traversal).
	if strings.Contains(got, `"aws_vpc.main.cidr_block"`) {
		t.Errorf("reference was emitted as a string literal:\n%s", got)
	}
	// The secret var must be auto-declared as a sensitive variable.
	if !strings.Contains(got, `variable "extra_cidr"`) {
		t.Errorf("expected auto-declared secret variable; got:\n%s", got)
	}
}

// TestGenerate_NestedObjectWithReference verifies a map/object literal whose
// values mix literals and references is emitted as an object constructor with
// deterministic (sorted) key order.
func TestGenerate_NestedObjectWithReference(t *testing.T) {
	p := &model.Project{
		Name:   "demo",
		Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "aws", Source: "hashicorp/aws"},
		},
		Resources: []model.Resource{
			{
				Type: "aws_instance", Name: "web",
				Attrs: map[string]model.AttrValue{
					"tags": {
						Mode: model.ModeLiteral,
						Entries: []model.AttrEntry{
							{Key: "Zone", Value: model.AttrValue{Mode: model.ModeReference, Ref: "aws_vpc.main.id"}},
							{Key: "Name", Value: model.AttrValue{Mode: model.ModeLiteral, Literal: cty.StringVal("web")}},
						},
					},
				},
			},
		},
	}

	out, err := Generate(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"Name" = "web"`) {
		t.Errorf("expected literal object entry; got:\n%s", got)
	}
	if !strings.Contains(got, `"Zone" = aws_vpc.main.id`) {
		t.Errorf("expected reference object entry; got:\n%s", got)
	}
	// Keys are sorted: Name before Zone.
	if strings.Index(got, `"Name"`) > strings.Index(got, `"Zone"`) {
		t.Errorf("object keys not sorted deterministically:\n%s", got)
	}
}
