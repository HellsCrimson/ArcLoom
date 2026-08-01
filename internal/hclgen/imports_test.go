package hclgen

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// import blocks land in their own file, keep for_each first, and emit `to`/`id`
// as EXPRESSIONS — `to` carries an index under a for_each and `id` is usually
// each.value, so quoting either would produce a config the engine rejects.
func TestGenerateImports(t *testing.T) {
	p := &model.Project{
		Imports: []model.Import{
			{ID: "1", To: `null_resource.one`, ImportID: `"abc123"`},
			{
				ID: "2", ForEach: `toset(local.subdomains)`,
				To: `null_resource.sub[each.key]`, ImportID: `each.value`,
				Provider: "null.other",
			},
		},
	}
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := files["imports.tf"]
	if !ok {
		t.Fatalf("no imports.tf generated; files: %v", keys(files))
	}
	s := string(out)
	for _, want := range []string{
		`import {`,
		`to = null_resource.one`,
		`id = "abc123"`,
		`for_each = toset(local.subdomains)`,
		`to       = null_resource.sub[each.key]`,
		`id       = each.value`,
		`provider = null.other`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	// The address must not be quoted into a string.
	if strings.Contains(s, `"null_resource.sub[each.key]"`) {
		t.Errorf("`to` emitted as a string literal:\n%s", s)
	}
	// for_each leads its block, like on resources and modules.
	if strings.Index(s, "for_each") > strings.Index(s, "to       =") {
		t.Errorf("for_each should lead the block:\n%s", s)
	}
	if _, diags := hclparse.NewParser().ParseHCL(out, "imports.tf"); diags.HasErrors() {
		t.Fatalf("generated HCL does not parse: %s\n%s", diags, s)
	}
	// main.tf must not carry them.
	if strings.Contains(string(files["main.tf"]), "import {") {
		t.Error("import blocks leaked into main.tf")
	}
}

// No import blocks, no file — an empty imports.tf would be noise in Git.
func TestGenerateImports_NoneEmitsNoFile(t *testing.T) {
	files, err := GenerateFiles(&model.Project{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["imports.tf"]; ok {
		t.Error("imports.tf generated for a project with no import blocks")
	}
}

// A malformed expression fails at generation (and therefore at the API, which
// validates with ValidateExpr) rather than at plan time.
func TestGenerateImports_RejectsBadExpression(t *testing.T) {
	for _, imp := range []model.Import{
		{To: "]] nope", ImportID: `"x"`},
		{To: "null_resource.a", ImportID: "]] nope"},
		{To: "null_resource.a", ImportID: `"x"`, ForEach: "]] nope"},
		{To: "", ImportID: `"x"`},
		{To: "null_resource.a", ImportID: ""},
	} {
		if _, err := GenerateImports(&model.Project{Imports: []model.Import{imp}}); err == nil {
			t.Errorf("expected an error for %+v", imp)
		}
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
