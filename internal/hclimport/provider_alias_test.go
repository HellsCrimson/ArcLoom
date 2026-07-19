package hclimport

import (
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func findInstance(t *testing.T, provs []model.ProviderInstance, key, alias string) model.ProviderInstance {
	t.Helper()
	for _, p := range provs {
		if p.Key == key && p.Alias == alias {
			return p
		}
	}
	t.Fatalf("provider instance %s(alias=%q) not found in %+v", key, alias, provs)
	return model.ProviderInstance{}
}

func TestParse_ProviderAlias(t *testing.T) {
	src := `
terraform {
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

provider "aws" {
  region = "us-east-1"
}

provider "aws" {
  alias  = "euwest"
  region = "eu-west-1"
}

resource "aws_instance" "eu" {
  provider = aws.euwest
  ami      = "ami-123"
}

data "aws_ami" "latest" {
  provider = aws.euwest
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(res.Providers) != 2 {
		t.Fatalf("providers = %+v, want default + aliased instance", res.Providers)
	}
	def := findInstance(t, res.Providers, "aws", "")
	ali := findInstance(t, res.Providers, "aws", "euwest")
	// Both instances share the required_providers source/version.
	for _, pi := range []model.ProviderInstance{def, ali} {
		if pi.Source != "hashicorp/aws" || pi.Version != "~> 5.0" {
			t.Errorf("instance %+v missing source/version from required_providers", pi)
		}
	}
	// alias is a meta-arg, never config.
	if _, ok := ali.Config["alias"]; ok {
		t.Errorf("alias leaked into config: %+v", ali.Config)
	}
	if av, ok := ali.Config["region"]; !ok || av.Literal.AsString() != "eu-west-1" {
		t.Errorf("aliased instance config wrong: %+v", ali.Config)
	}

	if len(res.Resources) != 1 || res.Resources[0].Provider != "aws.euwest" {
		t.Fatalf("resource provider meta-arg not lifted: %+v", res.Resources)
	}
	if _, ok := res.Resources[0].Attrs["provider"]; ok {
		t.Errorf("provider leaked into resource attrs: %+v", res.Resources[0].Attrs)
	}
	if len(res.DataSources) != 1 || res.DataSources[0].Provider != "aws.euwest" {
		t.Fatalf("data source provider meta-arg not lifted: %+v", res.DataSources)
	}
}

// A provider meta-arg referencing an alias with no matching provider block
// still registers the instance, so generation emits a valid config.
func TestParse_AliasRefWithoutBlock(t *testing.T) {
	src := `
resource "aws_instance" "eu" {
  provider = aws.euwest
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	findInstance(t, res.Providers, "aws", "")
	findInstance(t, res.Providers, "aws", "euwest")
}
