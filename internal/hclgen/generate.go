// Package hclgen converts the internal infrastructure model into clean,
// deterministic, formatted HCL. It is the only layer that produces HCL text.
package hclgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Generate renders a full project: terraform{required_providers}, provider
// blocks, then resource blocks. Output is formatted for deterministic diffs.
func Generate(p *model.Project) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()

	if err := writeTerraformBlock(body, p.Providers); err != nil {
		return nil, err
	}
	for _, prov := range p.Providers {
		body.AppendNewline()
		if err := writeProviderBlock(body, prov); err != nil {
			return nil, err
		}
	}
	// Conventional ordering: variables, locals, data, resources, modules, outputs.
	for _, v := range p.Variables {
		body.AppendNewline()
		if err := writeVariableBlock(body, v); err != nil {
			return nil, err
		}
	}
	if len(p.Locals) > 0 {
		body.AppendNewline()
		if err := writeLocalsBlock(body, p.Locals); err != nil {
			return nil, err
		}
	}
	for _, d := range p.DataSources {
		body.AppendNewline()
		if err := writeBlockWithKeyword(body, "data", d); err != nil {
			return nil, err
		}
	}
	for _, r := range p.Resources {
		body.AppendNewline()
		if err := writeBlockWithKeyword(body, "resource", r); err != nil {
			return nil, err
		}
	}
	for _, m := range p.Modules {
		body.AppendNewline()
		if err := writeModuleBlock(body, m); err != nil {
			return nil, err
		}
	}
	for _, o := range p.Outputs {
		body.AppendNewline()
		if err := writeOutputBlock(body, o); err != nil {
			return nil, err
		}
	}
	return hclwrite.Format(f.Bytes()), nil
}

// ProvidersConfig renders only the terraform{required_providers} block and the
// provider blocks — enough to `init` and fetch schemas without any resources.
func ProvidersConfig(providers []model.ProviderInstance) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	if err := writeTerraformBlock(body, providers); err != nil {
		return nil, err
	}
	for _, prov := range providers {
		body.AppendNewline()
		if err := writeProviderBlock(body, prov); err != nil {
			return nil, err
		}
	}
	return hclwrite.Format(f.Bytes()), nil
}

