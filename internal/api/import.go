package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/hclimport"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/state"
	"github.com/HellsCrimson/ArcLoom/internal/store"
)

// importHCLRequest carries one or more HCL files to import. Module targets a
// local module scope ("" = root project).
type importHCLRequest struct {
	Files  map[string]string `json:"files"` // filename -> contents
	Module string            `json:"module,omitempty"`
}

type importResponse struct {
	AddedResources   int      `json:"addedResources"`
	AddedProviders   int      `json:"addedProviders"`
	AddedVariables   int      `json:"addedVariables,omitempty"`
	AddedLocals      int      `json:"addedLocals,omitempty"`
	AddedOutputs     int      `json:"addedOutputs,omitempty"`
	AddedDataSources  int      `json:"addedDataSources,omitempty"`
	AddedModules      int      `json:"addedModules,omitempty"`
	AddedLocalModules int      `json:"addedLocalModules,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

// handleImportHCL parses submitted .tf files and merges the result into the
// project, then persists.
func (s *Server) handleImportHCL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req importHCLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(req.Files) == 0 {
		writeErr(w, http.StatusBadRequest, "no files provided")
		return
	}

	files := make(map[string][]byte, len(req.Files))
	for name, content := range req.Files {
		files[name] = []byte(content)
	}
	parsed, err := hclimport.Parse(files, uuid.NewString)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "parse: "+err.Error())
		return
	}

	resp := importResponse{Warnings: parsed.Warnings}
	err = s.store.Update(id, func(p *model.Project) error {
		// Providers always live at the project root, even when importing into a
		// module (a child module inherits the root's providers).
		resp.AddedProviders = mergeProviders(p, parsed.Providers)

		scope := &p.Resources
		vars, locals, outputs, data := &p.Variables, &p.Locals, &p.Outputs, &p.DataSources
		if req.Module != "" {
			lm := findLocalModule(p, req.Module)
			if lm == nil {
				return errModuleNotFound
			}
			scope = &lm.Resources
			vars, locals, outputs, data = &lm.Variables, &lm.Locals, &lm.Outputs, &lm.DataSources
			// Module-scope import can't hold provider/terraform or nested modules.
			if len(parsed.Modules) > 0 || len(parsed.LocalModules) > 0 {
				resp.Warnings = append(resp.Warnings, "nested modules are not supported inside a module and were skipped")
			}
		} else {
			resp.AddedModules = mergeModules(p, parsed.Modules)
			resp.AddedLocalModules = mergeLocalModules(p, parsed.LocalModules)
		}

		resp.AddedResources = mergeResourcesInto(scope, parsed.Resources)
		resp.AddedDataSources = mergeDataSourcesInto(data, parsed.DataSources)
		resp.AddedVariables = mergeVariablesInto(vars, parsed.Variables)
		resp.AddedLocals = mergeLocalsInto(locals, parsed.Locals)
		resp.AddedOutputs = mergeOutputsInto(outputs, parsed.Outputs)
		return nil
	})
	if errors.Is(err, errModuleNotFound) {
		writeErr(w, http.StatusNotFound, "module not found")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleImportState reads a Terraform/OpenTofu state file and imports its
// managed resources into the model, using each resource's schema to keep only
// user-settable (non computed-only) attributes so the generated config is valid.
func (s *Server) handleImportState(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		State string `json:"state"` // raw tfstate JSON
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	p, err := s.store.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	run, ok := s.runnerFor(string(p.Engine))
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}

	resources, warnings, err := state.Parse([]byte(req.State))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "parse state: "+err.Error())
		return
	}

	// For each state resource, fetch its schema and keep only settable attrs.
	imported := make([]model.Resource, 0, len(resources))
	for _, sr := range resources {
		source := providerSourceFromState(sr.ProviderAddr, sr.Type)
		schemas, serr := run.ProvidersSchema(r.Context(), []model.ProviderInstance{{
			Key: model.ProviderLocalName(sr.Type), Source: source,
		}})
		if serr != nil {
			warnings = append(warnings, fmt.Sprintf("%s.%s: schema unavailable (%v)", sr.Type, sr.Name, serr))
			continue
		}
		_, ps := findProviderSchema(schemas, source)
		if ps == nil {
			warnings = append(warnings, fmt.Sprintf("%s.%s: provider schema not found", sr.Type, sr.Name))
			continue
		}
		rs := ps.ResourceSchemas[sr.Type]
		attrs, w2 := state.SettableAttrs(rs, sr.Attributes)
		warnings = append(warnings, w2...)
		imported = append(imported, model.Resource{
			ID: uuid.NewString(), Type: sr.Type, Name: sr.Name, Attrs: attrs,
		})
	}

	// Collect provider instances for imported resources.
	provs := map[string]model.ProviderInstance{}
	for _, sr := range resources {
		key := model.ProviderLocalName(sr.Type)
		if _, ok := provs[key]; !ok {
			provs[key] = model.ProviderInstance{Key: key, Source: providerSourceFromState(sr.ProviderAddr, sr.Type)}
		}
	}
	provList := make([]model.ProviderInstance, 0, len(provs))
	for _, pi := range provs {
		provList = append(provList, pi)
	}

	resp := importResponse{Warnings: warnings}
	if err := s.store.Update(id, func(pr *model.Project) error {
		resp.AddedProviders = mergeProviders(pr, provList)
		resp.AddedResources = mergeResources(pr, imported)
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// mergeProviders adds providers not already present (by key). Real sources
// override placeholder "hashicorp/<key>" sources.
func mergeProviders(p *model.Project, incoming []model.ProviderInstance) int {
	added := 0
	for _, pi := range incoming {
		found := false
		for i := range p.Providers {
			if p.Providers[i].Key == pi.Key {
				found = true
				if p.Providers[i].Source == "hashicorp/"+pi.Key && pi.Source != "" {
					p.Providers[i].Source = pi.Source
				}
				if p.Providers[i].Version == "" && pi.Version != "" {
					p.Providers[i].Version = pi.Version
				}
				if len(p.Providers[i].Config) == 0 && len(pi.Config) > 0 {
					p.Providers[i].Config = pi.Config
				}
				break
			}
		}
		if !found {
			p.Providers = append(p.Providers, pi)
			added++
		}
	}
	return added
}

// errModuleNotFound is returned when an import targets a missing module scope.
var errModuleNotFound = errors.New("module not found")

// findLocalModule returns the named local module, or nil.
func findLocalModule(p *model.Project, name string) *model.LocalModule {
	for i := range p.LocalModules {
		if p.LocalModules[i].Name == name {
			return &p.LocalModules[i]
		}
	}
	return nil
}

// The merge helpers below operate on a scope's slice pointer so the same logic
// serves both the project root and a local module. The project-level wrappers
// preserve the existing call sites (state import, root HCL import).

// mergeDataSourcesInto appends imported data sources, renaming on (type,name) clash.
func mergeDataSourcesInto(dst *[]model.Resource, incoming []model.Resource) int {
	taken := map[string]bool{}
	for _, r := range *dst {
		taken[r.Type+"."+r.Name] = true
	}
	added := 0
	for _, r := range incoming {
		base := r.Name
		n := 1
		for taken[r.Type+"."+r.Name] {
			n++
			r.Name = fmt.Sprintf("%s_%d", base, n)
		}
		taken[r.Type+"."+r.Name] = true
		*dst = append(*dst, r)
		added++
	}
	return added
}

func mergeVariablesInto(dst *[]model.Variable, incoming []model.Variable) int {
	taken := map[string]bool{}
	for _, v := range *dst {
		taken[v.Name] = true
	}
	added := 0
	for _, v := range incoming {
		if taken[v.Name] {
			continue
		}
		taken[v.Name] = true
		*dst = append(*dst, v)
		added++
	}
	return added
}

func mergeLocalsInto(dst *[]model.Local, incoming []model.Local) int {
	taken := map[string]bool{}
	for _, l := range *dst {
		taken[l.Name] = true
	}
	added := 0
	for _, l := range incoming {
		if taken[l.Name] {
			continue
		}
		taken[l.Name] = true
		*dst = append(*dst, l)
		added++
	}
	return added
}

func mergeOutputsInto(dst *[]model.Output, incoming []model.Output) int {
	taken := map[string]bool{}
	for _, o := range *dst {
		taken[o.Name] = true
	}
	added := 0
	for _, o := range incoming {
		if taken[o.Name] {
			continue
		}
		taken[o.Name] = true
		*dst = append(*dst, o)
		added++
	}
	return added
}

func mergeModules(p *model.Project, incoming []model.Module) int {
	taken := map[string]bool{}
	for _, m := range p.Modules {
		taken[m.Name] = true
	}
	added := 0
	for _, m := range incoming {
		if taken[m.Name] {
			continue
		}
		taken[m.Name] = true
		p.Modules = append(p.Modules, m)
		added++
	}
	return added
}

// mergeLocalModules appends imported local modules, skipping any whose name is
// already present (by name).
func mergeLocalModules(p *model.Project, incoming []model.LocalModule) int {
	taken := map[string]bool{}
	for _, m := range p.LocalModules {
		taken[m.Name] = true
	}
	added := 0
	for _, m := range incoming {
		if taken[m.Name] {
			continue
		}
		taken[m.Name] = true
		p.LocalModules = append(p.LocalModules, m)
		added++
	}
	return added
}

// mergeResources appends imported resources, renaming on (type,name) collision.
func mergeResources(p *model.Project, incoming []model.Resource) int {
	return mergeResourcesInto(&p.Resources, incoming)
}

func mergeResourcesInto(dst *[]model.Resource, incoming []model.Resource) int {
	taken := map[string]bool{}
	for _, r := range *dst {
		taken[r.Type+"."+r.Name] = true
	}
	added := 0
	for _, r := range incoming {
		base := r.Name
		n := 1
		for taken[r.Type+"."+r.Name] {
			n++
			r.Name = fmt.Sprintf("%s_%d", base, n)
		}
		taken[r.Type+"."+r.Name] = true
		*dst = append(*dst, r)
		added++
	}
	return added
}

// providerSourceFromState derives a registry source from a state provider
// address like `provider["registry.terraform.io/hashicorp/aws"]`, falling back
// to hashicorp/<localname>.
func providerSourceFromState(providerAddr, resourceType string) string {
	// providerAddr example: provider["registry.terraform.io/hashicorp/aws"]
	start := indexByte(providerAddr, '"')
	end := lastIndexByte(providerAddr, '"')
	if start >= 0 && end > start {
		full := providerAddr[start+1 : end]
		parts := splitLast2(full)
		if parts != "" {
			return parts
		}
	}
	return "hashicorp/" + model.ProviderLocalName(resourceType)
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// splitLast2 returns the last two "/"-separated segments ("hashicorp/aws").
func splitLast2(s string) string {
	slash1 := lastIndexByte(s, '/')
	if slash1 < 0 {
		return s
	}
	slash2 := lastIndexByte(s[:slash1], '/')
	if slash2 < 0 {
		return s
	}
	return s[slash2+1:]
}
