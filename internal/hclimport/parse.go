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

	"github.com/matthiasrauline/arcloom/internal/model"
)

// Result is what a parse produced, ready to merge into a project.
type Result struct {
	Providers []model.ProviderInstance
	Resources []model.Resource
	Warnings  []string
}

// idFunc mints a unique id for an imported resource (injected so callers control
// id generation, e.g. uuid).
type idFunc func() string

// Parse parses one or more HCL files (name -> contents) into a Result.
func Parse(files map[string][]byte, newID idFunc) (*Result, error) {
	res := &Result{}
	provIndex := map[string]int{} // provider key -> index in res.Providers

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
				importRequiredProviders(blk, res, provIndex)

			case "variable", "output", "locals", "module", "data":
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("%s: %q blocks are not yet modeled and were skipped", name, blk.Type))
			}
		}
	}
	return res, nil
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

// importRequiredProviders reads terraform { required_providers { k = { source, version } } }.
func importRequiredProviders(tfBlock *hclsyntax.Block, res *Result, index map[string]int) {
	for _, inner := range tfBlock.Body.Blocks {
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
