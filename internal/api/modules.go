package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/matthiasrauline/arcloom/internal/model"
	"github.com/matthiasrauline/arcloom/internal/store"
)

var moduleNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// resourceSlice returns a pointer to the resource slice for the given scope
// ("" = root project, else a local module) and whether it was found.
func resourceSlice(p *model.Project, moduleName string) (*[]model.Resource, bool) {
	if moduleName == "" {
		return &p.Resources, true
	}
	for i := range p.LocalModules {
		if p.LocalModules[i].Name == moduleName {
			return &p.LocalModules[i].Resources, true
		}
	}
	return nil, false
}

// handleCreateLocalModule adds an empty local module to the project.
func (s *Server) handleCreateLocalModule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if !moduleNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid module name (letters, digits, _-; must start with a letter or _)")
		return
	}
	err := s.store.Update(id, func(p *model.Project) error {
		for _, lm := range p.LocalModules {
			if lm.Name == req.Name {
				return errDuplicate
			}
		}
		p.LocalModules = append(p.LocalModules, model.LocalModule{Name: req.Name, Resources: []model.Resource{}})
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if errors.Is(err, errDuplicate) {
		writeErr(w, http.StatusBadRequest, "a module with that name already exists")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteLocalModule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	err := s.store.Update(id, func(p *model.Project) error {
		out := p.LocalModules[:0]
		for _, lm := range p.LocalModules {
			if lm.Name != name {
				out = append(out, lm)
			}
		}
		p.LocalModules = out
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

// handleSetModuleInputs sets the input values the root passes to a local module.
func (s *Server) handleSetModuleInputs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	var req struct {
		Inputs map[string]attrValueDTO `json:"inputs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	inputs, err := attrsToModel(req.Inputs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err = s.store.Update(id, func(p *model.Project) error {
		for i := range p.LocalModules {
			if p.LocalModules[i].Name == name {
				p.LocalModules[i].Inputs = inputs
				return nil
			}
		}
		return store.ErrNotFound
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "module not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

var errDuplicate = errors.New("duplicate")
