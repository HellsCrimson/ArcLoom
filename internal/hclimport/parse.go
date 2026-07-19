// Package hclimport reconstructs the internal model from existing HCL. It is the
// inverse of hclgen. Constructs it cannot fully model (functions, complex
// expressions, dynamic blocks) are preserved verbatim as raw expressions so
// nothing is silently lost.
package hclimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Result is what a parse produced, ready to merge into a project.
type Result struct {
	// Backend is the terraform { backend "<type>" {...} } block, if present.
	Backend      *model.Backend
	Providers    []model.ProviderInstance
	Variables    []model.Variable
	Locals       []model.Local
	DataSources  []model.Resource
	Resources    []model.Resource
	Modules      []model.Module
	LocalModules []model.LocalModule
	Outputs      []model.Output
	Warnings     []string
}

// idFunc mints a unique id for an imported resource (injected so callers control
// id generation, e.g. uuid).
type idFunc func() string

// Parse parses one or more HCL files (name -> contents) into a Result.
//
// Files are scoped by path: anything under "modules/<name>/" is parsed into a
// LocalModule named <name> (its variables/resources/outputs stay in that scope
// rather than leaking into the root), inverting hclgen.GenerateFiles. Root
// `module "<name>" { source = "./modules/<name>" ... }` calls are linked to the
// matching LocalModule, their arguments becoming its Inputs.
func Parse(files map[string][]byte, newID idFunc) (*Result, error) {
	res := &Result{}
	provIndex := map[string]int{} // provider key -> index in res.Providers
	locals := map[string]*model.LocalModule{}
	var localOrder []string

	// Deterministic file order.
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		src := files[name]
		f, diags := hclsyntax.ParseConfig(src, name, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", name, diags.Error())
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		if scope := moduleScope(name); scope != "" {
			lm := locals[scope]
			if lm == nil {
				lm = &model.LocalModule{Name: scope}
				locals[scope] = lm
				localOrder = append(localOrder, scope)
			}
			parseModuleBody(body, src, name, lm, res, provIndex, newID)
			continue
		}
		parseRootBody(body, src, name, res, provIndex, newID)
	}

	linkLocalModules(res, locals, &localOrder)
	for _, n := range localOrder {
		res.LocalModules = append(res.LocalModules, *locals[n])
	}
	return res, nil
}

// parseRootBody imports the blocks of a root-scope file into res.
func parseRootBody(body *hclsyntax.Body, src []byte, name string, res *Result, provIndex map[string]int, newID idFunc) {
	for _, blk := range body.Blocks {
		switch blk.Type {
		case "resource":
			if len(blk.Labels) != 2 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: resource block missing labels", name))
				continue
			}
			res.Resources = append(res.Resources, importResource(blk, src, newID()))
			ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))

		case "provider":
			if len(blk.Labels) != 1 {
				continue
			}
			key := blk.Labels[0]
			cfg := importAttrs(blk.Body, src)
			i := ensureProviderKey(res, provIndex, key)
			if len(cfg) > 0 {
				res.Providers[i].Config = cfg
			}

		case "terraform":
			importRequiredProviders(blk, src, res, provIndex)

		case "data":
			if len(blk.Labels) == 2 {
				res.DataSources = append(res.DataSources, importResource(blk, src, newID()))
				ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))
			}

		case "variable":
			if len(blk.Labels) == 1 {
				res.Variables = append(res.Variables, importVariable(blk, src))
			}

		case "output":
			if len(blk.Labels) == 1 {
				res.Outputs = append(res.Outputs, importOutput(blk, src))
			}

		case "locals":
			res.Locals = append(res.Locals, importLocals(blk, src)...)

		case "module":
			if len(blk.Labels) == 1 {
				res.Modules = append(res.Modules, importModule(blk, src))
			}
		}
	}
}

// parseModuleBody imports the blocks of a local-module file into lm. Provider
// keys are still registered at the root (res) since the root declares providers
// for child modules. terraform{}/provider{}/nested module{} blocks are ignored
// (a warning is emitted for nested modules).
func parseModuleBody(body *hclsyntax.Body, src []byte, name string, lm *model.LocalModule, res *Result, provIndex map[string]int, newID idFunc) {
	for _, blk := range body.Blocks {
		switch blk.Type {
		case "resource":
			if len(blk.Labels) != 2 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: resource block missing labels", name))
				continue
			}
			lm.Resources = append(lm.Resources, importResource(blk, src, newID()))
			ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))

		case "data":
			if len(blk.Labels) == 2 {
				lm.DataSources = append(lm.DataSources, importResource(blk, src, newID()))
				ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))
			}

		case "variable":
			if len(blk.Labels) == 1 {
				lm.Variables = append(lm.Variables, importVariable(blk, src))
			}

		case "output":
			if len(blk.Labels) == 1 {
				lm.Outputs = append(lm.Outputs, importOutput(blk, src))
			}

		case "locals":
			lm.Locals = append(lm.Locals, importLocals(blk, src)...)

		case "module":
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: nested module in local module %q is not supported and was skipped", name, lm.Name))
		}
	}
}

