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
		res := model.Resource{ID: req.ID, Type: req.Type, Name: req.Name, Attrs: attrs}
		if res.ID == "" {
			res.ID = uuid.NewString()
		}
		// Ensure a provider instance exists for this resource type, using the
		// real source supplied by the client (e.g. "hellscrimson/arcane").
		ensureProvider(p, req.Type, req.ProviderSource)

		// Replace by id if present, else append.
		replaced := false
		for i := range p.Resources {
			if p.Resources[i].ID == res.ID {
				p.Resources[i] = res
				replaced = true
				break
			}
		}
		if !replaced {
			p.Resources = append(p.Resources, res)
		}
		saved = res
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
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rid := chi.URLParam(r, "rid")
	err := s.store.Update(id, func(p *model.Project) error {
		out := p.Resources[:0]
		for _, res := range p.Resources {
			if res.ID != rid {
				out = append(out, res)
			}
		}
		p.Resources = out
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
	out, err := hclgen.Generate(p)
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
