package model

// Engine identifies the CLI backend used to run a project.
type Engine string

const (
	EngineTofu      Engine = "tofu"
	EngineTerraform Engine = "terraform"
)

// Project is the top-level container the UI edits.
type Project struct {
	ID        string             `json:"id" yaml:"id"`
	Name      string             `json:"name" yaml:"name"`
	Engine    Engine             `json:"engine" yaml:"engine"`
	Providers []ProviderInstance `json:"providers" yaml:"providers"`
	Resources []Resource         `json:"resources" yaml:"resources"`
}

// ProviderInstance is a configured provider within a project.
type ProviderInstance struct {
	// Key is the local provider name used in HCL, e.g. "random".
	Key string `json:"key" yaml:"key"`
	// Source is the registry address, e.g. "hashicorp/random".
	Source string `json:"source" yaml:"source"`
	// Version is an optional version constraint; empty means latest.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Config is the provider block body (empty for the random provider).
	Config map[string]AttrValue `json:"config,omitempty" yaml:"config,omitempty"`
}

// Resource is a single managed resource in the model. It doubles as a generic
// nested-block body via the Blocks field so the generator recursion is ready
// for providers that use nested blocks.
type Resource struct {
	ID     string                `json:"id" yaml:"id"`
	Type   string                `json:"type" yaml:"type"`
	Name   string                `json:"name" yaml:"name"`
	Attrs  map[string]AttrValue  `json:"attrs" yaml:"attrs"`
	Blocks map[string][]Resource `json:"blocks,omitempty" yaml:"blocks,omitempty"`
	// DependsOn holds explicit dependency addresses ("aws_vpc.main"), emitted as
	// the depends_on meta-argument.
	DependsOn []string `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
}

// LocalName returns the provider local name inferred from a resource type,
// e.g. "random_password" -> "random".
func ProviderLocalName(resourceType string) string {
	for i := 0; i < len(resourceType); i++ {
		if resourceType[i] == '_' {
			return resourceType[:i]
		}
	}
	return resourceType
}
