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

// The generated configuration is split into files by THEME, the way a
// hand-written project usually is. Terraform reads every *.tf in a directory
// and does not care which block sits where, so the split is purely for the
// reader — and for the preview pane, which shows one tab per file.
//
// A section that would be empty emits NO file (the rule imports.tf already
// followed): a minimal project is terraform.tf + main.tf, not eight stubs.
const (
	FileTerraform = "terraform.tf" // terraform{} + provider blocks
	FileVariables = "variables.tf" // variable blocks, incl. auto-declared secrets
	FileLocals    = "locals.tf"
	FileData      = "data.tf"
	FileMain      = "main.tf" // resources + module calls
	FileOutputs   = "outputs.tf"
	FileImports   = "imports.tf"
	FileMoved     = "moved.tf"
)

// blockSep returns a separator for one body: a blank line before every block
// except the first written into it. Sections share it when they are composed
// into ONE document (Generate) and get a fresh one per file (GenerateFiles),
// which is what keeps a per-theme file from starting with a blank line while
// the single-document form stays byte-identical to what it always emitted.
func blockSep(body *hclwrite.Body) func() {
	first := true
	return func() {
		if !first {
			body.AppendNewline()
		}
		first = false
	}
}

// section renders one thematic file. Returns nil when nothing was written, so
// the caller emits no file at all.
func section(w func(body *hclwrite.Body, sep func()) error) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	if err := w(body, blockSep(body)); err != nil {
		return nil, err
	}
	out := hclwrite.Format(f.Bytes())
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, nil
	}
	return out, nil
}

// writeTerraformSection emits the terraform block and every provider block.
func writeTerraformSection(body *hclwrite.Body, p *model.Project, sep func()) error {
	sep()
	if err := writeTerraformBlock(body, p.Providers, p.Backend, p.RequiredVersion); err != nil {
		return err
	}
	for _, prov := range p.Providers {
		sep()
		if err := writeProviderBlock(body, prov); err != nil {
			return err
		}
	}
	return nil
}

// writeVariablesSection emits the declared variables plus one sensitive
// variable per referenced secret the user has not declared, so var.<name>
// resolves.
func writeVariablesSection(body *hclwrite.Body, vars []model.Variable, secretRefs []string, sep func()) error {
	declared := map[string]bool{}
	for _, v := range vars {
		declared[v.Name] = true
		sep()
		if err := writeVariableBlock(body, v); err != nil {
			return err
		}
	}
	for _, ref := range secretRefs {
		name := model.SecretVarName(ref)
		if declared[name] {
			continue
		}
		declared[name] = true
		sep()
		if err := writeVariableBlock(body, model.Variable{Name: name, Type: "string", Sensitive: true}); err != nil {
			return err
		}
	}
	return nil
}

func writeLocalsSection(body *hclwrite.Body, locals []model.Local, sep func()) error {
	if len(locals) == 0 {
		return nil
	}
	sep()
	return writeLocalsBlock(body, locals)
}

func writeDataSection(body *hclwrite.Body, data []model.Resource, sep func()) error {
	for _, d := range data {
		sep()
		if err := writeBlockWithKeyword(body, "data", d); err != nil {
			return err
		}
	}
	return nil
}

// writeResourcesSection emits resource blocks and module CALLS — one loop for
// every call, local ("./modules/x") and registry alike. A local module's
// *definition* generates its own folder (GenerateFiles) and emits nothing here:
// a definition that is never called is simply never used, exactly as in HCL.
func writeResourcesSection(body *hclwrite.Body, resources []model.Resource, modules []model.Module, sep func()) error {
	for _, r := range resources {
		sep()
		if err := writeBlockWithKeyword(body, "resource", r); err != nil {
			return err
		}
	}
	for _, m := range modules {
		sep()
		if err := writeModuleBlock(body, m); err != nil {
			return err
		}
	}
	return nil
}

