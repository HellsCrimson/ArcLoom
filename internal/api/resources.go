package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/matthiasrauline/arcloom/internal/hclgen"
	"github.com/matthiasrauline/arcloom/internal/model"
	"github.com/matthiasrauline/arcloom/internal/store"
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
	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		out = append(out, map[string]any{
			"id":            p.ID,
			"name":          p.Name,
			"engine":        p.Engine,
			"resourceCount": len(p.Resources),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["name"].(string) < out[j]["name"].(string)
	})
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteProject removes a project and its folder.
// handleSetProviderConfig sets (and persists) a provider block's configuration.
// The provider is created on the project if it does not yet exist.
func (s *Server) handleSetProviderConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")
	var req providerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	config, err := attrsToModel(req.Config)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	err = s.store.Update(id, func(p *model.Project) error {
		for i := range p.Providers {
			if p.Providers[i].Key == key {
				p.Providers[i].Config = config
				if req.Source != "" {
					p.Providers[i].Source = req.Source
				}
				if req.Version != "" {
					p.Providers[i].Version = req.Version
				}
				return nil
			}
		}
		// Provider not yet on the project: add it.
		source := req.Source
		if source == "" {
			source = "hashicorp/" + key
		}
		p.Providers = append(p.Providers, model.ProviderInstance{
			Key: key, Source: source, Version: req.Version, Config: config,
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

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleUpsertResource adds a new resource or updates an existing one (by id).
// It also ensures the resource's provider is registered on the project.
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
	attrs, err := attrsToModel(req.Attrs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	var saved model.Resource
	err = s.store.Update(id, func(p *model.Project) error {
		slice, ok := resourceSlice(p, req.Module)
		if !ok {
			return store.ErrNotFound
		}
		res := model.Resource{ID: req.ID, Type: req.Type, Name: req.Name, Attrs: attrs, DependsOn: req.DependsOn}
		if res.ID == "" {
			res.ID = uuid.NewString()
		}
		// Provider instances always live on the root; local modules inherit them.
		ensureProvider(p, req.Type, req.ProviderSource)

		// Replace by id if present, else append (within the target scope).
		replaced := false
		for i := range *slice {
			if (*slice)[i].ID == res.ID {
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
	err := s.store.Update(id, func(p *model.Project) error {
		slice, ok := resourceSlice(p, module)
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

func (s *Server) handleProjectHCL(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var out []byte
	if m := r.URL.Query().Get("module"); m != "" {
		found := false
		for _, lm := range p.LocalModules {
			if lm.Name == m {
				out, err = hclgen.GenerateModuleFile(lm)
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusNotFound, "module not found")
			return
		}
	} else {
		out, err = hclgen.Generate(p)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "generate: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(out)
}

// ensureProvider adds a ProviderInstance for the resource type's provider if it
// is not already present. source is the real registry address supplied by the
// client (e.g. "hellscrimson/arcane"); if empty, it falls back to the hashicorp
// namespace. If the provider already exists but was registered with a fallback
// source, a real source upgrades it.
func ensureProvider(p *model.Project, resourceType, source string) {
	key := model.ProviderLocalName(resourceType)
	if source == "" {
		source = "hashicorp/" + key
	}
	for i, prov := range p.Providers {
		if prov.Key == key {
			// Upgrade a placeholder source to the real one if provided.
			if prov.Source != source && !strings.HasPrefix(source, "hashicorp/") {
				p.Providers[i].Source = source
			}
			return
		}
	}
	p.Providers = append(p.Providers, model.ProviderInstance{Key: key, Source: source})
}
