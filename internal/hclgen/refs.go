package hclgen

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Reference checking for AUTHORED expressions.
//
// A `variable` block is written for two reasons and two only: the user declared
// one, or a secret reference made hclgen auto-declare one (writeVariablesSection
// + CollectSecretRefs). Nothing derives a declaration from an expression — so
// `local.x = var.region` typed into an expression box generates config naming a
// variable that no file declares, and the first thing to notice is the engine,
// at plan time, long after the write that caused it.
//
// This is a WARNING, not a validation: writes have no required order, so a
// local that references a variable declared by the next call is a legitimate
// intermediate state, and refusing it would break every declarations-last
// caller including the UI's own. UndeclaredRefs therefore reports; the caller
// decides what to do with it.

// UndeclaredRef is one `var.` / `local.` reference an authored expression makes
// that its own scope does not declare.
type UndeclaredRef struct {
	// Scope is "" for the root module, else the local module's name. A module
	// has its own variables and locals, so the same reference can be fine in
	// one scope and dangling in another.
	Scope string `json:"scope,omitempty"`
	// Where names the block the expression sits in, e.g. `local "subnets"`.
	Where string `json:"where"`
	// Ref is the reference as written: "var.region", "local.subnets".
	Ref string `json:"ref"`
}

// String renders the warning as one line.
func (u UndeclaredRef) String() string {
	scope := ""
	if u.Scope != "" {
		scope = " in module " + u.Scope
	}
	return fmt.Sprintf("%s%s references %s, which nothing declares", u.Where, scope, u.Ref)
}

// UndeclaredRefs walks every authored expression in the project — expression
// and reference values, resource and module meta-args, import blocks — and
// returns the references whose scope declares no matching variable or local.
//
// Only `var.` and `local.` are checked. Resource, data and module references
// are deliberately left alone: a false positive is worse than a miss here, and
// those addresses have enough shapes (aliases, module outputs, `each.value`)
// that a cheap check would cry wolf.
func UndeclaredRefs(p *model.Project) []UndeclaredRef {
	if p == nil {
		return nil
	}
	var out []UndeclaredRef

	root := newRefScope("", p.Variables, p.Locals, CollectSecretRefs(p))
	for _, prov := range p.Providers {
		name := prov.Key
		if prov.Alias != "" {
			name += "." + prov.Alias
		}
		root.attrs(fmt.Sprintf("provider %q", name), prov.Config)
	}
	for _, v := range p.Variables {
		if v.Default != nil {
			// Terraform forbids a default from referring to any other value, so
			// anything found here is dangling by definition.
			root.value(fmt.Sprintf("variable %q default", v.Name), *v.Default)
		}
	}
	for _, l := range p.Locals {
		root.value(fmt.Sprintf("local %q", l.Name), l.Value)
	}
	for _, o := range p.Outputs {
		root.value(fmt.Sprintf("output %q", o.Name), o.Value)
	}
	for _, m := range p.Modules {
		where := fmt.Sprintf("module %q", m.Name)
		root.attrs(where, m.Config)
		root.expr(where+" count", m.Count)
		root.expr(where+" for_each", m.ForEach)
	}
	for _, r := range p.DataSources {
		root.resource("data", r)
	}
	for _, r := range p.Resources {
		root.resource("resource", r)
	}
	for i, imp := range p.Imports {
		where := fmt.Sprintf("import block %d", i+1)
		root.expr(where+" to", imp.To)
		root.expr(where+" id", imp.ImportID)
		root.expr(where+" for_each", imp.ForEach)
	}
	out = append(out, root.out...)

	// A local module resolves var./local. against its OWN declarations; the
	// root's are not in scope. Secret refs are collected per module for the
	// same reason.
	for _, lm := range p.LocalModules {
		sc := newRefScope(lm.Name, lm.Variables, lm.Locals, moduleSecretRefs(lm))
		for _, v := range lm.Variables {
			if v.Default != nil {
				sc.value(fmt.Sprintf("variable %q default", v.Name), *v.Default)
			}
		}
		for _, l := range lm.Locals {
			sc.value(fmt.Sprintf("local %q", l.Name), l.Value)
		}
		for _, o := range lm.Outputs {
			sc.value(fmt.Sprintf("output %q", o.Name), o.Value)
		}
		for _, r := range lm.DataSources {
			sc.resource("data", r)
		}
		for _, r := range lm.Resources {
			sc.resource("resource", r)
		}
		out = append(out, sc.out...)
	}
	return out
}

