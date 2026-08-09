package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	goversion "github.com/hashicorp/go-version"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/store"
)

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	engine := req.Engine
	if engine == "" {
		engine = s.cfg.DefaultEngine
	}
	if _, ok := s.runnerFor(engine); !ok {
		writeErr(w, http.StatusBadRequest, "unknown engine")
		return
	}
	name := req.Name
	if name == "" {
		name = "untitled"
	}
	p := &model.Project{
		ID:        uuid.NewString(),
		Name:      name,
		Engine:    model.Engine(engine),
		Providers: []model.ProviderInstance{},
		Resources: []model.Resource{},
	}
	if err := s.store.Create(p); err != nil {
		writeErr(w, http.StatusInternalServerError, "create project: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// handleListProjects returns all projects (id, name, engine, counts).
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Hide projects the caller may not read (per-project access list), and —
	// for a key-authenticated request — anything outside the key's project
	// restriction. The listing has to agree with what the per-project gates
	// enforce, or a restricted key sees projects that 403 on every follow-up.
	u, _ := userFromContext(r)
	key, viaKey := keyFromContext(r)
	driftStates := s.driftStatuses()
	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		if !s.projectVisible(u, p.ID) {
			continue
		}
		if viaKey && !key.AllowsProject(p.ID) {
			continue
		}
		entry := map[string]any{
			"id":            p.ID,
			"name":          p.Name,
			"engine":        p.Engine,
			"resourceCount": len(p.Resources),
		}
		if s.settings != nil && s.settings.Drift(p.ID).Enabled {
			entry["driftEnabled"] = true
		}
		if st, ok := driftStates[p.ID]; ok {
			entry["drift"] = st
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["name"].(string) < out[j]["name"].(string)
	})
	writeJSON(w, http.StatusOK, out)
}

