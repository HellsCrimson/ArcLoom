package hclgen

import (
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func fileNames(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The generated config is split by THEME. Each block kind must land in exactly
// one file — a resource leaking into variables.tf (or vice versa) would still
// be valid Terraform, which is what makes this worth asserting rather than
// eyeballing.
func TestGenerateRootFiles_SplitsByTheme(t *testing.T) {
	p := &model.Project{
		RequiredVersion: ">= 1.6.0",
		Providers:       []model.ProviderInstance{{Key: "null", Source: "hashicorp/null"}},
		Variables:       []model.Variable{{Name: "names", Type: "list(string)"}},
		Locals:          []model.Local{{Name: "env", Value: model.AttrValue{Mode: model.ModeLiteral, Literal: cty.StringVal("dev")}}},
		DataSources:     []model.Resource{{ID: "d1", Type: "null_data_source", Name: "d"}},
		Resources:       []model.Resource{{ID: "r1", Type: "null_resource", Name: "web"}},
		Modules:         []model.Module{{Name: "team", Source: "./modules/team"}},
		LocalModules:    []model.LocalModule{{Name: "team", Resources: []model.Resource{{ID: "r2", Type: "null_resource", Name: "inner"}}}},
		Outputs:         []model.Output{{Name: "id", Value: model.AttrValue{Mode: model.ModeReference, Ref: "null_resource.web.id"}}},
		Imports:         []model.Import{{ID: "i1", To: "null_resource.web", ImportID: `"abc"`}},
		Moved:           []model.Moved{{ID: "m1", From: "null_resource.old", To: "null_resource.web"}},
	}
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatal(err)
	}

	// What belongs where, and what must NOT appear there.
	for _, tc := range []struct {
		file      string
		want      []string
		forbidden []string
	}{
		{"terraform.tf", []string{"terraform {", `required_version = ">= 1.6.0"`, `provider "null"`}, []string{"resource ", "variable "}},
		{"variables.tf", []string{`variable "names"`}, []string{"resource ", "terraform {"}},
		{"locals.tf", []string{"locals {", "env"}, []string{"resource ", "variable "}},
		{"data.tf", []string{`data "null_data_source" "d"`}, []string{`resource "null_resource"`}},
		{"main.tf", []string{`resource "null_resource" "web"`, `module "team"`}, []string{"variable ", "terraform {", "output ", `data "`}},
		{"outputs.tf", []string{`output "id"`}, []string{"resource ", "variable "}},
		{"imports.tf", []string{"import {"}, []string{"resource "}},
		{"moved.tf", []string{"moved {"}, []string{"resource "}},
		{"modules/team/main.tf", []string{`resource "null_resource" "inner"`}, []string{"terraform {", "provider "}},
	} {
		content, ok := files[tc.file]
		if !ok {
			t.Errorf("missing %s; got %v", tc.file, fileNames(files))
			continue
		}
		s := string(content)
		for _, w := range tc.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q in:\n%s", tc.file, w, s)
			}
		}
		for _, f := range tc.forbidden {
			if strings.Contains(s, f) {
				t.Errorf("%s: %q leaked into it:\n%s", tc.file, f, s)
			}
		}
		if _, diags := hclparse.NewParser().ParseHCL(content, tc.file); diags.HasErrors() {
			t.Errorf("%s does not parse: %s", tc.file, diags)
		}
	}
}

// A theme with nothing in it emits NO file — the rule imports.tf already
// followed. Without it every project would grow six near-empty tabs.
func TestGenerateRootFiles_OmitsEmptySections(t *testing.T) {
	p := &model.Project{
		Providers: []model.ProviderInstance{{Key: "null", Source: "hashicorp/null"}},
		Resources: []model.Resource{{ID: "r1", Type: "null_resource", Name: "web"}},
	}
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatal(err)
	}
	got := fileNames(files)
	want := []string{"main.tf", "terraform.tf"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}

	// An empty project keeps only the terraform block: nothing else has content,
	// and main.tf is not stubbed at the root.
	empty, err := GenerateFiles(&model.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if got := fileNames(empty); strings.Join(got, ",") != "terraform.tf" {
		t.Errorf("empty project generated %v", got)
	}
}

// A module with NOTHING in it still generates its folder: `init` fails with
// "module not found" if a call points at a directory that does not exist.
func TestGenerateModuleFiles_EmptyModuleStillMakesItsFolder(t *testing.T) {
	files, err := GenerateFiles(&model.Project{
		Modules:      []model.Module{{Name: "team", Source: "./modules/team"}},
		LocalModules: []model.LocalModule{{Name: "team"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["modules/team/main.tf"]; !ok {
		t.Errorf("no folder for an empty module: %v", fileNames(files))
	}
}

// The single-document form still renders everything, in the conventional order:
// it is what ProvidersConfig-style callers and the tests below assert on, and
// splitting the files must not change it.
func TestGenerate_StillOneDocumentInOrder(t *testing.T) {
	p := &model.Project{
		Providers:   []model.ProviderInstance{{Key: "null", Source: "hashicorp/null"}},
		Variables:   []model.Variable{{Name: "a", Type: "string"}},
		Locals:      []model.Local{{Name: "b", Value: model.AttrValue{Mode: model.ModeLiteral, Literal: cty.StringVal("x")}}},
		DataSources: []model.Resource{{ID: "d1", Type: "null_data_source", Name: "d"}},
		Resources:   []model.Resource{{ID: "r1", Type: "null_resource", Name: "web"}},
		Outputs:     []model.Output{{Name: "o", Value: model.AttrValue{Mode: model.ModeReference, Ref: "null_resource.web.id"}}},
	}
	out, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	order := []string{"terraform {", `provider "null"`, `variable "a"`, "locals {", `data "null_data_source"`, `resource "null_resource"`, `output "o"`}
	at := -1
	for _, want := range order {
		i := strings.Index(s, want)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
		if i < at {
			t.Errorf("%q is out of order in:\n%s", want, s)
		}
		at = i
	}
	// No leading blank line, and no run of blank lines between blocks.
	if strings.HasPrefix(s, "\n") || strings.Contains(s, "\n\n\n") {
		t.Errorf("spacing changed:\n%q", s)
	}
}
