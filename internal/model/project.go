package model

// Engine identifies the CLI backend used to run a project.
type Engine string

const (
	EngineTofu      Engine = "tofu"
	EngineTerraform Engine = "terraform"
)

// Project is the top-level container the UI edits.
type Project struct {
	ID          string             `json:"id" yaml:"id"`
	Name        string             `json:"name" yaml:"name"`
	Engine      Engine             `json:"engine" yaml:"engine"`
	Providers   []ProviderInstance `json:"providers" yaml:"providers"`
	Variables   []Variable         `json:"variables,omitempty" yaml:"variables,omitempty"`
	Locals      []Local            `json:"locals,omitempty" yaml:"locals,omitempty"`
	DataSources []Resource         `json:"dataSources,omitempty" yaml:"dataSources,omitempty"`
	Resources   []Resource         `json:"resources" yaml:"resources"`
	Modules     []Module           `json:"modules,omitempty" yaml:"modules,omitempty"`
	Outputs     []Output           `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}

// Variable is an input variable (variable "name" {}).
type Variable struct {
	Name        string     `json:"name" yaml:"name"`
	Type        string     `json:"type,omitempty" yaml:"type,omitempty"` // raw HCL type expr, e.g. "string"
	Default     *AttrValue `json:"default,omitempty" yaml:"default,omitempty"`
	Description string     `json:"description,omitempty" yaml:"description,omitempty"`
	Sensitive   bool       `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
}

// Local is a named local value (locals { name = value }).
type Local struct {
	Name  string    `json:"name" yaml:"name"`
	Value AttrValue `json:"value" yaml:"value"`
}

// Output is an output value (output "name" {}).
type Output struct {
	Name        string    `json:"name" yaml:"name"`
	Value       AttrValue `json:"value" yaml:"value"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
	Sensitive   bool      `json:"sensitive,omitempty" yaml:"sensitive,omitempty"`
}

// Module is a module call (module "name" {}).
type Module struct {
	Name    string               `json:"name" yaml:"name"`
	Source  string               `json:"source" yaml:"source"`
	Version string               `json:"version,omitempty" yaml:"version,omitempty"`
	Config  map[string]AttrValue `json:"config,omitempty" yaml:"config,omitempty"`
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
