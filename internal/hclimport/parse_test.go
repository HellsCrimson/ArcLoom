package hclimport

import (
	"testing"

	"github.com/matthiasrauline/arcloom/internal/model"
)

func ids() func() string {
	n := 0
	return func() string { n++; return "id-" + string(rune('a'+n)) }
}

func TestParse_ResourceProviderAndModes(t *testing.T) {
	src := `
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }
}

provider "random" {}

resource "random_pet" "srv" {
  length = 3
}

resource "random_password" "db" {
  length  = 16
  special = true
  keepers = random_pet.srv.keepers
  override_special = "prefix-${random_pet.srv.id}"
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(res.Resources))
	}

	// Provider source picked up from required_providers.
	if len(res.Providers) != 1 || res.Providers[0].Source != "hashicorp/random" {
		t.Fatalf("provider not imported correctly: %+v", res.Providers)
	}
	if res.Providers[0].Version != "~> 3.0" {
		t.Errorf("version not imported: %q", res.Providers[0].Version)
	}

	var db model.Resource
	for _, r := range res.Resources {
		if r.Name == "db" {
			db = r
		}
	}
	// Literal number.
	if v := db.Attrs["length"]; v.Mode != model.ModeLiteral {
		t.Errorf("length should be literal, got %s", v.Mode)
	}
	// Literal bool.
	if v := db.Attrs["special"]; v.Mode != model.ModeLiteral {
		t.Errorf("special should be literal, got %s", v.Mode)
	}
	// Reference.
	if v := db.Attrs["keepers"]; v.Mode != model.ModeReference || v.Ref != "random_pet.srv.keepers" {
		t.Errorf("keepers should be reference random_pet.srv.keepers, got %s %q", v.Mode, v.Ref)
	}
	// Interpolated string -> expression (raw preserved).
	if v := db.Attrs["override_special"]; v.Mode != model.ModeExpression {
		t.Errorf("override_special should be expression, got %s (%q)", v.Mode, v.Expr)
	}
}

func TestParse_VariablesLocalsOutputsDataModule(t *testing.T) {
	src := `
variable "region" {
  type        = string
  default     = "eu-west-1"
  description = "AWS region"
  sensitive   = false
}

locals {
  prefix = "prod"
  tags   = { env = "prod" }
}

data "aws_ami" "ubuntu" {
  most_recent = true
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
  cidr    = "10.0.0.0/16"
}

output "region_out" {
  value       = var.region
  description = "the region"
  sensitive   = true
}
`
	res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Variables) != 1 || res.Variables[0].Name != "region" {
		t.Fatalf("variables: %+v", res.Variables)
	}
	v := res.Variables[0]
	if v.Type != "string" {
		t.Errorf("variable type: %q", v.Type)
	}
	if v.Description != "AWS region" || v.Sensitive {
		t.Errorf("variable meta: %+v", v)
	}
	if v.Default == nil || v.Default.Literal.AsString() != "eu-west-1" {
		t.Errorf("variable default: %+v", v.Default)
	}
	if len(res.Locals) != 2 {
		t.Errorf("locals: %+v", res.Locals)
	}
	if len(res.DataSources) != 1 || res.DataSources[0].Type != "aws_ami" {
		t.Errorf("data sources: %+v", res.DataSources)
	}
	if len(res.Modules) != 1 || res.Modules[0].Source != "terraform-aws-modules/vpc/aws" || res.Modules[0].Version != "5.0.0" {
		t.Errorf("modules: %+v", res.Modules)
	}
	if len(res.Outputs) != 1 || res.Outputs[0].Name != "region_out" || !res.Outputs[0].Sensitive {
		t.Errorf("outputs: %+v", res.Outputs)
	}
	// Output value references a variable -> reference mode.
	if res.Outputs[0].Value.Mode != model.ModeReference || res.Outputs[0].Value.Ref != "var.region" {
		t.Errorf("output value: %+v", res.Outputs[0].Value)
	}
}

func TestParse_NestedBlocks(t *testing.T) {
	src := `
resource "example_thing" "x" {
  name = "hi"
  rule {
    action = "allow"
  }
  rule {
    action = "deny"
  }
}
`
	res, err := Parse(map[string][]byte{"a.tf": []byte(src)}, ids())
	if err != nil {
		t.Fatal(err)
	}
	r := res.Resources[0]
	if len(r.Blocks["rule"]) != 2 {
		t.Fatalf("expected 2 rule blocks, got %d", len(r.Blocks["rule"]))
	}
	if r.Blocks["rule"][0].Attrs["action"].Literal.AsString() != "allow" {
		t.Errorf("first rule action wrong: %+v", r.Blocks["rule"][0].Attrs["action"])
	}
}
