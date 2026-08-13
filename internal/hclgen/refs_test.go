package hclgen

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func expr(s string) model.AttrValue {
	return model.AttrValue{Mode: model.ModeExpression, Expr: s}
}

// refStrings flattens the findings for comparison.
func refStrings(refs []UndeclaredRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Scope+"|"+r.Ref)
	}
	return out
}

func hasRef(t *testing.T, refs []UndeclaredRef, want string) {
	t.Helper()
	for _, r := range refs {
		if r.Scope+"|"+r.Ref == want {
			return
		}
	}
	t.Fatalf("missing %q in %v", want, refStrings(refs))
}

func noRef(t *testing.T, refs []UndeclaredRef, unwanted string) {
	t.Helper()
	for _, r := range refs {
		if r.Scope+"|"+r.Ref == unwanted {
			t.Fatalf("unexpected %q (%s)", unwanted, r.String())
		}
	}
}

func TestUndeclaredRefsFindsDanglingVariable(t *testing.T) {
	p := &model.Project{
		Locals: []model.Local{{Name: "sync", Value: expr("var.zzz_probe_only")}},
	}
	refs := UndeclaredRefs(p)
	hasRef(t, refs, "|var.zzz_probe_only")
	if got := refs[0].String(); !strings.Contains(got, `local "sync"`) || !strings.Contains(got, "var.zzz_probe_only") {
		t.Fatalf("unhelpful message: %q", got)
	}
}

func TestUndeclaredRefsAcceptsDeclared(t *testing.T) {
	p := &model.Project{
		Variables: []model.Variable{{Name: "region"}},
		Locals: []model.Local{
			{Name: "a", Value: expr("var.region")},
			{Name: "b", Value: expr("local.a")},
		},
	}
	if refs := UndeclaredRefs(p); len(refs) != 0 {
		t.Fatalf("expected none, got %v", refStrings(refs))
	}
}

// A secret auto-declares `variable "<name>"`, so naming it is legitimate.
func TestUndeclaredRefsIgnoresSecretVariables(t *testing.T) {
	ref := "global_projectA_token"
	p := &model.Project{
		Locals: []model.Local{
			{Name: "creds", Value: model.AttrValue{Mode: model.ModeSecret, SecretRef: ref}},
			{Name: "again", Value: expr("var." + model.SecretVarName(ref))},
		},
	}
	noRef(t, UndeclaredRefs(p), "|var."+model.SecretVarName(ref))
}

// Resource/data/module addresses are deliberately not checked.
func TestUndeclaredRefsChecksOnlyVarAndLocal(t *testing.T) {
	p := &model.Project{
		Resources: []model.Resource{{
			Type: "null_resource", Name: "web",
			Attrs:   map[string]model.AttrValue{"triggers": expr("aws_instance.gone.id")},
			ForEach: expr("module.absent.names").Expr,
		}},
	}
	if refs := UndeclaredRefs(p); len(refs) != 0 {
		t.Fatalf("expected none, got %v", refStrings(refs))
	}
}

// The reference lives inside a map entry, several levels down.
func TestUndeclaredRefsWalksNestedValues(t *testing.T) {
	p := &model.Project{
		Resources: []model.Resource{{
			Type: "null_resource", Name: "web",
			Attrs: map[string]model.AttrValue{
				"env": {Mode: model.ModeLiteral, Entries: []model.AttrEntry{
					{Key: "outer", Value: model.AttrValue{Mode: model.ModeLiteral, Entries: []model.AttrEntry{
						{Key: "inner", Value: expr("var.deep")},
					}}},
				}},
			},
		}},
	}
	hasRef(t, UndeclaredRefs(p), "|var.deep")
}

// A reference-mode value is an authored traversal too.
func TestUndeclaredRefsChecksReferenceMode(t *testing.T) {
	p := &model.Project{
		Outputs: []model.Output{{Name: "o", Value: model.AttrValue{Mode: model.ModeReference, Ref: "var.missing"}}},
	}
	hasRef(t, UndeclaredRefs(p), "|var.missing")
}

// Meta-args are raw expressions and the likeliest place a refactor puts one.
func TestUndeclaredRefsChecksMetaArgs(t *testing.T) {
	p := &model.Project{
		Resources: []model.Resource{{Type: "null_resource", Name: "a", ForEach: "local.projects"}},
		Modules:   []model.Module{{Name: "team", Source: "./modules/team", Count: "var.replicas"}},
	}
	refs := UndeclaredRefs(p)
	hasRef(t, refs, "|local.projects")
	hasRef(t, refs, "|var.replicas")
}

// A local module resolves var./local. against its own declarations; the root's
// are not in scope, and a module's own declarations satisfy it.
func TestUndeclaredRefsScopesToLocalModule(t *testing.T) {
	p := &model.Project{
		Variables: []model.Variable{{Name: "region"}},
		LocalModules: []model.LocalModule{{
			Name:      "team",
			Variables: []model.Variable{{Name: "name"}},
			Resources: []model.Resource{{
				Type: "null_resource", Name: "a",
				Attrs: map[string]model.AttrValue{
					"ok":      expr("var.name"),
					"dangles": expr("var.region"),
				},
			}},
		}},
	}
	refs := UndeclaredRefs(p)
	hasRef(t, refs, "team|var.region")
	noRef(t, refs, "team|var.name")
}

// Unparseable text is not an expression; reporting it would be noise.
func TestUndeclaredRefsIgnoresUnparseable(t *testing.T) {
	p := &model.Project{Locals: []model.Local{{Name: "l", Value: expr("var.")}}}
	if refs := UndeclaredRefs(p); len(refs) != 0 {
		t.Fatalf("expected none, got %v", refStrings(refs))
	}
}

// A literal never references anything.
func TestUndeclaredRefsIgnoresLiterals(t *testing.T) {
	p := &model.Project{
		Locals: []model.Local{{Name: "l", Value: model.AttrValue{Mode: model.ModeLiteral, Literal: cty.StringVal("var.notaref")}}},
	}
	if refs := UndeclaredRefs(p); len(refs) != 0 {
		t.Fatalf("expected none, got %v", refStrings(refs))
	}
}
