package model

import "strings"

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
	// ModelVersion dates the stored document (see ModelVersion / Normalize).
	// Storage-only: the UI neither sees nor sets it, and the store stamps it on
	// every write, so anything this binary has ever written is current.
	ModelVersion int `json:"-" yaml:"modelVersion,omitempty"`
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
	// Files are user-authored companion files (cloud-init templates, scripts,
	// JSON policy documents) that HCL references via file()/templatefile().
	// They live in the model — not loose on disk — so they ride along with
	// generation, Git and undo like everything else the UI edits.
	Files []ProjectFile `json:"files,omitempty" yaml:"files,omitempty"`
}

// ProjectFile is a companion file emitted next to the generated HCL. Path is
// relative to the generated/ directory (and therefore to the run workspace's
// root module), so "templates/cloud-init.tftpl" is reachable as
// `${path.module}/templates/cloud-init.tftpl`, and "modules/<name>/x.tftpl"
// resolves under that local module's own path.module.
//
// Content is text: these are templates and scripts, and the model is YAML/JSON
// all the way down. Binary payloads are deliberately unsupported.
type ProjectFile struct {
	Path    string `json:"path" yaml:"path"`
	Content string `json:"content" yaml:"content"`
}

// LocalModule is a child module authored in-project as a subfolder
// (modules/<name>/) — a DEFINITION only. It has its own
// resources/variables/outputs and nothing else; whether it is used, and how
// many times, is described by the Module calls in Project.Modules that point at
// "./modules/<name>". Defining a module is not using it, and one definition can
// back any number of calls under different names.
type LocalModule struct {
	Name        string     `json:"name" yaml:"name"`
	Variables   []Variable `json:"variables,omitempty" yaml:"variables,omitempty"`
	Locals      []Local    `json:"locals,omitempty" yaml:"locals,omitempty"`
	DataSources []Resource `json:"dataSources,omitempty" yaml:"dataSources,omitempty"`
	Resources   []Resource `json:"resources" yaml:"resources"`
	Outputs     []Output   `json:"outputs,omitempty" yaml:"outputs,omitempty"`

	// LegacyInputs/LegacyCount/LegacyForEach are the pre-split shape, when a
	// LocalModule carried its one and only call. They are read from stored
	// YAML so an existing project still migrates (Project.Normalize turns them
	// into a real Module call and clears them) and are never written again.
	// Nothing outside Normalize may read them.
	LegacyInputs  map[string]AttrValue `json:"-" yaml:"inputs,omitempty"`
	LegacyCount   string               `json:"-" yaml:"count,omitempty"`
	LegacyForEach string               `json:"-" yaml:"forEach,omitempty"`
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

// Module is one `module "<Name>" { source = ... }` call in the root scope —
// a USE of a module, and the only thing that puts a module into the
// configuration. Source points either at a local definition authored in this
// project ("./modules/team", see LocalModule) or at a registry/remote address;
// both kinds live in this one list because they generate the same block and
// share the `module "<name>"` namespace.
//
// Name is the block label and therefore the address (module.<Name>), which is
// deliberately independent of the definition: calling one local module three
// times is three entries here, all with the same Source and different Names.
type Module struct {
	Name    string `json:"name" yaml:"name"`
	Source  string `json:"source" yaml:"source"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Config holds the arguments passed to the module, keyed by the module's
	// variable names.
	Config map[string]AttrValue `json:"config,omitempty" yaml:"config,omitempty"`
	// Count and ForEach are the count / for_each meta-arguments, stored as raw
	// HCL expressions ("var.teams", `toset(["a", "b"])`). At most one may be
	// set. Without these fields an imported for_each survives only as a fake
	// Config entry, which the inputs form then silently deletes.
	Count   string `json:"count,omitempty" yaml:"count,omitempty"`
	ForEach string `json:"forEach,omitempty" yaml:"forEach,omitempty"`
}

// LocalSourcePrefix is the source prefix of a call targeting a definition
// authored in this project.
const LocalSourcePrefix = "./modules/"

// LocalSource is the Source a call to the named local module uses.
func LocalSource(folder string) string { return LocalSourcePrefix + folder }

// LocalFolder returns the modules/<folder> definition this call targets, or ""
// when Source is a registry/remote address. It is the only sanctioned way to
// go from a call back to its definition — the call's Name says nothing about
// which module it uses.
func (m Module) LocalFolder() string {
	if !strings.HasPrefix(m.Source, LocalSourcePrefix) {
		return ""
	}
	folder := strings.TrimPrefix(m.Source, LocalSourcePrefix)
	// Only a direct child of modules/ is a definition this project owns;
	// "./modules/a/b" is some deeper path we don't model.
	if folder == "" || strings.Contains(folder, "/") {
		return ""
	}
	return folder
}

// CallsFor returns the calls that use the named local module definition.
func (p *Project) CallsFor(folder string) []Module {
	var out []Module
	for _, m := range p.Modules {
		if m.LocalFolder() == folder {
			out = append(out, m)
		}
	}
	return out
}

// ModelVersion is the shape of a stored project document. It exists to date a
// document, not to gate features: a migration that has to tell "this predates
// the change" from "this is current and deliberately empty" cannot do it by
// inspecting content. Bump it when a migration in Normalize needs that
// distinction.
//
//	1 (implicit, no field) — a LocalModule carried its own single call
//	2                      — module calls are separate (Project.Modules)
const ModelVersion = 2

// Normalize migrates a project loaded from disk into the current shape and is
// idempotent, so it can run on every read.
//
// The one migration it performs: a LocalModule used to BE its own single call,
// carrying the inputs and count/for_each the root passed it, with the block
// label pinned to the folder name. Calls are now separate (Project.Modules),
// which is what lets a definition be used zero times or three. For every
// version-1 definition with no call pointing at it, synthesize the call it used
// to imply — same name, same arguments — so an existing project generates
// byte-identical HCL. The legacy fields are then cleared and never written again.
//
// The version check is load-bearing, NOT an optimization. "A definition with no
// call" means two opposite things either side of the split: before it, a call
// that has not been extracted yet; after it, a module deliberately defined and
// not used. Without the version this would helpfully re-create a call every time
// the user deleted the last one, and an unused module would be unrepresentable —
// which is the state the split exists to make possible.
func (p *Project) Normalize() {
	if p == nil {
		return
	}
	// Legacy fields never survive a load, whatever the version.
	if p.ModelVersion >= ModelVersion {
		for i := range p.LocalModules {
			lm := &p.LocalModules[i]
			lm.LegacyInputs, lm.LegacyCount, lm.LegacyForEach = nil, "", ""
		}
		return
	}
	defer func() { p.ModelVersion = ModelVersion }()

	used := make(map[string]bool, len(p.Modules))
	for _, m := range p.Modules {
		if f := m.LocalFolder(); f != "" {
			used[f] = true
		}
	}
	taken := make(map[string]bool, len(p.Modules))
	for _, m := range p.Modules {
		taken[m.Name] = true
	}
	for i := range p.LocalModules {
		lm := &p.LocalModules[i]
		// Read the legacy call off the definition, then strip it either way —
		// a definition that already has a call must not keep a stale copy.
		inputs, count, forEach := lm.LegacyInputs, lm.LegacyCount, lm.LegacyForEach
		lm.LegacyInputs, lm.LegacyCount, lm.LegacyForEach = nil, "", ""
		// The name is free only if no call already claims it; a project whose
		// calls were imported can legitimately have both.
		if used[lm.Name] || taken[lm.Name] {
			continue
		}
		p.Modules = append(p.Modules, Module{
			Name:    lm.Name,
			Source:  LocalSource(lm.Name),
			Config:  inputs,
			Count:   count,
			ForEach: forEach,
		})
		used[lm.Name], taken[lm.Name] = true, true
	}
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
