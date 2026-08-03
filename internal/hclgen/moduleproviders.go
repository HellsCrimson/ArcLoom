package hclgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Provider wiring for LOCAL MODULES.
//
// A provider's local name is resolved to a source PER MODULE, and a module that
// declares no required_providers falls back to `hashicorp/<local name>`. Provider
// CONFIGURATION is then inherited by resolved address, not by name. So a module
// folder with no terraform block turned every resource of a non-hashicorp
// provider into a resource of a provider nobody configured:
//
//	Error: Missing required attribute
//	on provider["registry.opentofu.org/hashicorp/opennebula"] with no configuration
//	The attribute "password" is required, but no definition was found.
//
// The root's `provider "opennebula" { … }` block is real and correct — it just
// configures OpenNebula/opennebula, a different provider from the one the
// module's resources ended up bound to. Hence "with no configuration" and
// "(source code not available)": the engine invented that provider, so there is
// no source line to blame. Emitting the module's own required_providers is the
// fix, and it is derived rather than modelled — the root's provider instances
// already hold every source.
//
// The second half is ALIASES. An additional (aliased) provider configuration is
// never inherited: the module has to declare it with `configuration_aliases` and
// the caller has to pass it with `providers = { … }`. Without both, a resource
// inside a module that selects `opennebula.euwest` fails the same way.

// moduleNeeds is the provider configuration a local module's own blocks require:
// the local names they use, and the aliases they select by name.
type moduleNeeds struct {
	keys    []string            // sorted local names ("opennebula")
	aliases map[string][]string // local name -> sorted aliases ("euwest")
}

// hasAliases reports whether any alias is needed, which is what decides whether
// a call needs an explicit `providers` argument at all.
func (n moduleNeeds) hasAliases() bool { return len(n.aliases) > 0 }

// needsFor collects the provider names a local module's resources and data
// sources use. The local name comes from the block's TYPE
// (`opennebula_virtual_machine` -> `opennebula`), which is how the engine
// resolves it; the `provider` meta-arg adds the alias on top.
func needsFor(lm model.LocalModule) moduleNeeds {
	n := moduleNeeds{aliases: map[string][]string{}}
	seen := map[string]bool{}
	add := func(r model.Resource) {
		key := model.ProviderLocalName(r.Type)
		if key == "" {
			return
		}
		if !seen[key] {
			seen[key] = true
			n.keys = append(n.keys, key)
		}
		// `provider = opennebula.euwest` selects an additional configuration; a
		// bare `provider = opennebula` is just the default and needs nothing.
		ref := strings.TrimSpace(r.Provider)
		k, alias, ok := strings.Cut(ref, ".")
		if !ok || k == "" || alias == "" {
			return
		}
		if !seen[k] {
			seen[k] = true
			n.keys = append(n.keys, k)
		}
		for _, have := range n.aliases[k] {
			if have == alias {
				return
			}
		}
		n.aliases[k] = append(n.aliases[k], alias)
	}
	for _, r := range lm.Resources {
		add(r)
	}
	for _, d := range lm.DataSources {
		add(d)
	}
	sort.Strings(n.keys)
	for k := range n.aliases {
		sort.Strings(n.aliases[k])
	}
	if len(n.aliases) == 0 {
		n.aliases = nil
	}
	return n
}

// sourceFor returns the source the root declares for a provider local name.
// The DEFAULT instance is authoritative; aliased instances of the same key
// share its source. "" means the project does not know this provider, in which
// case the entry is omitted rather than guessed — omitting reproduces the
// engine's own default instead of pinning the module to a source that may be
// wrong.
func sourceFor(providers []model.ProviderInstance, key string) string {
	for _, p := range providers {
		if p.Key == key && p.Alias == "" && p.Source != "" {
			return p.Source
		}
	}
	for _, p := range providers {
		if p.Key == key && p.Source != "" {
			return p.Source
		}
	}
	return ""
}

// moduleTerraformFile renders a local module's terraform block, or nil when the
// module uses no provider ArcLoom can name (then it emits no file at all, like
// every other empty theme).
//
// No `version` here on purpose: the constraint belongs to the root, which is
// where a user can see and change it, and a child module repeating it turns an
// ordinary root-side bump into a conflict between two files.
func moduleTerraformFile(lm model.LocalModule, providers []model.ProviderInstance) ([]byte, error) {
	n := needsFor(lm)
	f := hclwrite.NewEmptyFile()
	tfBlock := f.Body().AppendNewBlock("terraform", nil)
	rp := tfBlock.Body().AppendNewBlock("required_providers", nil)
	written := 0
	for _, key := range n.keys {
		source := sourceFor(providers, key)
		if source == "" {
			continue
		}
		aliases := n.aliases[key]
		if len(aliases) == 0 {
			rp.Body().SetAttributeValue(key, cty.ObjectVal(map[string]cty.Value{
				"source": cty.StringVal(source),
			}))
			written++
			continue
		}
		// configuration_aliases holds TRAVERSALS (`opennebula.euwest`), not
		// strings, so the object cannot be a cty value — it goes out as raw
		// tokens.
		refs := make([]string, len(aliases))
		for i, a := range aliases {
			refs[i] = key + "." + a
		}
		toks, err := tokensForExpr(fmt.Sprintf(
			"{\n  source = %q\n  configuration_aliases = [%s]\n}",
			source, strings.Join(refs, ", "),
		))
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", key, err)
		}
		rp.Body().SetAttributeRaw(key, toks)
		written++
	}
	if written == 0 {
		return nil, nil
	}
	return f.Bytes(), nil
}

// callProviderArgs returns the `providers = { … }` text each module CALL needs,
// keyed by call name. Only calls whose definition selects an alias get one:
// without an alias the default inheritance is exactly right, and emitting the
// argument anyway would change every existing project's output for nothing.
//
// When one IS emitted it must be complete. Specifying `providers` REPLACES
// inheritance wholesale — the child then sees only what the map lists — so the
// default configuration of every provider the module uses is passed alongside
// the aliases.
//
// The mapping is identity (`opennebula.euwest = opennebula.euwest`): the model
// records that a resource inside the module selected the root's `euwest`
// instance, so that is the instance to hand it. A user who wants a different
// wiring imports (or writes) a `providers` argument, which Module.Providers
// keeps verbatim.
func callProviderArgs(p *model.Project) map[string]string {
	out := map[string]string{}
	byFolder := map[string]model.LocalModule{}
	for _, lm := range p.LocalModules {
		byFolder[lm.Name] = lm
	}
	for _, m := range p.Modules {
		folder := m.LocalFolder()
		if folder == "" {
			continue
		}
		lm, ok := byFolder[folder]
		if !ok {
			continue
		}
		n := needsFor(lm)
		if !n.hasAliases() {
			continue
		}
		var rows []string
		for _, key := range n.keys {
			if sourceFor(p.Providers, key) == "" {
				continue // not declared in the module's required_providers either
			}
			rows = append(rows, fmt.Sprintf("  %s = %s", key, key))
			for _, a := range n.aliases[key] {
				rows = append(rows, fmt.Sprintf("  %s.%s = %s.%s", key, a, key, a))
			}
		}
		if len(rows) == 0 {
			continue
		}
		out[m.Name] = "{\n" + strings.Join(rows, "\n") + "\n}"
	}
	return out
}
