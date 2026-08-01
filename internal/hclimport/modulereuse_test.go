package hclimport

import (
	"fmt"
	"testing"
)

// Several calls to ONE local module is the shape the call/definition split
// exists for, so every variant of it must keep every call.
func TestParse_TwoCallsSameSource(t *testing.T) {
	cases := map[string]string{
		"plain": `
module "team_a" { source = "./modules/team" }
module "team_b" { source = "./modules/team" }
`,
		"with inputs": `
module "team_a" {
  source = "./modules/team"
  name   = "a"
  size   = var.size
}
module "team_b" {
  source = "./modules/team"
  name   = "b"
  size   = 2
}
`,
		"with meta args": `
module "team_a" {
  source     = "./modules/team"
  count      = 2
  depends_on = [null_resource.gate]
}
module "team_b" {
  source   = "./modules/team"
  for_each = var.teams
  providers = {
    null = null.other
  }
}
`,
		"non canonical source": `
module "team_a" { source = "../modules/team" }
module "team_b" { source = "./modules/team/" }
`,
		"bare folder source": `
module "team_a" { source = "./team" }
module "team_b" { source = "./team" }
`,
		"three calls": `
module "team_a" { source = "./modules/team" }
module "team_b" { source = "./modules/team" }
module "team_c" { source = "./modules/team" }
`,
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			n := 0
			res, err := Parse(map[string][]byte{"main.tf": []byte(src)}, func() string {
				n++
				return fmt.Sprintf("id%d", n)
			})
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if name == "three calls" {
				want = 3
			}
			if len(res.Modules) != want {
				t.Fatalf("want %d calls, got %d: %+v", want, len(res.Modules), res.Modules)
			}
			seen := map[string]bool{}
			for _, m := range res.Modules {
				if seen[m.Name] {
					t.Errorf("duplicate call name %q", m.Name)
				}
				seen[m.Name] = true
			}
			if len(res.LocalModules) != 1 {
				t.Errorf("want 1 definition for the shared source, got %d: %+v", len(res.LocalModules), res.LocalModules)
			}
		})
	}
}

// The definition's own files plus two calls, in one import.
func TestParse_TwoCallsWithDefinitionFiles(t *testing.T) {
	files := map[string][]byte{
		"main.tf": []byte(`
module "team_a" {
  source = "./modules/team"
  name   = "a"
}
module "team_b" {
  source = "./modules/team"
  name   = "b"
}
`),
		"modules/team/main.tf": []byte(`
variable "name" { type = string }
resource "null_resource" "x" {}
output "id" { value = null_resource.x.id }
`),
	}
	res, err := Parse(files, func() string { return "id" })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != 2 {
		t.Fatalf("want 2 calls, got %d: %+v", len(res.Modules), res.Modules)
	}
	if len(res.LocalModules) != 1 {
		t.Fatalf("want 1 definition, got %d", len(res.LocalModules))
	}
	lm := res.LocalModules[0]
	if len(lm.Variables) != 1 || len(lm.Resources) != 1 || len(lm.Outputs) != 1 {
		t.Errorf("definition contents lost: vars=%d res=%d out=%d", len(lm.Variables), len(lm.Resources), len(lm.Outputs))
	}
	// Each call keeps its own arguments.
	for _, m := range res.Modules {
		if len(m.Config) != 1 {
			t.Errorf("call %q lost its arguments: %+v", m.Name, m.Config)
		}
	}
}