func writeTerraformBlock(body *hclwrite.Body, providers []model.ProviderInstance) error {
	tfBlock := body.AppendNewBlock("terraform", nil)
	rp := tfBlock.Body().AppendNewBlock("required_providers", nil)

	// Sort by key for deterministic output.
	sorted := append([]model.ProviderInstance(nil), providers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	for _, prov := range sorted {
		obj := map[string]cty.Value{"source": cty.StringVal(prov.Source)}
		if prov.Version != "" {
			obj["version"] = cty.StringVal(prov.Version)
		}
		rp.Body().SetAttributeValue(prov.Key, cty.ObjectVal(obj))
	}
	return nil
}

func writeProviderBlock(body *hclwrite.Body, prov model.ProviderInstance) error {
	blk := body.AppendNewBlock("provider", []string{prov.Key})
	return writeAttrs(blk.Body(), prov.Config)
}

// writeBlockWithKeyword writes a resource or data block (keyword = "resource"
// or "data"), including attributes, depends_on, and nested blocks.
func writeBlockWithKeyword(body *hclwrite.Body, keyword string, r model.Resource) error {
	blk := body.AppendNewBlock(keyword, []string{r.Type, r.Name})
	if err := writeAttrs(blk.Body(), r.Attrs); err != nil {
		return err
	}
	if len(r.DependsOn) > 0 {
		toks, err := tokensForExpr("[" + strings.Join(r.DependsOn, ", ") + "]")
		if err != nil {
			return fmt.Errorf("depends_on: %w", err)
		}
		blk.Body().SetAttributeRaw("depends_on", toks)
	}
	// Nested blocks, sorted by name for determinism.
	names := make([]string, 0, len(r.Blocks))
	for n := range r.Blocks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, nested := range r.Blocks[n] {
			nb := blk.Body().AppendNewBlock(n, nil)
			if err := writeAttrs(nb.Body(), nested.Attrs); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeVariableBlock(body *hclwrite.Body, v model.Variable) error {
	blk := body.AppendNewBlock("variable", []string{v.Name})
	b := blk.Body()
	if v.Type != "" {
		toks, err := tokensForExpr(v.Type)
		if err != nil {
			return fmt.Errorf("variable %q type: %w", v.Name, err)
		}
		b.SetAttributeRaw("type", toks)
	}
	if v.Default != nil {
		if err := setAttr(b, "default", *v.Default); err != nil {
			return fmt.Errorf("variable %q default: %w", v.Name, err)
		}
	}
	if v.Description != "" {
		b.SetAttributeValue("description", cty.StringVal(v.Description))
	}
	if v.Sensitive {
		b.SetAttributeValue("sensitive", cty.True)
	}
	return nil
}

func writeLocalsBlock(body *hclwrite.Body, locals []model.Local) error {
	blk := body.AppendNewBlock("locals", nil)
	attrs := make(map[string]model.AttrValue, len(locals))
	for _, l := range locals {
		attrs[l.Name] = l.Value
	}
	return writeAttrs(blk.Body(), attrs)
}

func writeOutputBlock(body *hclwrite.Body, o model.Output) error {
	blk := body.AppendNewBlock("output", []string{o.Name})
	b := blk.Body()
	if err := setAttr(b, "value", o.Value); err != nil {
		return fmt.Errorf("output %q value: %w", o.Name, err)
	}
	if o.Description != "" {
		b.SetAttributeValue("description", cty.StringVal(o.Description))
	}
	if o.Sensitive {
		b.SetAttributeValue("sensitive", cty.True)
	}
	return nil
}

func writeModuleBlock(body *hclwrite.Body, m model.Module) error {
	blk := body.AppendNewBlock("module", []string{m.Name})
	b := blk.Body()
	b.SetAttributeValue("source", cty.StringVal(m.Source))
	if m.Version != "" {
		b.SetAttributeValue("version", cty.StringVal(m.Version))
	}
	return writeAttrs(b, m.Config)
}

// writeAttrs emits attributes in sorted key order.
func writeAttrs(body *hclwrite.Body, attrs map[string]model.AttrValue) error {
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := setAttr(body, name, attrs[name]); err != nil {
			return err
		}
	}
	return nil
}

// setAttr writes one attribute, dispatching on value mode.
func setAttr(body *hclwrite.Body, name string, v model.AttrValue) error {
	switch v.Mode {
	case model.ModeLiteral:
		body.SetAttributeValue(name, v.Literal)
	case model.ModeReference:
		trav, err := parseTraversal(v.Ref)
		if err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
		body.SetAttributeTraversal(name, trav)
	case model.ModeExpression:
		toks, err := tokensForExpr(v.Expr)
		if err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
		body.SetAttributeRaw(name, toks)
	case model.ModeSecret:
		// Stub: reference a variable named after the secret, never inline.
		trav, err := parseTraversal("var." + secretVarName(v.SecretRef))
		if err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
		body.SetAttributeTraversal(name, trav)
	default:
		return fmt.Errorf("attribute %q: unknown value mode %q", name, v.Mode)
	}
	return nil
}

// parseTraversal turns a dotted reference like "random_pet.name.id" into an
// hcl.Traversal for SetAttributeTraversal.
func parseTraversal(ref string) (hcl.Traversal, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("empty reference")
	}
	parts := strings.Split(ref, ".")
	trav := hcl.Traversal{hcl.TraverseRoot{Name: parts[0]}}
	for _, p := range parts[1:] {
		if p == "" {
			return nil, fmt.Errorf("invalid reference %q", ref)
		}
		trav = append(trav, hcl.TraverseAttr{Name: p})
	}
	return trav, nil
}

// tokensForExpr lexes a raw HCL expression and validates it before emitting, so
// malformed input fails here rather than opaquely at plan time. It parses the
// expression as a throwaway attribute and returns its properly-lexed tokens.
func tokensForExpr(expr string) (hclwrite.Tokens, error) {
	if _, diags := hclsyntax.ParseExpression([]byte(expr), "expr.hcl", hcl.Pos{Line: 1, Column: 1}); diags.HasErrors() {
		return nil, fmt.Errorf("invalid expression: %s", diags.Error())
	}
	src := []byte("__arcloom_expr = " + expr + "\n")
	tmp, diags := hclwrite.ParseConfig(src, "expr.hcl", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("invalid expression: %s", diags.Error())
	}
	attr := tmp.Body().GetAttribute("__arcloom_expr")
	if attr == nil {
		return nil, fmt.Errorf("could not parse expression")
	}
	return attr.Expr().BuildTokens(nil), nil
}

func secretVarName(ref string) string {
	r := strings.NewReplacer("/", "_", "-", "_", ".", "_", " ", "_")
	return r.Replace(ref)
}