// moduleSecretRefs collects the secret variable names a local module's own
// values would auto-declare, so referencing one by name is not reported.
func moduleSecretRefs(lm model.LocalModule) []string {
	set := map[string]bool{}
	visit := func(av model.AttrValue) {
		av.Walk(func(n model.AttrValue) {
			if n.Mode == model.ModeSecret && n.SecretRef != "" {
				set[n.SecretRef] = true
			}
		})
	}
	for _, v := range lm.Variables {
		if v.Default != nil {
			visit(*v.Default)
		}
	}
	for _, l := range lm.Locals {
		visit(l.Value)
	}
	for _, o := range lm.Outputs {
		visit(o.Value)
	}
	for _, r := range append(append([]model.Resource{}, lm.DataSources...), lm.Resources...) {
		visitResource(r, visit)
	}
	out := make([]string, 0, len(set))
	for ref := range set {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// refScope holds one scope's declarations and the findings against it.
type refScope struct {
	scope  string
	vars   map[string]bool
	locals map[string]bool
	seen   map[string]bool
	out    []UndeclaredRef
}

func newRefScope(scope string, vars []model.Variable, locals []model.Local, secretRefs []string) *refScope {
	s := &refScope{
		scope:  scope,
		vars:   map[string]bool{},
		locals: map[string]bool{},
		seen:   map[string]bool{},
	}
	for _, v := range vars {
		s.vars[v.Name] = true
	}
	for _, l := range locals {
		s.locals[l.Name] = true
	}
	// A secret reference auto-declares `variable "<SecretVarName>"`, so naming
	// one from an expression is legitimate and must not be reported.
	for _, ref := range secretRefs {
		s.vars[model.SecretVarName(ref)] = true
	}
	return s
}

// resource scans one resource or data source, including its nested blocks.
func (s *refScope) resource(keyword string, r model.Resource) {
	where := fmt.Sprintf("%s %s.%s", keyword, r.Type, r.Name)
	s.attrs(where, r.Attrs)
	s.expr(where+" count", r.Count)
	s.expr(where+" for_each", r.ForEach)
	for blockType, blocks := range r.Blocks {
		for _, nb := range blocks {
			s.attrs(fmt.Sprintf("%s %s block", where, blockType), nb.Attrs)
			for inner, nested := range nb.Blocks {
				for _, n := range nested {
					s.attrs(fmt.Sprintf("%s %s.%s block", where, blockType, inner), n.Attrs)
				}
			}
		}
	}
}

func (s *refScope) attrs(where string, attrs map[string]model.AttrValue) {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic warning order out of a map
	for _, name := range names {
		s.value(fmt.Sprintf("%s attribute %q", where, name), attrs[name])
	}
}

// value walks a value's structure so a reference nested inside a list or map is
// checked too — the same recursion CollectSecretRefs relies on.
func (s *refScope) value(where string, av model.AttrValue) {
	av.Walk(func(n model.AttrValue) {
		switch n.Mode {
		case model.ModeExpression:
			s.expr(where, n.Expr)
		case model.ModeReference:
			s.expr(where, n.Ref)
		}
	})
}

// expr parses one raw HCL expression and records its dangling references.
//
// A parse failure is ignored rather than reported: writes are already gated by
// ValidateExpr, so unparseable text here means the value is not an expression
// at all (a bare reference string, say), and guessing at it would produce noise.
func (s *refScope) expr(where, text string) {
	if text == "" {
		return
	}
	expr, diags := hclsyntax.ParseExpression([]byte(text), "expr.hcl", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return
	}
	for _, t := range expr.Variables() {
		root := t.RootName()
		if root != "var" && root != "local" {
			continue
		}
		if len(t) < 2 {
			continue
		}
		attr, ok := t[1].(hcl.TraverseAttr)
		if !ok {
			continue
		}
		declared := s.vars
		if root == "local" {
			declared = s.locals
		}
		if declared[attr.Name] {
			continue
		}
		ref := root + "." + attr.Name
		key := where + "\x00" + ref
		if s.seen[key] {
			continue
		}
		s.seen[key] = true
		s.out = append(s.out, UndeclaredRef{Scope: s.scope, Where: where, Ref: ref})
	}
}
