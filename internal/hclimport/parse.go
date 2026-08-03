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
	// RequiredVersion is the terraform block's required_version constraint, if set.
	RequiredVersion string
	// Backend is the terraform { backend "<type>" {...} } block, if present.
	Backend      *model.Backend
	Providers    []model.ProviderInstance
	Variables    []model.Variable
	Locals       []model.Local
	DataSources  []model.Resource
	Resources    []model.Resource
	Modules      []model.Module
	Imports      []model.Import
	Moved        []model.Moved
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
			r := importResource(blk, src, newID())
			res.Resources = append(res.Resources, r)
			ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))
			registerAliasRef(res, provIndex, r.Provider)

		case "provider":
			if len(blk.Labels) != 1 {
				continue
			}
			key := blk.Labels[0]
			cfg := importAttrs(blk.Body, src)
			// alias is a meta-argument, not provider config: it makes this block a
			// separate instance of the provider (indexed as "key.alias").
			alias := ""
			if av, ok := cfg["alias"]; ok && av.Mode == model.ModeLiteral && av.Literal.Type() == cty.String {
				alias = av.Literal.AsString()
				delete(cfg, "alias")
			}
			if len(cfg) == 0 {
				cfg = nil
			}
			i := ensureProviderKey(res, provIndex, key)
			if alias != "" {
				i = ensureProviderInstance(res, provIndex, key, alias)
			}
			if len(cfg) > 0 {
				res.Providers[i].Config = cfg
			}

		case "terraform":
			importRequiredProviders(blk, src, res, provIndex)

		case "data":
			if len(blk.Labels) == 2 {
				d := importResource(blk, src, newID())
				res.DataSources = append(res.DataSources, d)
				ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))
				registerAliasRef(res, provIndex, d.Provider)
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

		case "import":
			// Config-driven import (`import { to = …, id = … }`). Kept as raw
			// expression text like every other meta-arg: `to` carries an index
			// under a for_each and `id` is usually built from each.value, so
			// neither survives being parsed into anything more structured.
			res.Imports = append(res.Imports, importImportBlock(blk, src, newID()))

		case "moved":
			// Config-driven rename (`moved { from = …, to = … }`). Both fields
			// are addresses, kept as their source text like the import block's:
			// an address with an index is not a name, and re-lexing it is what
			// hclgen emits back verbatim.
			res.Moved = append(res.Moved, importMovedBlock(blk, src, newID()))
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
			r := importResource(blk, src, newID())
			lm.Resources = append(lm.Resources, r)
			ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))
			registerAliasRef(res, provIndex, r.Provider)

		case "data":
			if len(blk.Labels) == 2 {
				d := importResource(blk, src, newID())
				lm.DataSources = append(lm.DataSources, d)
				ensureProviderKey(res, provIndex, model.ProviderLocalName(blk.Labels[0]))
				registerAliasRef(res, provIndex, d.Provider)
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

		case "terraform":
			// A real module folder carries its own required_providers (that is
			// what makes it work at all), and it is the only place the module's
			// provider SOURCES are written down. Read them into the root's
			// instances — ArcLoom keeps every provider instance at the root and
			// re-derives each module's block from there on generation — so an
			// imported module upgrades a `hashicorp/<key>` placeholder to the
			// real source instead of leaving the project pointing at a provider
			// that does not exist.
			//
			// Only required_providers: a child module has no backend and its
			// required_version is the root's business.
			importModuleRequiredProviders(blk, res, provIndex)

		case "module":
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: nested module in local module %q is not supported and was skipped", name, lm.Name))
		}
	}
}

// importModuleRequiredProviders reads a LOCAL MODULE's required_providers into
// the root's provider instances.
//
// It only ever REFINES: a source already known to the project wins, because the
// root's own terraform block is authoritative and file order must not decide
// which one lands. `configuration_aliases` entries register the aliased
// instance, which is exactly the instance the caller has to pass.
func importModuleRequiredProviders(tfBlock *hclsyntax.Block, res *Result, index map[string]int) {
	for _, inner := range tfBlock.Body.Blocks {
		if inner.Type != "required_providers" {
			continue
		}
		for key, attr := range inner.Body.Attributes {
			entry := requiredProviderEntry(attr.Expr)
			i := ensureProviderKey(res, index, key)
			if entry.source != "" && isPlaceholderSource(res.Providers[i].Source, key) {
				res.Providers[i].Source = entry.source
			}
			for _, alias := range entry.aliases {
				ensureProviderInstance(res, index, key, alias)
			}
		}
	}
}

// isPlaceholderSource reports whether a stored source is the "we had to guess"
// value ensureProviderInstance writes, rather than something the config stated.
func isPlaceholderSource(source, key string) bool {
	return source == "" || source == "hashicorp/"+key
}

// providerEntry is what one required_providers value declares.
type providerEntry struct {
	source  string
	version string
	aliases []string
}

