package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/hclimport"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
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
	AddedResources    int `json:"addedResources"`
	AddedProviders    int `json:"addedProviders"`
	AddedVariables    int `json:"addedVariables,omitempty"`
	AddedLocals       int `json:"addedLocals,omitempty"`
	AddedOutputs      int `json:"addedOutputs,omitempty"`
	AddedDataSources  int `json:"addedDataSources,omitempty"`
	AddedModules      int `json:"addedModules,omitempty"`
	AddedLocalModules int `json:"addedLocalModules,omitempty"`
	AddedImports      int `json:"addedImports,omitempty"`
	// MatchedResources is how many state resources were recognized as resources
	// the model already describes (state import only) — matched, not duplicated.
	MatchedResources int `json:"matchedResources,omitempty"`
	// AdoptedState is how many state resources were installed as the project's
	// own state (state import only; 0 when the state was not adopted).
	AdoptedState int      `json:"adoptedState,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
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
			var skipped []string
			resp.AddedModules, skipped = mergeModules(p, parsed.Modules)
			resp.Warnings = append(resp.Warnings,
				skipWarnings("module call", skipped, " (a call's name is its address; rename it to call the same module twice)")...)
			resp.AddedLocalModules, skipped = mergeLocalModules(p, parsed.LocalModules)
			resp.Warnings = append(resp.Warnings,
				skipWarnings("local module", skipped, " — the project's own definition was kept, the imported one dropped")...)
			// import blocks have no name to collide on, so they simply append —
			// a duplicate is the engine's problem to report, and dropping one
			// silently would lose an adoption the user wrote down.
			p.Imports = append(p.Imports, parsed.Imports...)
			resp.AddedImports = len(parsed.Imports)
			// A backend block belongs to the root; only adopt it if the project
			// doesn't already have one (never silently repoint state).
			if parsed.Backend != nil && p.Backend == nil {
				p.Backend = parsed.Backend
			}
			// Same rule for the terraform block's required_version.
			if parsed.RequiredVersion != "" && p.RequiredVersion == "" {
				p.RequiredVersion = parsed.RequiredVersion
			}
		}

		resp.AddedResources = mergeResourcesInto(scope, parsed.Resources)
		resp.AddedDataSources = mergeDataSourcesInto(data, parsed.DataSources)
		// Unlike resources (renamed to `x_2`), a name clash here drops the block
		// whole, so each one is reported.
		var skip []string
		resp.AddedVariables, skip = mergeVariablesInto(vars, parsed.Variables)
		resp.Warnings = append(resp.Warnings, skipWarnings("variable", skip, "")...)
		resp.AddedLocals, skip = mergeLocalsInto(locals, parsed.Locals)
		resp.Warnings = append(resp.Warnings, skipWarnings("local", skip, "")...)
		resp.AddedOutputs, skip = mergeOutputsInto(outputs, parsed.Outputs)
		resp.Warnings = append(resp.Warnings, skipWarnings("output", skip, "")...)
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

// handleImportState reads a Terraform/OpenTofu state file and adopts the project
// it describes: its managed resources become model resources (keeping only
// user-settable attributes, per each resource's schema, so the generated config
// stays valid) AND the state itself is installed as the target workspace's
// state. Both halves matter — with the config alone, the next plan would propose
// creating everything the state already tracks a second time.
//
// Adopting the state is refused with 409 when the workspace already tracks
// managed resources (overwriting would orphan them), unless ?force=true.
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
	// The state lands in the selected environment's workspace, like every other
	// state operation.
	envName, env, err := s.resolveEnv(id, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	resources, warnings, err := state.Parse([]byte(req.State))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "parse state: "+err.Error())
		return
	}

	// Refuse before touching anything: replacing a state that still tracks
	// resources would orphan them (same rule as the project delete guard).
	force := r.URL.Query().Get("force") == "true"
	dir := s.runDir(id, envName)
	if !force {
		if n := s.managedCountIn(r.Context(), p, dir); n > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": fmt.Sprintf("this project's state already tracks %d resource(s); importing would replace it and orphan them — tick \"replace the existing state\" to do it anyway", n),
				"state": n,
			})
			return
		}
	}

	// Resolve the provider schemas once for the whole state (a single engine
	// init), not once per resource.
	schemas, schemaWarnings := s.stateProviderSchemas(r.Context(), run, resources)
	warnings = append(warnings, schemaWarnings...)

	imported := make([]scopedResource, 0, len(resources))
	for _, sr := range resources {
		ps := schemas[providerSourceFromState(sr.ProviderAddr, sr.Type)]
		if ps == nil {
			continue // already reported by stateProviderSchemas
		}
		rs := ps.ResourceSchemas[sr.Type]
		attrs, w2 := state.SettableAttrs(rs, sr.Attributes)
		warnings = append(warnings, w2...)
		imported = append(imported, scopedResource{
			Module:   sr.Module,
			Resource: model.Resource{ID: uuid.NewString(), Type: sr.Type, Name: sr.Name, Attrs: attrs},
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
		// State resources are matched to the config by address, never renamed: a
		// state entry with the same address IS that config block (the usual flow
		// is import HCL, then import its state).
		added, matched, w2 := mergeStateResources(pr, imported)
		resp.AddedResources, resp.MatchedResources = added, matched
		resp.Warnings = append(resp.Warnings, w2...)
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Install the state itself, so the State panel lists these resources and the
	// next plan diffs against them.
	if err := s.adoptState(r.Context(), run, id, envName, env, []byte(req.State)); err != nil {
		// The model was imported; say plainly that the state half failed rather
		// than pretending the whole import worked.
		resp.Warnings = append(resp.Warnings, "state not adopted: "+firstLine(err.Error()))
	} else {
		resp.AdoptedState = len(resources)
	}
	writeJSON(w, http.StatusOK, resp)
}

// stateProviderSchemas resolves the provider schema for every distinct provider
// referenced by a state, batching them into ONE engine call (each call installs
// and runs the provider plugins, so per-resource fetches made a modest state
// take tens of seconds). If the batch fails — one bad source sinks it — each
// provider is retried alone so the rest still import.
func (s *Server) stateProviderSchemas(ctx context.Context, run runner.Runner, resources []state.Resource) (map[string]*tfjson.ProviderSchema, []string) {
	out := map[string]*tfjson.ProviderSchema{}
	var warnings []string

	sources := map[string]string{} // source -> local name
	for _, sr := range resources {
		src := providerSourceFromState(sr.ProviderAddr, sr.Type)
		if _, ok := sources[src]; !ok {
			sources[src] = model.ProviderLocalName(sr.Type)
		}
	}
	instances := make([]model.ProviderInstance, 0, len(sources))
	for src, key := range sources {
		instances = append(instances, model.ProviderInstance{Key: key, Source: src})
	}
	if len(instances) == 0 {
		return out, nil
	}

	if schemas, err := run.ProvidersSchema(ctx, instances); err == nil {
		for src := range sources {
			if _, ps := findProviderSchema(schemas, src); ps != nil {
				out[src] = ps
			}
		}
	}
	// Anything the batch didn't resolve (whole call failed, or one provider
	// missing) gets one more chance on its own.
	for src, key := range sources {
		if out[src] != nil {
			continue
		}
		schemas, err := run.ProvidersSchema(ctx, []model.ProviderInstance{{Key: key, Source: src}})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: schema unavailable (%v) — its resources were skipped", src, err))
			continue
		}
		if _, ps := findProviderSchema(schemas, src); ps != nil {
			out[src] = ps
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s: provider schema not found — its resources were skipped", src))
	}
	return out, warnings
}

// adoptState installs an imported state as the workspace's own. With the default
// local backend that is a file write (the engine picks it up on the next op,
// terraform.tfstate being one of the persisted workspace entries); with a
// configured backend it has to go through the CLI so the remote receives it.
func (s *Server) adoptState(ctx context.Context, run runner.Runner, id, envName string, env settings.Environment, stateJSON []byte) error {
	p, err := s.store.Get(id)
	if err != nil {
		return err
	}
	dir := s.runDir(id, envName)
	if p.Backend != nil {
		files, err := hclgen.GenerateFiles(p)
		if err != nil {
			return err
		}
		bc, err := s.backendConfigFor(p, env, func(ref string) {
			slog.Warn("backend secret not set for state push", "project", id, "secret", ref)
		})
		if err != nil {
			return err
		}
		if bc != nil {
			files[".arcloom.tfbackend"] = bc
		}
		return run.StatePush(ctx, dir, files, stateJSON, nil)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 0600: a state file carries resource attributes, including values a provider
	// marked sensitive.
	return os.WriteFile(filepath.Join(dir, "terraform.tfstate"), stateJSON, 0o600)
}

// scopedResource is a resource read from a state file together with the module
// scope its address puts it in ("" = root).
type scopedResource struct {
	Module string
	model.Resource
}

// mergeStateResources merges resources read from a state file into the model,
// matching them to the config by ADDRESS instead of renaming on collision — a
// state entry and a config block with the same address are the same resource, so
// the "import HCL, then import its state" flow must not produce duplicates. An
// existing resource is left exactly as it is (the config, which can hold
// references and expressions the state cannot express, wins) and counted as
// matched. Resources whose module scope ArcLoom does not model are skipped with a
// warning: the adopted state still tracks them, but their config lives elsewhere.
func mergeStateResources(p *model.Project, incoming []scopedResource) (added, matched int, warnings []string) {
	for _, sr := range incoming {
		scope, err := stateScope(p, sr.Module)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", stateAddress(sr), err))
			continue
		}
		if idx := indexOfResource(*scope, sr.Type, sr.Name); idx >= 0 {
			matched++
			continue
		}
		*scope = append(*scope, sr.Resource)
		added++
	}
	return added, matched, warnings
}

// stateScope resolves a state module path to the model slice that holds its
// resources. Only the root and a single level of local module are addressable in
// the model today.
func stateScope(p *model.Project, module string) (*[]model.Resource, error) {
	if module == "" {
		return &p.Resources, nil
	}
	name, ok := localModuleName(module)
	if !ok {
		return nil, fmt.Errorf("nested module %q is not supported; its configuration was not imported", module)
	}
	if lm := findLocalModule(p, name); lm != nil {
		return &lm.Resources, nil
	}
	return nil, fmt.Errorf("no local module %q in this project; its configuration was not imported (the adopted state still tracks it)", name)
}

// localModuleName extracts the module name from a single-level state module path
// ("module.web" → "web"); a nested path returns ok=false.
func localModuleName(module string) (string, bool) {
	rest, ok := strings.CutPrefix(module, "module.")
	if !ok || rest == "" || strings.Contains(rest, ".") {
		return "", false
	}
	return rest, true
}

// indexOfResource finds a resource by (type, name) within one scope.
func indexOfResource(scope []model.Resource, typ, name string) int {
	for i := range scope {
		if scope[i].Type == typ && scope[i].Name == name {
			return i
		}
	}
	return -1
}

// stateAddress renders a scoped resource's address for messages.
func stateAddress(sr scopedResource) string {
	addr := sr.Type + "." + sr.Name
	if sr.Module != "" {
		return sr.Module + "." + addr
	}
	return addr
}

// mergeProviders adds provider instances not already present (by key + alias).
// Real sources override placeholder "hashicorp/<key>" sources.
func mergeProviders(p *model.Project, incoming []model.ProviderInstance) int {
	added := 0
	for _, pi := range incoming {
		found := false
		for i := range p.Providers {
			if p.Providers[i].Key == pi.Key && p.Providers[i].Alias == pi.Alias {
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

// mergeNamed appends every incoming item whose name is still free and REPORTS
// the ones it skipped. The report is the point: a block that collides is
// dropped whole (unlike resources, which are renamed to `x_2`), and a silent
// drop is indistinguishable from a parse failure — a second `module` call whose
// label was already taken simply vanished, with the import summary still
// reading "success".
func mergeNamed[T any](dst *[]T, incoming []T, name func(T) string) (int, []string) {
	taken := map[string]bool{}
	for _, item := range *dst {
		taken[name(item)] = true
	}
	added := 0
	var skipped []string
	for _, item := range incoming {
		n := name(item)
		if taken[n] {
			skipped = append(skipped, n)
			continue
		}
		taken[n] = true
		*dst = append(*dst, item)
		added++
	}
	return added, skipped
}

// skipWarnings turns a merge's skipped names into user-facing warnings. `kind`
// names the block ("variable"), `hint` says what to do about it.
func skipWarnings(kind string, skipped []string, hint string) []string {
	out := make([]string, 0, len(skipped))
	for _, n := range skipped {
		out = append(out, fmt.Sprintf("%s %q already exists — skipped%s", kind, n, hint))
	}
	return out
}

func mergeVariablesInto(dst *[]model.Variable, incoming []model.Variable) (int, []string) {
	return mergeNamed(dst, incoming, func(v model.Variable) string { return v.Name })
}

func mergeLocalsInto(dst *[]model.Local, incoming []model.Local) (int, []string) {
	return mergeNamed(dst, incoming, func(l model.Local) string { return l.Name })
}

func mergeOutputsInto(dst *[]model.Output, incoming []model.Output) (int, []string) {
	return mergeNamed(dst, incoming, func(o model.Output) string { return o.Name })
}

// mergeModules appends imported module CALLS. Several calls to one source are
// normal (that is what the call/definition split is for), so the only thing
// that collides is the call's own name — its address.
func mergeModules(p *model.Project, incoming []model.Module) (int, []string) {
	return mergeNamed(&p.Modules, incoming, func(m model.Module) string { return m.Name })
}

// mergeLocalModules appends imported local module DEFINITIONS, skipping any
// whose name is already present. Skipping is right — the definition in the
// project wins — but it means the imported folder's contents were dropped,
// which is worth saying out loud.
func mergeLocalModules(p *model.Project, incoming []model.LocalModule) (int, []string) {
	return mergeNamed(&p.LocalModules, incoming, func(m model.LocalModule) string { return m.Name })
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
