package model

// Engine identifies the CLI backend used to run a project.
type Engine string

const (
	EngineTofu      Engine = "tofu"
	EngineTerraform Engine = "terraform"
)

// Backend configures remote state storage, emitted as
// terraform { backend "<type>" { ... } }. Config values must be literal-ish
// expressions (Terraform forbids interpolation in backend blocks); credentials
// should come from the server's environment, not from config.
type Backend struct {
	Type   string               `json:"type" yaml:"type"` // "s3", "gcs", "http", "pg", "local", ...
	Config map[string]AttrValue `json:"config,omitempty" yaml:"config,omitempty"`
}

// Project is the top-level container the UI edits.
type Project struct {
	ID     string `json:"id" yaml:"id"`
	Name   string `json:"name" yaml:"name"`
	Engine Engine `json:"engine" yaml:"engine"`
	// RequiredVersion is the terraform block's required_version constraint
	// (e.g. ">= 1.6.0"). Empty omits the attribute.
	RequiredVersion string `json:"requiredVersion,omitempty" yaml:"required_version,omitempty"`
	// Backend is nil for the default local state in the run/ workspace.
	Backend      *Backend           `json:"backend,omitempty" yaml:"backend,omitempty"`
	Providers    []ProviderInstance `json:"providers" yaml:"providers"`
	Variables    []Variable         `json:"variables,omitempty" yaml:"variables,omitempty"`
	Locals       []Local            `json:"locals,omitempty" yaml:"locals,omitempty"`
	DataSources  []Resource         `json:"dataSources,omitempty" yaml:"dataSources,omitempty"`
	Resources    []Resource         `json:"resources" yaml:"resources"`
	Modules      []Module           `json:"modules,omitempty" yaml:"modules,omitempty"`
	LocalModules []LocalModule      `json:"localModules,omitempty" yaml:"localModules,omitempty"`
	Outputs      []Output           `json:"outputs,omitempty" yaml:"outputs,omitempty"`
}

// LocalModule is a child module authored in-project as a subfolder
// (modules/<name>/). It has its own resources/variables/outputs; the root
// generates a `module "<name>" { source = "./modules/<name>" ... }` call whose
// arguments come from Inputs.
type LocalModule struct {
	Name        string     `json:"name" yaml:"name"`
	Variables   []Variable `json:"variables,omitempty" yaml:"variables,omitempty"`
	Locals      []Local    `json:"locals,omitempty" yaml:"locals,omitempty"`
	DataSources []Resource `json:"dataSources,omitempty" yaml:"dataSources,omitempty"`
	Resources   []Resource `json:"resources" yaml:"resources"`
	Outputs     []Output   `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	// Inputs are the values the root passes to this module (keyed by the
	// module's variable names).
	Inputs map[string]AttrValue `json:"inputs,omitempty" yaml:"inputs,omitempty"`
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
	// Alias distinguishes additional instances of the same provider (multi-region
	// AWS etc.); empty for the default instance. Resources select an aliased
	// instance via their Provider field ("aws.euwest").
	Alias string `json:"alias,omitempty" yaml:"alias,omitempty"`
	// Source is the registry address, e.g. "hashicorp/random".
	Source string `json:"source" yaml:"source"`
	// Version is an optional version constraint; empty means latest.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Config is the provider block body (empty for the random provider).
	Config map[string]AttrValue `json:"config,omitempty" yaml:"config,omitempty"`
}

// Address is the reference resources use to select this instance:
// "aws" for the default, "aws.euwest" for an alias.
func (pi ProviderInstance) Address() string {
	if pi.Alias == "" {
		return pi.Key
	}
	return pi.Key + "." + pi.Alias
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
	// Count and ForEach are the count / for_each meta-arguments, stored as raw
	// HCL expressions ("3", "var.replicas", `toset(["a", "b"])`). At most one
	// may be set.
	Count   string `json:"count,omitempty" yaml:"count,omitempty"`
	ForEach string `json:"forEach,omitempty" yaml:"forEach,omitempty"`
	// Provider is the provider meta-argument selecting a non-default provider
	// instance, e.g. "aws.euwest". Empty uses the type's default provider.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
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