// requiredProviderEntry reads a required_providers object WITHOUT evaluating
// it. Evaluating is what the root importer used to do, and it drops the whole
// entry — source and all — as soon as `configuration_aliases` is present,
// because that holds provider TRAVERSALS (`aws.euwest`) which have no value.
// Since configuration_aliases is exactly what a module using an aliased
// provider must declare, the modules that needed importing most were the ones
// whose source went missing.
func requiredProviderEntry(expr hclsyntax.Expression) providerEntry {
	var out providerEntry
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return out
	}
	for _, item := range obj.Items {
		kv, diags := item.KeyExpr.Value(nil)
		if diags.HasErrors() || kv.Type() != cty.String {
			continue
		}
		switch kv.AsString() {
		case "source":
			out.source = literalString(item.ValueExpr)
		case "version":
			out.version = literalString(item.ValueExpr)
		case "configuration_aliases":
			tuple, ok := item.ValueExpr.(*hclsyntax.TupleConsExpr)
			if !ok {
				continue
			}
			for _, el := range tuple.Exprs {
				trav, ok := el.(*hclsyntax.ScopeTraversalExpr)
				if !ok {
					continue
				}
				// `aws.euwest` — root name plus one attribute.
				if ref := simpleTraversal(trav.Traversal); ref != "" {
					if _, alias, found := strings.Cut(ref, "."); found && alias != "" {
						out.aliases = append(out.aliases, alias)
					}
				}
			}
		}
	}
	return out
}

// linkLocalModules makes sure every root `module` call with a local ("./...")
// source has a LocalModule definition to point at. The CALLS are left in
// res.Modules untouched — including their name, arguments and meta-args, and
// including several calls to the same folder, which is the whole point of
// keeping definitions and uses apart. If the module's own files were not part
// of the import, an empty definition is registered (in order) so the call still
// resolves and the user can fill the module in later.
//
// Registry/remote calls need no definition and are skipped.
//
// A local source that isn't already the canonical "./modules/<folder>" (say
// "../modules/vpc") is REWRITTEN to it, because the definition it resolves to
// is generated at modules/<folder>/ and the original relative path would not
// exist in the run workspace. That rewrite is reported as a warning — it is the
// one thing here that doesn't round-trip.
func linkLocalModules(res *Result, locals map[string]*model.LocalModule, order *[]string) {
	for i := range res.Modules {
		m := &res.Modules[i]
		if !isLocalSource(m.Source) {
			continue
		}
		folder := localSourceFolder(m.Source)
		if folder == "" {
			continue
		}
		if canonical := model.LocalSource(folder); m.Source != canonical {
			res.Warnings = append(res.Warnings, fmt.Sprintf("module %q: source %q imported as %q (local modules live in modules/<name>/)", m.Name, m.Source, canonical))
			m.Source = canonical
		}
		if _, ok := locals[folder]; !ok {
			locals[folder] = &model.LocalModule{Name: folder}
			*order = append(*order, folder)
		}
	}
}

// importImportBlock reads one `import` block. Its attributes are stored as the
// source text between the "=" and the end of the expression, which is what
// hclgen emits back verbatim.
func importImportBlock(blk *hclsyntax.Block, src []byte, id string) model.Import {
	imp := model.Import{ID: id}
	for name, attr := range blk.Body.Attributes {
		text := strings.TrimSpace(string(attr.Expr.Range().SliceBytes(src)))
		switch name {
		case "for_each":
			imp.ForEach = text
		case "to":
			imp.To = text
		case "id":
			imp.ImportID = text
		case "provider":
			imp.Provider = text
		}
	}
	return imp
}

