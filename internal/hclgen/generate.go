// Package hclgen converts the internal infrastructure model into clean,
// deterministic, formatted HCL. It is the only layer that produces HCL text.
package hclgen

import (
	"fmt"
	"sort"
	"strconv"
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

	if err := writeTerraformBlock(body, p.Providers, p.Backend, p.RequiredVersion); err != nil {
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
	// Auto-declare a sensitive input variable for each referenced secret that the
	// user hasn't already declared, so var.<name> resolves.
	declared := map[string]bool{}
	for _, v := range p.Variables {
		declared[v.Name] = true
	}
	for _, ref := range CollectSecretRefs(p) {
		name := model.SecretVarName(ref)
		if declared[name] {
			continue
		}
		declared[name] = true
		body.AppendNewline()
		if err := writeVariableBlock(body, model.Variable{Name: name, Type: "string", Sensitive: true}); err != nil {
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
	// One loop for every module call: local ("./modules/x") and registry alike.
	// A local module's *definition* generates its own file (GenerateFiles) and
	// emits nothing here — a definition that is never called is simply never
	// used, exactly as in HCL.
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
	// No backend here: schema fetching must never touch (or initialize) the
	// project's remote state.
	// No required_version either: the schema fetch runs on whatever CLI the server
	// has, and a project constraint it doesn't satisfy would fail the fetch for no
	// benefit.
	if err := writeTerraformBlock(body, providers, nil, ""); err != nil {
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

func writeTerraformBlock(body *hclwrite.Body, providers []model.ProviderInstance, backend *model.Backend, requiredVersion string) error {
	tfBlock := body.AppendNewBlock("terraform", nil)
	// required_version leads the block, as Terraform's own docs and every style
	// guide (and tflint's terraform_required_version rule) put it.
	if requiredVersion != "" {
		tfBlock.Body().SetAttributeValue("required_version", cty.StringVal(requiredVersion))
	}
	if backend != nil && backend.Type != "" {
		bb := tfBlock.Body().AppendNewBlock("backend", []string{backend.Type})
		if err := writeBackendAttrs(bb.Body(), backend.Config); err != nil {
			return fmt.Errorf("backend %q: %w", backend.Type, err)
		}
	}
	rp := tfBlock.Body().AppendNewBlock("required_providers", nil)

	// Sort by key for deterministic output. Aliased instances share their key's
	// required_providers entry — emit one per key, preferring an instance that
	// pins a version.
	sorted := append([]model.ProviderInstance(nil), providers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	written := map[string]bool{}
	for _, prov := range sorted {
		if written[prov.Key] {
			continue
		}
		obj := map[string]cty.Value{"source": cty.StringVal(prov.Source)}
		version := prov.Version
		if version == "" {
			for _, other := range sorted {
				if other.Key == prov.Key && other.Version != "" {
					version = other.Version
					break
				}
			}
		}
		if version != "" {
			obj["version"] = cty.StringVal(version)
		}
		rp.Body().SetAttributeValue(prov.Key, cty.ObjectVal(obj))
		written[prov.Key] = true
	}
	return nil
}

func writeProviderBlock(body *hclwrite.Body, prov model.ProviderInstance) error {
	blk := body.AppendNewBlock("provider", []string{prov.Key})
	if prov.Alias != "" {
		blk.Body().SetAttributeValue("alias", cty.StringVal(prov.Alias))
	}
	return writeAttrs(blk.Body(), prov.Config)
}

// writeBlockWithKeyword writes a resource or data block (keyword = "resource"
// or "data"), including attributes, depends_on, and nested blocks.
func writeBlockWithKeyword(body *hclwrite.Body, keyword string, r model.Resource) error {
	blk := body.AppendNewBlock(keyword, []string{r.Type, r.Name})
	// count / for_each lead the block, matching terraform style.
	if r.Count != "" && r.ForEach != "" {
		return fmt.Errorf("%s.%s: count and for_each are mutually exclusive", r.Type, r.Name)
	}
	if r.Count != "" {
		toks, err := tokensForExpr(r.Count)
		if err != nil {
			return fmt.Errorf("%s.%s count: %w", r.Type, r.Name, err)
		}
		blk.Body().SetAttributeRaw("count", toks)
	}
	if r.ForEach != "" {
		toks, err := tokensForExpr(r.ForEach)
		if err != nil {
			return fmt.Errorf("%s.%s for_each: %w", r.Type, r.Name, err)
		}
		blk.Body().SetAttributeRaw("for_each", toks)
	}
	// provider meta-arg selects a non-default (aliased) provider instance.
	if r.Provider != "" {
		trav, err := parseTraversal(r.Provider)
		if err != nil {
			return fmt.Errorf("%s.%s provider: %w", r.Type, r.Name, err)
		}
		blk.Body().SetAttributeTraversal("provider", trav)
	}
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

// writeModuleMeta emits the count / for_each meta-arguments of a module call.
// They lead the block, matching writeBlockWithKeyword's treatment of resources.
func writeModuleMeta(b *hclwrite.Body, name, count, forEach string) error {
	if count != "" && forEach != "" {
		return fmt.Errorf("module %q: count and for_each are mutually exclusive", name)
	}
	if count != "" {
		toks, err := tokensForExpr(count)
		if err != nil {
			return fmt.Errorf("module %q count: %w", name, err)
		}
		b.SetAttributeRaw("count", toks)
	}
	if forEach != "" {
		toks, err := tokensForExpr(forEach)
		if err != nil {
			return fmt.Errorf("module %q for_each: %w", name, err)
		}
		b.SetAttributeRaw("for_each", toks)
	}
	return nil
}

func writeModuleBlock(body *hclwrite.Body, m model.Module) error {
	blk := body.AppendNewBlock("module", []string{m.Name})
	b := blk.Body()
	if err := writeModuleMeta(b, m.Name, m.Count, m.ForEach); err != nil {
		return err
	}
	b.SetAttributeValue("source", cty.StringVal(m.Source))
	if m.Version != "" {
		b.SetAttributeValue("version", cty.StringVal(m.Version))
	}
	return writeAttrs(b, m.Config)
}

// GenerateModuleFile renders a local module's own file (variables, locals, data,
// resources, outputs) — no terraform{} or provider{} blocks (inherited).
func GenerateModuleFile(lm model.LocalModule) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	first := true
	nl := func() {
		if !first {
			body.AppendNewline()
		}
		first = false
	}
	for _, v := range lm.Variables {
		nl()
		if err := writeVariableBlock(body, v); err != nil {
			return nil, err
		}
	}
	if len(lm.Locals) > 0 {
		nl()
		if err := writeLocalsBlock(body, lm.Locals); err != nil {
			return nil, err
		}
	}
	for _, d := range lm.DataSources {
		nl()
		if err := writeBlockWithKeyword(body, "data", d); err != nil {
			return nil, err
		}
	}
	for _, r := range lm.Resources {
		nl()
		if err := writeBlockWithKeyword(body, "resource", r); err != nil {
			return nil, err
		}
	}
	for _, o := range lm.Outputs {
		nl()
		if err := writeOutputBlock(body, o); err != nil {
			return nil, err
		}
	}
	return hclwrite.Format(f.Bytes()), nil
}

// GenerateFiles renders the whole project as a set of files keyed by path
// relative to the generated/ directory: "main.tf", one
// "modules/<name>/main.tf" per local module, and the project's companion files
// (templates/scripts referenced by file()/templatefile()).
//
// Companion files ride in this map deliberately: it is the single funnel into
// both store.Disk.persist (the on-disk project, hence Git) and every runner
// operation, so nothing downstream needs to know they exist. They are written
// LAST so a bad path can never displace generated HCL — paths are validated on
// the way in (model.ValidateFilePath), and this is the backstop.
func GenerateFiles(p *model.Project) (map[string][]byte, error) {
	root, err := Generate(p)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"main.tf": root}
	for _, lm := range p.LocalModules {
		mf, err := GenerateModuleFile(lm)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", lm.Name, err)
		}
		files["modules/"+lm.Name+"/main.tf"] = mf
	}
	for _, f := range p.Files {
		clean, err := model.ValidateFilePath(f.Path)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", f.Path, err)
		}
		if _, taken := files[clean]; taken {
			return nil, fmt.Errorf("file %q: path is generated from the model", clean)
		}
		files[clean] = []byte(f.Content)
	}
	return files, nil
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

// writeBackendAttrs writes the backend block's settings. It differs from
// writeAttrs in one way: a **secret** value is not emitted at all. Terraform
// evaluates a backend block before anything else exists, so it forbids
// variables there — the `var.<name>` the secret mode normally renders would be
// a hard parse error ("Variables may not be used here"), which is why a
// credential has to reach the backend through `-backend-config` at init
// instead (internal/api renders it into .arcloom.tfbackend). A comment is left
// in its place so the generated HCL still says the setting exists and where its
// value comes from; the secret name is not a secret.
func writeBackendAttrs(body *hclwrite.Body, attrs map[string]model.AttrValue) error {
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		v := attrs[name]
		if v.Mode == model.ModeSecret {
			body.AppendUnstructuredTokens(hclwrite.Tokens{{
				Type:  hclsyntax.TokenComment,
				Bytes: []byte("# " + name + " is supplied at init from secret " + strconv.Quote(v.SecretRef) + "\n"),
			}})
			continue
		}
		if err := setAttr(body, name, v); err != nil {
			return err
		}
	}
	return nil
}

// BackendSecretRefs maps the backend settings bound to a secret to their secret
// names, for the caller that resolves and renders them into the init-time
// backend config. Nil when the backend has none.
func BackendSecretRefs(b *model.Backend) map[string]string {
	if b == nil {
		return nil
	}
	var out map[string]string
	for name, v := range b.Config {
		if v.Mode == model.ModeSecret && v.SecretRef != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[name] = v.SecretRef
		}
	}
	return out
}

// setAttr writes one attribute, dispatching on value mode.
func setAttr(body *hclwrite.Body, name string, v model.AttrValue) error {
	// A plain literal keeps the SetAttributeValue path so all-literal output is
	// byte-for-byte unchanged. Everything else (references, expressions, secrets,
	// and structural collections containing them) is emitted as raw tokens.
	if v.Mode == model.ModeLiteral && v.Items == nil && v.Entries == nil {
		body.SetAttributeValue(name, v.Literal)
		return nil
	}
	toks, err := valueTokens(v)
	if err != nil {
		return fmt.Errorf("attribute %q: %w", name, err)
	}
	body.SetAttributeRaw(name, toks)
	return nil
}

// valueTokens renders any AttrValue (at any nesting depth) to HCL tokens,
// dispatching on mode. Structural collections (Items/Entries) recurse so a
// reference or secret can appear inside a list/map/object.
func valueTokens(v model.AttrValue) (hclwrite.Tokens, error) {
	switch v.Mode {
	case model.ModeLiteral:
		switch {
		case v.Items != nil:
			elems := make([]hclwrite.Tokens, len(v.Items))
			for i, it := range v.Items {
				t, err := valueTokens(it)
				if err != nil {
					return nil, err
				}
				elems[i] = t
			}
			return hclwrite.TokensForTuple(elems), nil
		case v.Entries != nil:
			// Sort by key so output is deterministic, matching writeAttrs.
			entries := append([]model.AttrEntry(nil), v.Entries...)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
			attrs := make([]hclwrite.ObjectAttrTokens, len(entries))
			for i, e := range entries {
				vt, err := valueTokens(e.Value)
				if err != nil {
					return nil, err
				}
				attrs[i] = hclwrite.ObjectAttrTokens{
					Name:  hclwrite.TokensForValue(cty.StringVal(e.Key)),
					Value: vt,
				}
			}
			return hclwrite.TokensForObject(attrs), nil
		default:
			return hclwrite.TokensForValue(v.Literal), nil
		}
	case model.ModeReference:
		trav, err := parseTraversal(v.Ref)
		if err != nil {
			return nil, err
		}
		return hclwrite.TokensForTraversal(trav), nil
	case model.ModeExpression:
		return tokensForExpr(v.Expr)
	case model.ModeSecret:
		// A secret is referenced as an input variable; its value is injected as
		// TF_VAR_<name> at run time and never written to HCL.
		trav, err := parseTraversal("var." + model.SecretVarName(v.SecretRef))
		if err != nil {
			return nil, err
		}
		return hclwrite.TokensForTraversal(trav), nil
	default:
		return nil, fmt.Errorf("unknown value mode %q", v.Mode)
	}
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

// ValidateExpr reports whether a string is a well-formed HCL expression, for
// callers that accept raw expressions (count / for_each) and want to reject
// bad input at the API boundary instead of at generation time.
func ValidateExpr(expr string) error {
	_, err := tokensForExpr(expr)
	return err
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
