package hclimport

import (
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func providerByAddr(res *Result, addr string) *model.ProviderInstance {
	for i := range res.Providers {
		if res.Providers[i].Address() == addr {
			return &res.Providers[i]
		}
	}
	return nil
}

// A real module folder carries its own required_providers, and it is the only
// place the module's provider SOURCES are written down. Reading them upgrades
// the `hashicorp/<key>` placeholder a resource-derived registration leaves
// behind — which is the very source that makes the plan fail.
func TestImport_ModuleRequiredProvidersRefinesSource(t *testing.T) {
	files := map[string][]byte{
		"main.tf": []byte(`module "vms" { source = "./modules/vms" }`),
		"modules/vms/terraform.tf": []byte(`
terraform {
  required_providers {
    opennebula = { source = "OpenNebula/opennebula" }
  }
}
`),
		"modules/vms/main.tf": []byte(`resource "opennebula_virtual_machine" "vm" {}`),
	}
	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := providerByAddr(res, "opennebula")
	if p == nil {
		t.Fatalf("provider not registered: %+v", res.Providers)
	}
	if p.Source != "OpenNebula/opennebula" {
		t.Errorf("source = %q, want the module's declaration, not a placeholder", p.Source)
	}
}

// The root's own terraform block is authoritative, and file order must not
// decide which declaration lands.
func TestImport_ModuleDoesNotOverrideRootSource(t *testing.T) {
	files := map[string][]byte{
		"terraform.tf": []byte(`
terraform {
  required_providers {
    opennebula = { source = "OpenNebula/opennebula", version = "~> 1.4" }
  }
}
`),
		"modules/vms/terraform.tf": []byte(`
terraform {
  required_providers {
    opennebula = { source = "somebody/else" }
  }
}
`),
		"modules/vms/main.tf": []byte(`resource "opennebula_virtual_machine" "vm" {}`),
	}
	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := providerByAddr(res, "opennebula")
	if p == nil || p.Source != "OpenNebula/opennebula" || p.Version != "~> 1.4" {
		t.Fatalf("root declaration lost: %+v", res.Providers)
	}
}

// configuration_aliases holds provider TRAVERSALS, which have no value — so
// evaluating the entry failed and dropped it whole, source and all. Exactly the
// modules that need importing most (they use an aliased provider) were the ones
// whose source went missing.
func TestImport_ConfigurationAliasesKeepSourceAndRegisterAlias(t *testing.T) {
	files := map[string][]byte{
		"modules/vms/terraform.tf": []byte(`
terraform {
  required_providers {
    opennebula = {
      source                = "OpenNebula/opennebula"
      configuration_aliases = [opennebula.lab]
    }
  }
}
`),
		"modules/vms/main.tf": []byte(`resource "opennebula_virtual_machine" "vm" { provider = opennebula.lab }`),
	}
	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	def := providerByAddr(res, "opennebula")
	if def == nil || def.Source != "OpenNebula/opennebula" {
		t.Fatalf("source lost to configuration_aliases: %+v", res.Providers)
	}
	if providerByAddr(res, "opennebula.lab") == nil {
		t.Errorf("aliased instance not registered: %+v", res.Providers)
	}
}

// A `providers` mapping is a meta-argument. Left in Config it becomes a fake
// module input, which the inputs form (rebuilt from the module's declared
// variables) deletes on the first save — silently unwiring the module.
func TestImport_ProvidersIsAMetaArgNotAnInput(t *testing.T) {
	files := map[string][]byte{
		"main.tf": []byte(`
module "lab" {
  source = "./modules/vms"
  providers = {
    opennebula = opennebula.lab
  }
  size = 3
}
`),
		"modules/vms/main.tf": []byte(`resource "opennebula_virtual_machine" "vm" {}`),
	}
	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Modules) != 1 {
		t.Fatalf("expected 1 call, got %d", len(res.Modules))
	}
	m := res.Modules[0]
	if _, leaked := m.Config["providers"]; leaked {
		t.Error("providers leaked into the module's inputs")
	}
	if !strings.Contains(m.Providers, "opennebula.lab") {
		t.Errorf("providers meta-arg not captured: %q", m.Providers)
	}
	if _, ok := m.Config["size"]; !ok {
		t.Errorf("real inputs must survive: %+v", m.Config)
	}
}

// End to end: what generation now emits must import back to the same wiring,
// so a project that round-trips through Git or the HCL import keeps working.
func TestRoundTrip_ModuleProviderWiring(t *testing.T) {
	p := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "opennebula", Source: "OpenNebula/opennebula", Version: "~> 1.4"},
			{Key: "opennebula", Alias: "lab", Source: "OpenNebula/opennebula"},
		},
		LocalModules: []model.LocalModule{{Name: "vms", Resources: []model.Resource{
			{ID: "r1", Type: "opennebula_virtual_machine", Name: "vm", Provider: "opennebula.lab"},
		}}},
		Modules: []model.Module{{Name: "lab", Source: model.LocalSource("vms")}},
	}
	files, err := hclgen.GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	res, err := Parse(files, ids())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def := providerByAddr(res, "opennebula"); def == nil || def.Source != "OpenNebula/opennebula" {
		t.Fatalf("default instance lost: %+v", res.Providers)
	}
	if providerByAddr(res, "opennebula.lab") == nil {
		t.Fatalf("aliased instance lost: %+v", res.Providers)
	}
	if len(res.Modules) != 1 || !strings.Contains(res.Modules[0].Providers, "opennebula.lab") {
		t.Fatalf("providers mapping lost: %+v", res.Modules)
	}
	// And re-generating from the imported model reproduces the wiring.
	p2 := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Providers: res.Providers, LocalModules: res.LocalModules, Modules: res.Modules,
	}
	again, err := hclgen.GenerateFiles(p2)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if !strings.Contains(string(again["modules/vms/terraform.tf"]), "configuration_aliases = [opennebula.lab]") {
		t.Errorf("alias declaration did not survive:\n%s", again["modules/vms/terraform.tf"])
	}
}
