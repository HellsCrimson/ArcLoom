package hclgen

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// moved blocks land in their own file and emit both addresses as EXPRESSIONS:
// the whole point of a bulk move is that the target carries an instance key
// (`x.y["www"]`), which quoting would turn into a string the engine rejects.
func TestGenerateMoved(t *testing.T) {
	p := &model.Project{
		Moved: []model.Moved{
			{ID: "1", From: `null_resource.www`, To: `null_resource.sub["www"]`},
			{ID: "2", From: `null_resource.api`, To: `module.team["a"].null_resource.sub[0]`},
		},
	}
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := files["moved.tf"]
	if !ok {
		t.Fatalf("no moved.tf generated; files: %v", keys(files))
	}
	s := string(out)
	for _, want := range []string{
		`moved {`,
		`from = null_resource.www`,
		`to   = null_resource.sub["www"]`,
		`from = null_resource.api`,
		`to   = module.team["a"].null_resource.sub[0]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"null_resource.sub[\"www\"]"`) {
		t.Errorf("address emitted as a string literal:\n%s", s)
	}
	if _, diags := hclparse.NewParser().ParseHCL(out, "moved.tf"); diags.HasErrors() {
		t.Fatalf("generated HCL does not parse: %s\n%s", diags, s)
	}
	if strings.Contains(string(files["main.tf"]), "moved {") {
		t.Error("moved blocks leaked into main.tf")
	}
}

// No moved blocks, no file — an empty moved.tf would be noise in Git.
func TestGenerateMoved_NoneEmitsNoFile(t *testing.T) {
	files, err := GenerateFiles(&model.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["moved.tf"]; ok {
		t.Error("moved.tf generated for a project with no moved blocks")
	}
}

func TestGenerateMoved_RejectsBad(t *testing.T) {
	for _, mv := range []model.Moved{
		{From: "]] nope", To: "null_resource.a"},
		{From: "null_resource.a", To: "]] nope"},
		{From: "", To: "null_resource.a"},
		{From: "null_resource.a", To: ""},
	} {
		if _, err := GenerateMoved(&model.Project{Moved: []model.Moved{mv}}); err == nil {
			t.Errorf("expected an error for %+v", mv)
		}
	}
}

// ValidateAddress accepts what a `moved` block may hold and refuses what the
// engine would refuse anyway — an address is resolved before anything is
// evaluated, so a computed index or a bare expression is not an address.
func TestValidateAddress(t *testing.T) {
	ok := []string{
		`null_resource.www`,
		`null_resource.sub["www"]`,
		`null_resource.sub[0]`,
		`module.team.null_resource.a`,
		`module.team["alpha"].null_resource.a[2]`,
		`data.null_data_source.x`,
	}
	for _, a := range ok {
		if err := ValidateAddress(a); err != nil {
			t.Errorf("ValidateAddress(%q) = %v, want nil", a, err)
		}
	}
	bad := []string{
		``,
		`null_resource.sub[each.key]`,
		`null_resource.sub[var.i]`,
		`toset(["a"])`,
		`"null_resource.a"`,
		`]] nope`,
	}
	for _, a := range bad {
		if err := ValidateAddress(a); err == nil {
			t.Errorf("ValidateAddress(%q) = nil, want an error", a)
		}
	}
}