func writeOutputsSection(body *hclwrite.Body, outputs []model.Output, sep func()) error {
	for _, o := range outputs {
		sep()
		if err := writeOutputBlock(body, o); err != nil {
			return err
		}
	}
	return nil
}

// Generate renders the whole root configuration as ONE document, in the
// conventional order: terraform{required_providers}, providers, variables,
// locals, data, resources, modules, outputs. GenerateFiles is what the project
// on disk uses — this is the single-document form, kept for callers that want
// the config as one blob (and for tests that assert on it end to end).
func Generate(p *model.Project) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	sep := blockSep(body)
	if err := writeTerraformSection(body, p, sep); err != nil {
		return nil, err
	}
	if err := writeVariablesSection(body, p.Variables, CollectSecretRefs(p), sep); err != nil {
		return nil, err
	}
	if err := writeLocalsSection(body, p.Locals, sep); err != nil {
		return nil, err
	}
	if err := writeDataSection(body, p.DataSources, sep); err != nil {
		return nil, err
	}
	if err := writeResourcesSection(body, p.Resources, p.Modules, sep); err != nil {
		return nil, err
	}
	if err := writeOutputsSection(body, p.Outputs, sep); err != nil {
		return nil, err
	}
	return hclwrite.Format(f.Bytes()), nil
}

// GenerateRootFiles renders the root configuration split by theme: the map the
// preview pane shows as tabs and the store writes into generated/. Keys are
// file names, and a theme with nothing in it is absent rather than empty.
func GenerateRootFiles(p *model.Project) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, s := range []struct {
		name string
		w    func(*hclwrite.Body, func()) error
	}{
		{FileTerraform, func(b *hclwrite.Body, sep func()) error { return writeTerraformSection(b, p, sep) }},
		{FileVariables, func(b *hclwrite.Body, sep func()) error {
			return writeVariablesSection(b, p.Variables, CollectSecretRefs(p), sep)
		}},
		{FileLocals, func(b *hclwrite.Body, sep func()) error { return writeLocalsSection(b, p.Locals, sep) }},
		{FileData, func(b *hclwrite.Body, sep func()) error { return writeDataSection(b, p.DataSources, sep) }},
		{FileMain, func(b *hclwrite.Body, sep func()) error {
			return writeResourcesSection(b, p.Resources, p.Modules, sep)
		}},
		{FileOutputs, func(b *hclwrite.Body, sep func()) error { return writeOutputsSection(b, p.Outputs, sep) }},
	} {
		content, err := section(s.w)
		if err != nil {
			return nil, err
		}
		if content != nil {
			out[s.name] = content
		}
	}
	// import / moved blocks render themselves whole, and already return nil when
	// the project has none.
	imports, err := GenerateImports(p)
	if err != nil {
		return nil, err
	}
	if imports != nil {
		out[FileImports] = imports
	}
	moved, err := GenerateMoved(p)
	if err != nil {
		return nil, err
	}
	if moved != nil {
		out[FileMoved] = moved
	}
	return out, nil
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
	return writeNestedBlocks(blk.Body(), r.Blocks)
}