// identRe matches an HCL identifier, as valid for a provider alias.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// providerRefRe matches a provider meta-arg reference: "aws" or "aws.euwest".
var providerRefRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)?$`)

// handleSetProviderConfig sets (and persists) a provider instance's
// configuration. Instances are identified by (key, alias); an unknown pair is
// created on the project, which is also how aliased instances are added.
func (s *Server) handleSetProviderConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	var req providerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Alias != "" && !identRe.MatchString(req.Alias) {
		writeErr(w, http.StatusBadRequest, "invalid alias (must be an identifier)")
		return
	}
	// The source is user-editable from the provider form, and it flows into a
	// generated config AND becomes an HCL identifier via providerKeyFromSource,
	// so it gets the same guard as every other provider route.
	source := strings.TrimSpace(req.Source)
	if source != "" && !validProviderSource(source) {
		writeErr(w, http.StatusBadRequest, "invalid provider source (expected [host/][namespace/]type)")
		return
	}
	// A malformed constraint would otherwise fail on every engine op instead of
	// here — same reasoning as required_version in handleSetTerraform.
	if req.Version != nil {
		if v := strings.TrimSpace(*req.Version); v != "" {
			if _, verr := goversion.NewConstraint(v); verr != nil {
				writeErr(w, http.StatusBadRequest, "invalid version constraint: "+verr.Error())
				return
			}
		}
	}
	config, err := attrsToModel(req.Config)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	err = s.store.Update(id, func(p *model.Project) error {
		for i := range p.Providers {
			if p.Providers[i].Key == key && p.Providers[i].Alias == req.Alias {
				p.Providers[i].Config = config
				if source != "" {
					p.Providers[i].Source = source
				}
				// nil = leave the pin alone; "" = unpin.
				if req.Version != nil {
					p.Providers[i].Version = strings.TrimSpace(*req.Version)
				}
				return nil
			}
		}
		// Instance not yet on the project: add it. An aliased instance inherits
		// source/version from the key's default instance unless overridden.
		version := ""
		if req.Version != nil {
			version = strings.TrimSpace(*req.Version)
		}
		for _, prov := range p.Providers {
			if prov.Key == key && prov.Alias == "" {
				if source == "" {
					source = prov.Source
				}
				if version == "" {
					version = prov.Version
				}
				break
			}
		}
		if source == "" {
			source = "hashicorp/" + key
		}
		p.Providers = append(p.Providers, model.ProviderInstance{
			Key: key, Alias: req.Alias, Source: source, Version: version, Config: config,
		})
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

// handleDeleteProviderInstance removes one provider instance (key + optional
// ?alias=). Deleting is refused while a resource or data source still selects
// the instance via its provider meta-arg (the default instance is always
// implicitly selectable, so only its explicit references are checked).
func (s *Server) handleDeleteProviderInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	alias := r.URL.Query().Get("alias")
	addr := key
	if alias != "" {
		addr = key + "." + alias
	}
	err := s.store.Update(id, func(p *model.Project) error {
		if n := countProviderRefs(p, addr); n > 0 {
			return fmt.Errorf("%w: %d resource(s) still use provider %s", errProviderInUse, n, addr)
		}
		out := p.Providers[:0]
		found := false
		for _, prov := range p.Providers {
			if prov.Key == key && prov.Alias == alias {
				found = true
				continue
			}
			out = append(out, prov)
		}
		if !found {
			return store.ErrNotFound
		}
		p.Providers = out
		return nil
	})
	if errors.Is(err, errProviderInUse) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project or provider not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

var errProviderInUse = errors.New("provider in use")

// countProviderRefs counts resources and data sources (root and local modules)
// whose provider meta-arg selects the given instance address.
func countProviderRefs(p *model.Project, addr string) int {
	n := 0
	count := func(rs []model.Resource) {
		for _, r := range rs {
			if r.Provider == addr {
				n++
			}
		}
	}
	count(p.Resources)
	count(p.DataSources)
	for _, lm := range p.LocalModules {
		count(lm.Resources)
		count(lm.DataSources)
	}
	return n
}

// handleSetConfig replaces a project's variables, locals, and outputs.
func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req projectConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	vars, locals, outputs, err := req.toModel()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err = s.store.Update(id, func(p *model.Project) error {
		if req.Module == "" {
			p.Variables, p.Locals, p.Outputs = vars, locals, outputs
			return nil
		}
		for i := range p.LocalModules {
			if p.LocalModules[i].Name == req.Module {
				p.LocalModules[i].Variables = vars
				p.LocalModules[i].Locals = locals
				p.LocalModules[i].Outputs = outputs
				return nil
			}
		}
		return store.ErrNotFound
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project or module not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

// deployedResourceCount reports how many managed resources the project's
// persisted state still tracks. Missing or unreadable state counts as zero, so
// projects that were never applied delete without friction. With a remote
// backend the state is pulled through the CLI (best-effort — the state itself
// survives project deletion, so the guard is advisory there).
func (s *Server) deployedResourceCount(ctx context.Context, id string) int {
	p, err := s.store.Get(id)
	if err != nil {
		return 0
	}
	// Check the default workspace plus every configured environment workspace, so
	// a project with resources deployed only under a named env still warns.
	envDirs := []string{""}
	if s.settings != nil {
		for _, e := range s.settings.Environments(id) {
			envDirs = append(envDirs, e.Name)
		}
	}
	n := 0
	for _, env := range envDirs {
		n += s.managedCountIn(ctx, p, s.runDir(id, env))
	}
	return n
}

// managedCountIn returns the number of managed resources tracked in one
// workspace's state (0 on any error — a never-applied workspace deletes freely).
func (s *Server) managedCountIn(ctx context.Context, p *model.Project, dir string) int {
	var data []byte
	if p.Backend != nil {
		run, ok := s.runnerFor(string(p.Engine))
		if !ok {
			return 0
		}
		raw, perr := run.StatePull(ctx, dir)
		if perr != nil {
			return 0
		}
		data = []byte(raw)
	} else if b, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate")); err != nil {
		return 0
	} else {
		data = b
	}
	var st struct {
		Resources []struct {
			Mode string `json:"mode"`
		} `json:"resources"`
	}
	if json.Unmarshal(data, &st) != nil {
		return 0
	}
	n := 0
	for _, res := range st.Resources {
		if res.Mode == "managed" {
			n++
		}
	}
	return n
}

// handleDeleteProject deletes a project and its folder. Deleting also deletes
// the persisted state, which would silently orphan anything still deployed —
// so if the state tracks managed resources the delete is refused with 409
// unless ?force=true is passed.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if r.URL.Query().Get("force") != "true" {
		if n := s.deployedResourceCount(r.Context(), id); n > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    fmt.Sprintf("%d deployed resource(s) are still tracked in this project's state — destroy them first, or delete anyway to orphan them", n),
				"deployed": n,
			})
			return
		}
	}
	if err := s.store.Delete(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.runs != nil {
		_ = s.runs.DeleteProject(id) // best-effort; the project itself is gone
	}
	if s.settings != nil {
		_ = s.settings.DeleteDrift(id)
		_ = s.settings.DeleteGitCreds(id)
		_ = s.settings.DeleteProjectACL(id)
		_ = s.settings.DeleteCost(id)
		_ = s.settings.DeleteEnvironments(id)
		_ = s.settings.DeleteEnvVars(id)
		_ = s.settings.DeleteApprovalPolicy(id)
		_ = s.settings.DeletePolicy(id)
		_ = s.settings.DeleteNotify(id)
	}
	if s.approvals != nil {
		_ = s.approvals.DeleteProject(id)
	}
	if s.audit != nil {
		_ = s.audit.DeleteProject(id)
	}
	s.driftMu.Lock()
	delete(s.driftStates, id)
	s.driftMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

var errBadModel = errors.New("invalid model")

// handleReplaceProject replaces a project's entire editable model in one call.
// This backs client-side undo/redo, which restores full snapshots. Identity
// (ID, Name, Engine) and the state Backend are always preserved from the
// stored project: a snapshot restore must never rename the project or repoint
// its state. The incoming model is generated up front because store persist
// swallows generation errors.
func (s *Server) handleReplaceProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var incoming model.Project
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	err := s.store.Update(id, func(p *model.Project) error {
		incoming.ID = p.ID
		incoming.Name = p.Name
		incoming.Engine = p.Engine
		incoming.Backend = p.Backend
		if _, gerr := hclgen.GenerateFiles(&incoming); gerr != nil {
			return fmt.Errorf("%w: %s", errBadModel, gerr)
		}
		*p = incoming
		return nil
	})
	if errors.Is(err, errBadModel) {
		writeErr(w, http.StatusBadRequest, err.Error())
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
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleUpsertResource adds a new resource or data source (kind = "data") or
// updates an existing one (by id). It also ensures the type's provider is
// registered on the project.
func (s *Server) handleUpsertResource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req resourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Type == "" || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "type and name are required")
		return
	}
	if req.Kind != "" && req.Kind != "resource" && req.Kind != "data" {
		writeErr(w, http.StatusBadRequest, "kind must be \"resource\" or \"data\"")
		return
	}
	attrs, err := attrsToModel(req.Attrs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Count) != "" && strings.TrimSpace(req.ForEach) != "" {
		writeErr(w, http.StatusBadRequest, "count and for_each are mutually exclusive")
		return
	}
	for name, expr := range map[string]string{"count": req.Count, "for_each": req.ForEach} {
		if strings.TrimSpace(expr) == "" {
			continue
		}
		if err := hclgen.ValidateExpr(expr); err != nil {
			writeErr(w, http.StatusBadRequest, name+": "+err.Error())
			return
		}
	}
	providerRef := strings.TrimSpace(req.Provider)
	if providerRef != "" && !providerRefRe.MatchString(providerRef) {
		writeErr(w, http.StatusBadRequest, "provider must be \"<key>\" or \"<key>.<alias>\"")
		return
	}
	blocks, err := blocksToModel(req.Blocks)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	var saved model.Resource
	err = s.store.Update(id, func(p *model.Project) error {
		slice, ok := resourceSlice(p, req.Module)
		if req.Kind == "data" {
			slice, ok = dataSourceSlice(p, req.Module)
		}
		if !ok {
			return store.ErrNotFound
		}
		res := model.Resource{
			ID: req.ID, Type: req.Type, Name: req.Name, Attrs: attrs, DependsOn: req.DependsOn,
			Count: strings.TrimSpace(req.Count), ForEach: strings.TrimSpace(req.ForEach),
			Provider: providerRef, Blocks: blocks,
		}
		if res.ID == "" {
			res.ID = uuid.NewString()
		}
		// Provider instances always live on the root; local modules inherit them.
		ensureProvider(p, req.Type, req.ProviderSource)

		// Replace by id if present, else append (within the target scope).
		replaced := false
		for i := range *slice {
			if (*slice)[i].ID == res.ID {
				// Nested blocks the request did not mention are the stored ones:
				// the form cannot edit a dynamic/provisioner block, so a save from
				// it must not delete one.
				if req.Blocks == nil {
					res.Blocks = (*slice)[i].Blocks
				}
				(*slice)[i] = res
				replaced = true
				break
			}
		}
		if !replaced {
			*slice = append(*slice, res)
		}
		saved = res
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project or module not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rid := chi.URLParam(r, "rid")
	module := r.URL.Query().Get("module")
	kind := r.URL.Query().Get("kind")
	err := s.store.Update(id, func(p *model.Project) error {
		slice, ok := resourceSlice(p, module)
		if kind == "data" {
			slice, ok = dataSourceSlice(p, module)
		}
		if !ok {
			return store.ErrNotFound
		}
		out := (*slice)[:0]
		for _, res := range *slice {
			if res.ID != rid {
				out = append(out, res)
			}
		}
		*slice = out
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProjectHCL returns the generated configuration for a scope as the map
// of FILES it actually consists of — `{"terraform.tf": "...", "main.tf": ...}`
// — which is what the preview pane renders as one tab per file.
//
// It used to return one `text/plain` document, which stopped being the truth
// the moment generation grew a second file: imports.tf and moved.tf were on
// disk and in Git but invisible in the pane. Returning the same map the store
// writes means the preview cannot drift from the project again.
//
// `?module=<name>` returns that local module's own files instead of the root's.
func (s *Server) handleProjectHCL(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var files map[string][]byte
	if m := r.URL.Query().Get("module"); m != "" {
		found := false
		for _, lm := range p.LocalModules {
			if lm.Name == m {
				files, err = hclgen.GenerateModuleFiles(lm, p.Providers)
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusNotFound, "module not found")
			return
		}
	} else {
		// Root scope only: a local module's files are reached through ?module=,
		// and companion files are user-authored, edited elsewhere, and not HCL.
		files, err = hclgen.GenerateRootFiles(p)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "generate: "+err.Error())
		return
	}
	out := make(map[string]string, len(files))
	for name, content := range files {
		out[name] = string(content)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

// ensureProvider adds a ProviderInstance for the resource type's provider if it
// is not already present. source is the real registry address supplied by the
// client (e.g. "hellscrimson/arcane"); if empty, it falls back to the hashicorp
// namespace.
//
// It upgrades ONLY the fallback placeholder ("hashicorp/<key>") to a real
// source, and never rewrites a source the project already holds. The hint comes
// from the client's palette, which follows whichever provider was touched last
// — so saving a resource of provider B while the palette still held provider
// A's source used to overwrite B's source with A's, keeping only the key. Every
// later plan then failed, and the config form fetched A's schema for B, writing
// A-shaped attributes into B's block. A stale hint must be inert, not
// authoritative: the source is edited in the provider form (PUT
// providers/{key}), which is the one place that may change it.
func ensureProvider(p *model.Project, resourceType, source string) {
	key := model.ProviderLocalName(resourceType)
	placeholder := "hashicorp/" + key
	// A source only describes THIS provider when its last segment is the local
	// name the resource type implies: "hashicorp/null" can never be the source
	// for a `random_*` resource. Checking that is what makes a stale hint inert —
	// the placeholder test alone is not enough, because for a hashicorp provider
	// the genuine source and the placeholder are the same string, so "upgrading"
	// it happily accepted another provider's address.
	if source == "" || providerKeyFromSource(source) != key {
		source = placeholder
	}
	for i, prov := range p.Providers {
		if prov.Key == key {
			// Only ever fill in the fallback; a source the project already holds
			// is authoritative and changes only in the provider form.
			if prov.Source == placeholder && source != placeholder {
				p.Providers[i].Source = source
			}
			return
		}
	}
	p.Providers = append(p.Providers, model.ProviderInstance{Key: key, Source: source})
}