// importMovedBlock reads one `moved` block.
func importMovedBlock(blk *hclsyntax.Block, src []byte, id string) model.Moved {
	mv := model.Moved{ID: id}
	for name, attr := range blk.Body.Attributes {
		text := strings.TrimSpace(string(attr.Expr.Range().SliceBytes(src)))
		switch name {
		case "from":
			mv.From = text
		case "to":
			mv.To = text
		}
	}
	return mv
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

// registerAliasRef ensures the aliased provider instance a resource's provider
// meta-arg ("aws.euwest") points at exists, so generation emits a matching
// provider block even when the source HCL declared none.
func registerAliasRef(res *Result, index map[string]int, ref string) {
	parts := strings.Split(strings.TrimSpace(ref), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return
	}
	ensureProviderInstance(res, index, parts[0], parts[1])
}

// ensureProviderKey returns the index of the default (unaliased) provider with
// the given local name, creating it (with a placeholder source) if absent.
func ensureProviderKey(res *Result, index map[string]int, key string) int {
	return ensureProviderInstance(res, index, key, "")
}

// ensureProviderInstance returns the index of the provider instance with the
// given local name and alias, creating it if absent. An aliased instance
// inherits source/version from the key's default instance when that is known.
func ensureProviderInstance(res *Result, index map[string]int, key, alias string) int {
	ik := key
	if alias != "" {
		ik = key + "." + alias
	}
	if i, ok := index[ik]; ok {
		return i
	}
	pi := model.ProviderInstance{
		Key:    key,
		Alias:  alias,
		Source: "hashicorp/" + key, // refined if terraform{} declares a real source
	}
	if di, ok := index[key]; ok && alias != "" {
		pi.Source = res.Providers[di].Source
		pi.Version = res.Providers[di].Version
	}
	res.Providers = append(res.Providers, pi)
	i := len(res.Providers) - 1
	index[ik] = i
	return i
}

// importRequiredProviders reads terraform { required_providers { ... } } and,
// when present, the backend block (source text preserved via importAttrs).
func importRequiredProviders(tfBlock *hclsyntax.Block, src []byte, res *Result, index map[string]int) {
	// required_version is an attribute of the terraform block, not a nested one.
	if a, ok := tfBlock.Body.Attributes["required_version"]; ok && res.RequiredVersion == "" {
		if val, diags := a.Expr.Value(nil); !diags.HasErrors() && val.Type() == cty.String {
			res.RequiredVersion = val.AsString()
		}
	}
	for _, inner := range tfBlock.Body.Blocks {
		if inner.Type == "backend" && len(inner.Labels) == 1 && res.Backend == nil {
			res.Backend = &model.Backend{Type: inner.Labels[0], Config: importAttrs(inner.Body, src)}
			continue
		}
		if inner.Type != "required_providers" {
			continue
		}
		for key, attr := range inner.Body.Attributes {
			// Read structurally rather than by evaluating: an entry carrying
			// configuration_aliases holds traversals and has no value, and
			// evaluating it dropped the source with it.
			entry := requiredProviderEntry(attr.Expr)
			if entry.source == "" && entry.version == "" && len(entry.aliases) == 0 {
				continue
			}
			ensureProviderKey(res, index, key)
			// Apply to every instance of the key: aliased instances share the
			// required_providers entry.
			for i := range res.Providers {
				if res.Providers[i].Key != key {
					continue
				}
				if entry.source != "" {
					res.Providers[i].Source = entry.source
				}
				if entry.version != "" {
					res.Providers[i].Version = entry.version
				}
			}
			for _, alias := range entry.aliases {
				ensureProviderInstance(res, index, key, alias)
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
	// provider selects an aliased provider instance ("aws.euwest") — also a
	// meta-argument.
	if a, ok := blk.Body.Attributes["provider"]; ok {
		r.Provider = rawSource(a.Expr, src)
		delete(r.Attrs, "provider")
	}
	if len(r.Attrs) == 0 {
		r.Attrs = nil
	}
	r.Blocks = importBlocks(blk.Body, src)
	return r
}

// importBlocks reads a body's nested blocks into the Blocks map, RECURSIVELY
// and keeping each block's labels.
//
// Both halves matter for `dynamic`, which is the construct that needs them:
//
//	dynamic "ingress" {        <- one label, "ingress"
//	  for_each = var.ports
//	  content { ... }          <- a block nested two deep
//	}
//
// Reading only the top level's attributes kept the for_each and dropped both,
// so generation emitted a bare `dynamic {` with no content — rejected by the
// engine ("Missing type for dynamic") and missing the body besides. The same
// applied to every labelled block (`provisioner "local-exec"`) and everything
// below depth one.
//
// count/for_each are NOT lifted out here the way importResource lifts a
// resource's: inside a nested block they are ordinary arguments (a dynamic
// block's for_each is the block's own), not meta-arguments of a resource.
func importBlocks(body *hclsyntax.Body, src []byte) map[string][]model.Resource {
	if len(body.Blocks) == 0 {
		return nil
	}
	out := map[string][]model.Resource{}
	for _, nested := range body.Blocks {
		nb := model.Resource{
			Labels: append([]string(nil), nested.Labels...),
			Attrs:  importAttrs(nested.Body, src),
			Blocks: importBlocks(nested.Body, src),
		}
		out[nested.Type] = append(out[nested.Type], nb)
	}
	return out
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
		// count / for_each are meta-arguments of the call, not module inputs:
		// keep them as raw expression text, like importResource does.
		case "count":
			m.Count = rawSource(attr.Expr, src)
		case "for_each":
			m.ForEach = rawSource(attr.Expr, src)
		// providers is a meta-argument too, and the one most costly to miss: as
		// a Config entry it becomes a fake module input, which the inputs form
		// (rebuilt from the module's DECLARED variables) deletes on the first
		// save — silently unwiring the module's providers. Kept verbatim because
		// a hand-written mapping may be non-identity, which no re-derivation can
		// reproduce.
		case "providers":
			m.Providers = rawSource(attr.Expr, src)
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