// linkLocalModules connects root `module` calls with a local ("./...") source to
// a LocalModule, moving the call's arguments into the module's Inputs and
// dropping it from the remote Modules list. If the module's child files were not
// imported, an empty LocalModule is created anyway (registered in order) so the
// call is represented as a local module — this prevents a duplicate `module`
// block when the user later fills the module in. Registry/remote modules are
// left untouched.
func linkLocalModules(res *Result, locals map[string]*model.LocalModule, order *[]string) {
	kept := res.Modules[:0]
	for _, m := range res.Modules {
		if !isLocalSource(m.Source) {
			kept = append(kept, m)
			continue
		}
		folder := localSourceFolder(m.Source)
		lm, ok := locals[folder]
		if !ok {
			lm = &model.LocalModule{Name: folder}
			locals[folder] = lm
			*order = append(*order, folder)
		}
		if lm.Inputs == nil && len(m.Config) > 0 {
			lm.Inputs = m.Config
		}
		if m.Name != folder {
			res.Warnings = append(res.Warnings, fmt.Sprintf("module call %q references %q; imported as local module %q (ArcLoom couples the call name to the folder)", m.Name, m.Source, folder))
		}
	}
	res.Modules = kept
}

// moduleScope returns the local-module name for a file path (the segment after
// a "modules/" component), or "" for a root-scope file.
func moduleScope(name string) string {
	parts := strings.Split(strings.ReplaceAll(name, "\\", "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "modules" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return ""
}

// isLocalSource reports whether a module source is a local path (vs a registry
// or remote address).
func isLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

// localSourceFolder returns the folder name a local module source resolves to:
// the segment after "modules/", else the last path segment.
func localSourceFolder(source string) string {
	parts := strings.Split(source, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "modules" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ensureProviderKey returns the index of the provider with the given local name,
// creating it (with a placeholder source) if absent.
func ensureProviderKey(res *Result, index map[string]int, key string) int {
	if i, ok := index[key]; ok {
		return i
	}
	res.Providers = append(res.Providers, model.ProviderInstance{
		Key:    key,
		Source: "hashicorp/" + key, // refined if terraform{} declares a real source
	})
	i := len(res.Providers) - 1
	index[key] = i
	return i
}

// importRequiredProviders reads terraform { required_providers { ... } } and,
// when present, the backend block (source text preserved via importAttrs).
func importRequiredProviders(tfBlock *hclsyntax.Block, src []byte, res *Result, index map[string]int) {
	for _, inner := range tfBlock.Body.Blocks {
		if inner.Type == "backend" && len(inner.Labels) == 1 && res.Backend == nil {
			res.Backend = &model.Backend{Type: inner.Labels[0], Config: importAttrs(inner.Body, src)}
			continue
		}
		if inner.Type != "required_providers" {
			continue
		}
		for key, attr := range inner.Body.Attributes {
			val, diags := attr.Expr.Value(nil)
			if diags.HasErrors() || !val.Type().IsObjectType() {
				continue
			}
			i := ensureProviderKey(res, index, key)
			if val.Type().HasAttribute("source") {
				res.Providers[i].Source = val.GetAttr("source").AsString()
			}
			if val.Type().HasAttribute("version") {
				res.Providers[i].Version = val.GetAttr("version").AsString()
			}
		}
	}
}

// importResource builds a model.Resource from a resource block.
func importResource(blk *hclsyntax.Block, src []byte, id string) model.Resource {
	r := model.Resource{
		ID:    id,
		Type:  blk.Labels[0],
		Name:  blk.Labels[1],
		Attrs: importAttrs(blk.Body, src),
	}
	// count / for_each are meta-arguments, not provider attributes: lift them
	// out of Attrs into their dedicated fields (kept as raw expression text).
	if a, ok := blk.Body.Attributes["count"]; ok {
		r.Count = rawSource(a.Expr, src)
		delete(r.Attrs, "count")
	}
	if a, ok := blk.Body.Attributes["for_each"]; ok {
		r.ForEach = rawSource(a.Expr, src)
		delete(r.Attrs, "for_each")
	}
	if len(r.Attrs) == 0 {
		r.Attrs = nil
	}
	// Nested blocks -> r.Blocks[type] = [...]
	for _, nested := range blk.Body.Blocks {
		nb := model.Resource{Attrs: importAttrs(nested.Body, src)}
		if r.Blocks == nil {
			r.Blocks = map[string][]model.Resource{}
		}
		r.Blocks[nested.Type] = append(r.Blocks[nested.Type], nb)
	}
	return r
}

func importVariable(blk *hclsyntax.Block, src []byte) model.Variable {
	v := model.Variable{Name: blk.Labels[0]}
	if a, ok := blk.Body.Attributes["type"]; ok {
		v.Type = rawSource(a.Expr, src)
	}
	if a, ok := blk.Body.Attributes["default"]; ok {
		def := importExpr(a.Expr, src)
		v.Default = &def
	}
	if a, ok := blk.Body.Attributes["description"]; ok {
		v.Description = literalString(a.Expr)
	}
	if a, ok := blk.Body.Attributes["sensitive"]; ok {
		v.Sensitive = literalBool(a.Expr)
	}
	return v
}

func importOutput(blk *hclsyntax.Block, src []byte) model.Output {
	o := model.Output{Name: blk.Labels[0]}
	if a, ok := blk.Body.Attributes["value"]; ok {
		o.Value = importExpr(a.Expr, src)
	}
	if a, ok := blk.Body.Attributes["description"]; ok {
		o.Description = literalString(a.Expr)
	}
	if a, ok := blk.Body.Attributes["sensitive"]; ok {
		o.Sensitive = literalBool(a.Expr)
	}
	return o
}

func importLocals(blk *hclsyntax.Block, src []byte) []model.Local {
	names := make([]string, 0, len(blk.Body.Attributes))
	for n := range blk.Body.Attributes {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]model.Local, 0, len(names))
	for _, n := range names {
		out = append(out, model.Local{Name: n, Value: importExpr(blk.Body.Attributes[n].Expr, src)})
	}
	return out
}

func importModule(blk *hclsyntax.Block, src []byte) model.Module {
	m := model.Module{Name: blk.Labels[0], Config: map[string]model.AttrValue{}}
	for name, attr := range blk.Body.Attributes {
		switch name {
		case "source":
			m.Source = literalString(attr.Expr)
		case "version":
			m.Version = literalString(attr.Expr)
		default:
			m.Config[name] = importExpr(attr.Expr, src)
		}
	}
	if len(m.Config) == 0 {
		m.Config = nil
	}
	return m
}

// rawSource returns the verbatim source text of an expression.
func rawSource(expr hclsyntax.Expression, src []byte) string {
	rng := expr.Range()
	return strings.TrimSpace(string(src[rng.Start.Byte:rng.End.Byte]))
}

func literalString(expr hclsyntax.Expression) string {
	if val, diags := expr.Value(nil); !diags.HasErrors() && val.Type() == cty.String {
		return val.AsString()
	}
	return ""
}

func literalBool(expr hclsyntax.Expression) bool {
	if val, diags := expr.Value(nil); !diags.HasErrors() && val.Type() == cty.Bool {
		return val.True()
	}
	return false
}

// importAttrs converts a body's attributes into model AttrValues.
func importAttrs(body *hclsyntax.Body, src []byte) map[string]model.AttrValue {
	if len(body.Attributes) == 0 {
		return nil
	}
	out := make(map[string]model.AttrValue, len(body.Attributes))
	for name, attr := range body.Attributes {
		out[name] = importExpr(attr.Expr, src)
	}
	return out
}

// importExpr classifies an expression into literal / reference / expression.
func importExpr(expr hclsyntax.Expression, src []byte) model.AttrValue {
	// A value that evaluates with no context is a pure literal.
	if val, diags := expr.Value(nil); !diags.HasErrors() && val.IsWhollyKnown() {
		return model.AttrValue{Mode: model.ModeLiteral, Literal: val}
	}
	// A simple scope traversal ("type.name.attr", "var.x") is a reference.
	if trav, ok := expr.(*hclsyntax.ScopeTraversalExpr); ok {
		if ref := simpleTraversal(trav.Traversal); ref != "" {
			return model.AttrValue{Mode: model.ModeReference, Ref: ref}
		}
	}
	// Everything else is preserved as a raw expression.
	rng := expr.Range()
	raw := strings.TrimSpace(string(src[rng.Start.Byte:rng.End.Byte]))
	return model.AttrValue{Mode: model.ModeExpression, Expr: raw}
}

// simpleTraversal renders a traversal as a dotted string, or "" if it contains
// index steps (which the reference mode cannot represent).
func simpleTraversal(t hcl.Traversal) string {
	var parts []string
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			parts = append(parts, s.Name)
		case hcl.TraverseAttr:
			parts = append(parts, s.Name)
		default:
			return "" // index or splat -> not a simple reference
		}
	}
	return strings.Join(parts, ".")
}
