package hclgen

import (
	"sort"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// CollectSecretRefs returns the unique, sorted set of secret references
// (ModeSecret) used anywhere in the project.
func CollectSecretRefs(p *model.Project) []string {
	set := map[string]bool{}
	// Walk recurses into structural collections (Items/Entries) so a secret used
	// inside a list/map/object is still discovered and its var auto-declared.
	visit := func(av model.AttrValue) {
		av.Walk(func(n model.AttrValue) {
			if n.Mode == model.ModeSecret && n.SecretRef != "" {
				set[n.SecretRef] = true
			}
		})
	}
	visitAttrs := func(attrs map[string]model.AttrValue) {
		for _, av := range attrs {
			visit(av)
		}
	}

	for _, prov := range p.Providers {
		visitAttrs(prov.Config)
	}
	for _, v := range p.Variables {
		if v.Default != nil {
			visit(*v.Default)
		}
	}
	for _, l := range p.Locals {
		visit(l.Value)
	}
	for _, o := range p.Outputs {
		visit(o.Value)
	}
	for _, m := range p.Modules {
		visitAttrs(m.Config)
	}
	for _, r := range p.DataSources {
		visitResource(r, visit)
	}
	for _, r := range p.Resources {
		visitResource(r, visit)
	}

	out := make([]string, 0, len(set))
	for ref := range set {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func visitResource(r model.Resource, visit func(model.AttrValue)) {
	for _, av := range r.Attrs {
		visit(av)
	}
	for _, nested := range r.Blocks {
		for _, nb := range nested {
			visitResource(nb, visit)
		}
	}
}
