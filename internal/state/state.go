// Package state reads Terraform/OpenTofu state files (v4) and extracts managed
// resources so existing infrastructure can be imported into the model.
package state

import (
	"encoding/json"
	"fmt"
	"sort"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// Resource is one managed resource read from a state file.
type Resource struct {
	Type string
	Name string
	// Module is the state's module path for the resource ("" = root,
	// "module.web", "module.a.module.b"). It decides which scope the resource
	// belongs to, so a module's resource is not imported as a root one.
	Module       string
	ProviderAddr string // e.g. provider["registry.terraform.io/hashicorp/aws"]
	Attributes   map[string]any
}

// stateFile is the subset of the tfstate v4 format we read.
type stateFile struct {
	Version   int `json:"version"`
	Resources []struct {
		Mode      string `json:"mode"` // "managed" | "data"
		Type      string `json:"type"`
		Name      string `json:"name"`
		Module    string `json:"module"` // "" = root module
		Provider  string `json:"provider"`
		Instances []struct {
			Attributes map[string]any `json:"attributes"`
		} `json:"instances"`
	} `json:"resources"`
}

// Parse reads a state file and returns its managed resources.
func Parse(data []byte) ([]Resource, []string, error) {
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, nil, fmt.Errorf("not valid state JSON: %w", err)
	}
	var out []Resource
	var warnings []string
	for _, r := range sf.Resources {
		if r.Mode != "managed" {
			warnings = append(warnings, fmt.Sprintf("%s.%s: %s resource skipped", r.Type, r.Name, r.Mode))
			continue
		}
		if len(r.Instances) == 0 {
			continue
		}
		if len(r.Instances) > 1 {
			warnings = append(warnings, fmt.Sprintf("%s.%s: only the first of %d instances imported", r.Type, r.Name, len(r.Instances)))
		}
		out = append(out, Resource{
			Type:         r.Type,
			Name:         r.Name,
			Module:       r.Module,
			ProviderAddr: r.Provider,
			Attributes:   r.Instances[0].Attributes,
		})
	}
	return out, warnings, nil
}

// SettableAttrs filters state attribute values to those that are user-settable
// per the schema (required or optional, not computed-only), converting each to a
// literal AttrValue. Nested-block attributes are skipped with a warning.
func SettableAttrs(rs *tfjson.Schema, attrs map[string]any) (map[string]model.AttrValue, []string) {
	out := map[string]model.AttrValue{}
	var warnings []string
	if rs == nil || rs.Block == nil {
		return out, []string{"schema unavailable; no attributes imported"}
	}

	// Deterministic iteration for stable warnings.
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		raw := attrs[name]
		if raw == nil {
			continue
		}
		sa, ok := rs.Block.Attributes[name]
		if !ok {
			// Might be a nested block rather than an attribute.
			if _, isBlock := rs.Block.NestedBlocks[name]; isBlock {
				warnings = append(warnings, fmt.Sprintf("attribute %q is a nested block; skipped (edit manually)", name))
			}
			continue
		}
		// Skip computed-only (server-assigned) values: setting them is invalid.
		if sa.Computed && !sa.Optional && !sa.Required {
			continue
		}
		av, err := model.LiteralFromNative(raw)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("attribute %q: %v", name, err))
			continue
		}
		out[name] = av
	}
	return out, warnings
}
