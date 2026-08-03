package hclgen

import (
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

func projectWithModule(lm model.LocalModule, call model.Module, providers ...model.ProviderInstance) *model.Project {
	if len(providers) == 0 {
		providers = []model.ProviderInstance{{Key: "opennebula", Source: "OpenNebula/opennebula", Version: "~> 1.4"}}
	}
	return &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Providers:    providers,
		LocalModules: []model.LocalModule{lm},
		Modules:      []model.Module{call},
	}
}

// The regression. A local module with no required_providers of its own makes
// the engine resolve `opennebula_*` to hashicorp/opennebula — a provider nobody
// configured — and the plan fails with "Missing required attribute ... on
// provider[...] with no configuration" for every required argument.
func TestGenerateFiles_LocalModuleDeclaresItsProviders(t *testing.T) {
	p := projectWithModule(
		model.LocalModule{Name: "vms", Resources: []model.Resource{
			{ID: "r1", Type: "opennebula_virtual_machine", Name: "vm"},
		}},
		model.Module{Name: "vms", Source: model.LocalSource("vms")},
	)
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tf, ok := files["modules/vms/terraform.tf"]
	if !ok {
		t.Fatalf("the module folder has no terraform.tf; files: %v", keysOf(files))
	}
	got := string(tf)
	if !strings.Contains(got, `source = "OpenNebula/opennebula"`) {
		t.Errorf("module does not declare the root's source:\n%s", got)
	}
	// The version constraint belongs to the root: repeating it in the child
	// turns an ordinary root-side bump into a conflict between two files.
	if strings.Contains(got, "version") {
		t.Errorf("child module must not pin a version:\n%s", got)
	}
}

// A provider ArcLoom cannot name must not be guessed at — omitting the entry
// reproduces the engine's own default instead of pinning the module to a source
// that may be wrong.
func TestGenerateFiles_UnknownProviderIsNotInvented(t *testing.T) {
	p := projectWithModule(
		model.LocalModule{Name: "vms", Resources: []model.Resource{
			{ID: "r1", Type: "mystery_thing", Name: "x"},
		}},
		model.Module{Name: "vms", Source: model.LocalSource("vms")},
	)
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if tf, ok := files["modules/vms/terraform.tf"]; ok {
		t.Fatalf("expected no terraform.tf for an unknown provider, got:\n%s", tf)
	}
}

// An aliased provider is never inherited: the module must declare it with
// configuration_aliases and the call must pass it. Passing `providers` also
// REPLACES inheritance wholesale, so the default has to ride along.
func TestGenerateFiles_AliasNeedsDeclarationAndPassing(t *testing.T) {
	p := projectWithModule(
		model.LocalModule{Name: "vms", Resources: []model.Resource{
			{ID: "r1", Type: "opennebula_virtual_machine", Name: "vm", Provider: "opennebula.lab"},
			{ID: "r2", Type: "opennebula_image", Name: "img"},
		}},
		model.Module{Name: "lab", Source: model.LocalSource("vms")},
		model.ProviderInstance{Key: "opennebula", Source: "OpenNebula/opennebula"},
		model.ProviderInstance{Key: "opennebula", Alias: "lab", Source: "OpenNebula/opennebula"},
	)
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tf := string(files["modules/vms/terraform.tf"])
	if !strings.Contains(tf, "configuration_aliases = [opennebula.lab]") {
		t.Errorf("module does not declare the alias:\n%s", tf)
	}
	main := string(files["main.tf"])
	for _, want := range []string{"providers = {", "opennebula     = opennebula", "opennebula.lab = opennebula.lab"} {
		if !strings.Contains(main, want) {
			t.Errorf("call is missing %q:\n%s", want, main)
		}
	}
}

// No alias, no `providers` argument: default inheritance is already right, and
// emitting one would change every existing project's output for nothing.
func TestGenerateFiles_NoAliasMeansNoProvidersArgument(t *testing.T) {
	p := projectWithModule(
		model.LocalModule{Name: "vms", Resources: []model.Resource{
			{ID: "r1", Type: "opennebula_virtual_machine", Name: "vm"},
		}},
		model.Module{Name: "vms", Source: model.LocalSource("vms")},
	)
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(string(files["main.tf"]), "providers") {
		t.Errorf("unexpected providers argument:\n%s", files["main.tf"])
	}
}

// A mapping the config already had wins over the derived one: it may be
// deliberately non-identity, and re-deriving would repoint the module at a
// different account or region.
func TestGenerateFiles_StoredProvidersMappingWins(t *testing.T) {
	call := model.Module{Name: "lab", Source: model.LocalSource("vms"),
		Providers: "{\n  opennebula = opennebula.lab\n}"}
	p := projectWithModule(
		model.LocalModule{Name: "vms", Resources: []model.Resource{
			{ID: "r1", Type: "opennebula_virtual_machine", Name: "vm", Provider: "opennebula.lab"},
		}},
		call,
		model.ProviderInstance{Key: "opennebula", Source: "OpenNebula/opennebula"},
		model.ProviderInstance{Key: "opennebula", Alias: "lab", Source: "OpenNebula/opennebula"},
	)
	files, err := GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	main := string(files["main.tf"])
	if !strings.Contains(main, "opennebula = opennebula.lab") {
		t.Errorf("stored mapping was not used:\n%s", main)
	}
	if strings.Contains(main, "opennebula.lab = opennebula.lab") {
		t.Errorf("derived mapping leaked in alongside the stored one:\n%s", main)
	}
}

// An empty module still emits only its main.tf stub — the folder has to exist,
// but there is no provider to declare.
func TestGenerateFiles_EmptyModuleGetsNoTerraformBlock(t *testing.T) {
	files, err := GenerateModuleFiles(model.LocalModule{Name: "empty"}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected only main.tf, got %v", keysOf(files))
	}
	if _, ok := files[FileMain]; !ok {
		t.Errorf("expected the main.tf stub, got %v", keysOf(files))
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
