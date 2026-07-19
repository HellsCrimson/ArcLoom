package hclgen

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func TestGenerate_ProviderAlias(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{
			{Key: "aws", Source: "hashicorp/aws", Version: "~> 5.0", Config: map[string]model.AttrValue{
				"region": {Mode: model.ModeLiteral, Literal: cty.StringVal("us-east-1")},
			}},
			{Key: "aws", Alias: "euwest", Source: "hashicorp/aws", Config: map[string]model.AttrValue{
				"region": {Mode: model.ModeLiteral, Literal: cty.StringVal("eu-west-1")},
			}},
		},
		Resources: []model.Resource{
			{Type: "aws_instance", Name: "eu", Provider: "aws.euwest", Attrs: map[string]model.AttrValue{
				"ami": {Mode: model.ModeLiteral, Literal: cty.StringVal("ami-123")},
			}},
			{Type: "aws_instance", Name: "us", Attrs: map[string]model.AttrValue{
				"ami": {Mode: model.ModeLiteral, Literal: cty.StringVal("ami-456")},
			}},
		},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	s := string(out)

	for _, w := range []string{`"euwest"`, `"eu-west-1"`, "provider = aws.euwest"} {
		if !strings.Contains(s, w) {
			t.Errorf("generated HCL missing %q\n---\n%s", w, s)
		}
	}
	// Both instances emit a provider block, but required_providers holds one
	// entry per key.
	if n := strings.Count(s, `provider "aws"`); n != 2 {
		t.Errorf("provider blocks = %d, want 2\n%s", n, s)
	}
	if n := strings.Count(s, "hashicorp/aws"); n != 1 {
		t.Errorf("required_providers entries = %d, want 1\n%s", n, s)
	}
	// The provider meta-arg is a traversal, not a quoted string.
	if strings.Contains(s, `provider = "aws.euwest"`) {
		t.Errorf("provider meta-arg emitted as string literal:\n%s", s)
	}
	// The default-instance resource has no provider meta-arg.
	if n := strings.Count(s, "provider = aws."); n != 1 {
		t.Errorf("provider meta-args = %d, want 1\n%s", n, s)
	}
}

// The required_providers entry picks up a version pinned on any instance of
// the key, even when the default instance leaves it empty.
func TestGenerate_AliasVersionFallback(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{
			{Key: "aws", Source: "hashicorp/aws"},
			{Key: "aws", Alias: "euwest", Source: "hashicorp/aws", Version: "~> 5.0"},
		},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(string(out), `version = "~> 5.0"`) {
		t.Errorf("version from aliased instance not used:\n%s", out)
	}
}

func TestGenerate_InvalidProviderRef(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{{Key: "aws", Source: "hashicorp/aws"}},
		Resources: []model.Resource{
			{Type: "aws_instance", Name: "bad", Provider: "aws..nope"},
		},
	}
	if _, err := Generate(p); err == nil {
		t.Fatal("expected error for invalid provider reference")
	}
}