// writeNestedBlocks writes a body's nested blocks, sorted by type for
// determinism (the order WITHIN a type is the model's, which is the order they
// were imported in).
//
// Labels are emitted and the recursion goes all the way down, which is what
// `dynamic` needs on both counts:
//
//	dynamic "ingress" {
//	  for_each = var.ports
//	  content { from_port = ingress.value }
//	}
//
// Writing `AppendNewBlock(n, nil)` instead produced `dynamic {`, which the
// engine rejects with "Missing type for dynamic; All dynamic blocks must have 1
// labels (type)", and stopping at depth one dropped the content block entirely.
// hclwrite renders each label with TokensForValue, so a label is quoted and
// escaped rather than pasted in.
func writeNestedBlocks(body *hclwrite.Body, blocks map[string][]model.Resource) error {
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, nested := range blocks[n] {
			nb := body.AppendNewBlock(n, nested.Labels)
			if err := writeAttrs(nb.Body(), nested.Attrs); err != nil {
				return fmt.Errorf("block %q: %w", n, err)
			}
			if err := writeNestedBlocks(nb.Body(), nested.Blocks); err != nil {
				return fmt.Errorf("block %q: %w", n, err)
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

// GenerateModuleFile renders a local module's own configuration as ONE document
// (variables, locals, data, resources, outputs) — no terraform{} or provider{}
// blocks, which a child module inherits. GenerateModuleFiles is the split form.
func GenerateModuleFile(lm model.LocalModule) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	sep := blockSep(body)
	if err := writeVariablesSection(body, lm.Variables, nil, sep); err != nil {
		return nil, err
	}
	if err := writeLocalsSection(body, lm.Locals, sep); err != nil {
		return nil, err
	}
	if err := writeDataSection(body, lm.DataSources, sep); err != nil {
		return nil, err
	}
	if err := writeResourcesSection(body, lm.Resources, nil, sep); err != nil {
		return nil, err
	}
	if err := writeOutputsSection(body, lm.Outputs, sep); err != nil {
		return nil, err
	}
	return hclwrite.Format(f.Bytes()), nil
}

// GenerateModuleFiles renders a local module's configuration split by theme,
// keyed relative to the module's own folder. Same rules as the root's
// (GenerateRootFiles), minus the sections a child module cannot have: no
// terraform/provider blocks, and no import/moved blocks — those address the
// root's state and belong to the root configuration.
//
// A module with nothing but resources therefore still generates exactly
// modules/<name>/main.tf, which is what it always did.
func GenerateModuleFiles(lm model.LocalModule) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, s := range []struct {
		name string
		w    func(*hclwrite.Body, func()) error
	}{
		{FileVariables, func(b *hclwrite.Body, sep func()) error {
			return writeVariablesSection(b, lm.Variables, nil, sep)
		}},
		{FileLocals, func(b *hclwrite.Body, sep func()) error { return writeLocalsSection(b, lm.Locals, sep) }},
		{FileData, func(b *hclwrite.Body, sep func()) error { return writeDataSection(b, lm.DataSources, sep) }},
		{FileMain, func(b *hclwrite.Body, sep func()) error { return writeResourcesSection(b, lm.Resources, nil, sep) }},
		{FileOutputs, func(b *hclwrite.Body, sep func()) error { return writeOutputsSection(b, lm.Outputs, sep) }},
	} {
		content, err := section(s.w)
		if err != nil {
			return nil, err
		}
		if content != nil {
			out[s.name] = content
		}
	}
	// A module with nothing in it still emits an (empty) main.tf, unlike the
	// root: the FOLDER has to exist, or `init` fails with "module not found" on
	// the first call to a module the user has only just created. The root needs
	// no such stub — its terraform.tf is always written.
	if len(out) == 0 {
		out[FileMain] = []byte{}
	}
	return out, nil
}

// GenerateImports renders the project's `import` blocks into their own file.
//
// Their own file on purpose: import blocks are TRANSIENT config — you add them,
// apply once, and delete them — so keeping them out of main.tf makes them easy
// to see, review and remove without reading around the resources they adopt.
// Returns nil when the project has none, and the caller then emits no file.
func GenerateImports(p *model.Project) ([]byte, error) {
	if len(p.Imports) == 0 {
		return nil, nil
	}
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	for i, imp := range p.Imports {
		if strings.TrimSpace(imp.To) == "" || strings.TrimSpace(imp.ImportID) == "" {
			return nil, fmt.Errorf("import block %d: `to` and `id` are required", i+1)
		}
		if i > 0 {
			body.AppendNewline()
		}
		blk := body.AppendNewBlock("import", nil)
		// for_each leads the block, as it does on resources and modules: it is
		// what the other two arguments are written in terms of.
		if e := strings.TrimSpace(imp.ForEach); e != "" {
			toks, err := tokensForExpr(e)
			if err != nil {
				return nil, fmt.Errorf("import block %d: for_each: %w", i+1, err)
			}
			blk.Body().SetAttributeRaw("for_each", toks)
		}
		if ref := strings.TrimSpace(imp.Provider); ref != "" {
			trav, err := parseTraversal(ref)
			if err != nil {
				return nil, fmt.Errorf("import block %d: provider: %w", i+1, err)
			}
			blk.Body().SetAttributeTraversal("provider", trav)
		}
		// `to` is an ADDRESS expression, not a string: with a for_each it carries
		// an index (`x.y[each.key]`), so it is lexed like any other expression.
		toToks, err := tokensForExpr(strings.TrimSpace(imp.To))
		if err != nil {
			return nil, fmt.Errorf("import block %d: to: %w", i+1, err)
		}
		blk.Body().SetAttributeRaw("to", toToks)
		idToks, err := tokensForExpr(strings.TrimSpace(imp.ImportID))
		if err != nil {
			return nil, fmt.Errorf("import block %d: id: %w", i+1, err)
		}
		blk.Body().SetAttributeRaw("id", idToks)
	}
	return hclwrite.Format(f.Bytes()), nil
}

// GenerateMoved renders the project's `moved` blocks into their own file.
//
// Own file for the same reason imports.tf is one: these are refactoring
// records, not infrastructure, and burying them among the resources makes them
// hard to find when they are eventually cleaned up. Returns nil when the
// project has none.
func GenerateMoved(p *model.Project) ([]byte, error) {
	if len(p.Moved) == 0 {
		return nil, nil
	}
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	for i, mv := range p.Moved {
		from, to := strings.TrimSpace(mv.From), strings.TrimSpace(mv.To)
		if from == "" || to == "" {
			return nil, fmt.Errorf("moved block %d: `from` and `to` are required", i+1)
		}
		if i > 0 {
			body.AppendNewline()
		}
		blk := body.AppendNewBlock("moved", nil)
		// Emitted as expressions, not strings: an address carries an index
		// (`x.y["www"]`), which is lexed like any other traversal.
		fromToks, err := tokensForExpr(from)
		if err != nil {
			return nil, fmt.Errorf("moved block %d: from: %w", i+1, err)
		}
		blk.Body().SetAttributeRaw("from", fromToks)
		toToks, err := tokensForExpr(to)
		if err != nil {
			return nil, fmt.Errorf("moved block %d: to: %w", i+1, err)
		}
		blk.Body().SetAttributeRaw("to", toToks)
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
	files, err := GenerateRootFiles(p)
	if err != nil {
		return nil, err
	}
	for _, lm := range p.LocalModules {
		mf, err := GenerateModuleFiles(lm)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", lm.Name, err)
		}
		for name, content := range mf {
			files["modules/"+lm.Name+"/"+name] = content
		}
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

// ValidateAddress reports whether a string is a static resource address —
// `null_resource.www`, `null_resource.sub["www"]`, `module.team["a"].x.y[0]`.
//
// Stricter than ValidateExpr on purpose: `moved` blocks take addresses the
// engine resolves BEFORE evaluating anything, so `x.y[each.key]` or a variable
// is refused by the engine itself. hclsyntax folds a literal index into the
// traversal and leaves a computed one as an index expression, so
// "is this an absolute traversal" is exactly the right test — and it fails at
// the API boundary with a clear message instead of on the next plan.
func ValidateAddress(addr string) error {
	expr, diags := hclsyntax.ParseExpression([]byte(addr), "addr.hcl", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return fmt.Errorf("invalid address: %s", diags.Error())
	}
	if _, diags := hcl.AbsTraversalForExpr(expr); diags.HasErrors() {
		return fmt.Errorf("not a static address (an index must be a literal, and expressions are not allowed here): %s", addr)
	}
	return nil
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
