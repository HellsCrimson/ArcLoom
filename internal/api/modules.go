package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/store"
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

// dataSourceSlice is resourceSlice's counterpart for the data-source list of a
// scope ("" = root project, else a local module).
func dataSourceSlice(p *model.Project, moduleName string) (*[]model.Resource, bool) {
	if moduleName == "" {
		return &p.DataSources, true
	}
	for i := range p.LocalModules {
		if p.LocalModules[i].Name == moduleName {
			return &p.LocalModules[i].DataSources, true
		}
	}
	return nil, false
}

// handleCreateLocalModule adds an empty local module DEFINITION to the project,
// plus a first call to it (same name) so the common case — define it, use it —
// stays one step. `call: false` in the body creates the definition alone, for a
// module the user means to call later, or several times under other names.
func (s *Server) handleCreateLocalModule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req := struct {
		Name string `json:"name"`
		Call *bool  `json:"call,omitempty"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if !moduleNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid module name (letters, digits, _-; must start with a letter or _)")
		return
	}
	withCall := req.Call == nil || *req.Call
	err := s.store.Update(id, func(p *model.Project) error {
		for _, lm := range p.LocalModules {
			if lm.Name == req.Name {
				return errDuplicate
			}
		}
		p.LocalModules = append(p.LocalModules, model.LocalModule{Name: req.Name, Resources: []model.Resource{}})
		if !withCall {
			return nil
		}
		// The call is a convenience, so a name already taken in the
		// `module "<name>"` namespace just means no auto-call — not an error,
		// since the definition itself is what was asked for.
		for _, m := range p.Modules {
			if m.Name == req.Name {
				return nil
			}
		}
		p.Modules = append(p.Modules, model.Module{Name: req.Name, Source: model.LocalSource(req.Name)})
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

// handleDeleteLocalModule removes a local module definition. It 409s while a
// call still uses it — deleting the definition alone would leave a `module`
// block pointing at a folder that no longer generates, which every engine op
// then fails on. `?force=true` deletes the calls along with it. Same shape as
// deleting a provider instance a resource still references.
func (s *Server) handleDeleteLocalModule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	force := r.URL.Query().Get("force") == "true"
	var users []string
	err := s.store.Update(id, func(p *model.Project) error {
		users = users[:0]
		for _, m := range p.CallsFor(name) {
			users = append(users, m.Name)
		}
		if len(users) > 0 {
			if !force {
				return errModuleInUse
			}
			kept := p.Modules[:0]
			for _, m := range p.Modules {
				if m.LocalFolder() != name {
					kept = append(kept, m)
				}
			}
			p.Modules = kept
		}
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
	if errors.Is(err, errModuleInUse) {
		writeErr(w, http.StatusConflict, "module "+name+" is still used by module."+strings.Join(users, ", module.")+" — delete the call(s) first")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

// validateModuleMeta checks the count / for_each meta-args of a module call:
// mutually exclusive, and each a parseable HCL expression (store persist
// swallows generation errors, so a typo has to fail here).
func validateModuleMeta(count, forEach string) error {
	if count != "" && forEach != "" {
		return errors.New("count and for_each are mutually exclusive")
	}
	if count != "" {
		if err := hclgen.ValidateExpr(count); err != nil {
			return errors.New("count: " + err.Error())
		}
	}
	if forEach != "" {
		if err := hclgen.ValidateExpr(forEach); err != nil {
			return errors.New("for_each: " + err.Error())
		}
	}
	return nil
}

var (
	errDuplicate   = errors.New("duplicate")
	errModuleInUse = errors.New("module in use")
	errNoSuchLocal = errors.New("no such local module")
)

// handleSetModuleCall creates or updates a module call — one `module "<name>"`
// block — for BOTH kinds: a registry/remote source and a local
// "./modules/<folder>" one go through here, because they are the same block and
// share one name namespace. `rename` moves an existing call, which is how the
// UI edits a call's name without a delete+create round trip.
//
// Like the resource upsert it REPLACES the call, so the form must always resend
// config/count/forEach. Expression values are validated here because store
// persist swallows generation errors.
func (s *Server) handleSetModuleCall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name    string                  `json:"name"`
		Rename  string                  `json:"rename,omitempty"`
		Source  string                  `json:"source"`
		Version string                  `json:"version,omitempty"`
		Config  map[string]attrValueDTO `json:"config"`
		Count   string                  `json:"count,omitempty"`
		ForEach string                  `json:"forEach,omitempty"`
		// Providers is the `providers = { … }` meta-arg as raw HCL. A POINTER so
		// absent means "keep what is stored" while "" means "clear it and go
		// back to the derived mapping" — the form does not model it, so with a
		// plain string every save would wipe an imported mapping, the same way
		// an imported for_each used to be deleted by the inputs form.
		Providers *string `json:"providers,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if !moduleNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid module name (letters, digits, _-; must start with a letter or _)")
		return
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		writeErr(w, http.StatusBadRequest, "source is required")
		return
	}
	config, err := attrsToModel(req.Config)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	for name, v := range config {
		if v.Mode == model.ModeExpression {
			if err := hclgen.ValidateExpr(v.Expr); err != nil {
				writeErr(w, http.StatusBadRequest, "input "+name+": "+err.Error())
				return
			}
		}
	}
	if len(config) == 0 {
		config = nil
	}
	count, forEach := strings.TrimSpace(req.Count), strings.TrimSpace(req.ForEach)
	if err := validateModuleMeta(count, forEach); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Providers != nil && strings.TrimSpace(*req.Providers) != "" {
		if err := hclgen.ValidateExpr(strings.TrimSpace(*req.Providers)); err != nil {
			writeErr(w, http.StatusBadRequest, "providers: "+err.Error())
			return
		}
	}

	err = s.store.Update(id, func(p *model.Project) error {
		m := model.Module{Name: req.Name, Source: source, Version: strings.TrimSpace(req.Version), Config: config, Count: count, ForEach: forEach}
		if req.Providers != nil {
			m.Providers = strings.TrimSpace(*req.Providers)
		}
		// A local source must name a definition this project actually has, or
		// generation produces a call into a folder that is never written.
		if folder := m.LocalFolder(); folder != "" {
			found := false
			for _, lm := range p.LocalModules {
				if lm.Name == folder {
					found = true
					break
				}
			}
			if !found {
				return errNoSuchLocal
			}
		}
		prev := strings.TrimSpace(req.Rename)
		if prev == "" {
			prev = req.Name
		}
		for i := range p.Modules {
			if p.Modules[i].Name == req.Name && p.Modules[i].Name != prev {
				return errDuplicate // renaming onto an existing call
			}
		}
		for i := range p.Modules {
			if p.Modules[i].Name == prev {
				// A request that says nothing about providers keeps the stored
				// mapping: the call form has no field for it.
				if req.Providers == nil {
					m.Providers = p.Modules[i].Providers
				}
				p.Modules[i] = m
				return nil
			}
		}
		p.Modules = append(p.Modules, m)
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if errors.Is(err, errDuplicate) {
		writeErr(w, http.StatusBadRequest, "a module call named "+req.Name+" already exists")
		return
	}
	if errors.Is(err, errNoSuchLocal) {
		writeErr(w, http.StatusBadRequest, "source "+source+" has no matching local module")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteModuleCall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	err := s.store.Update(id, func(p *model.Project) error {
		out := p.Modules[:0]
		for _, m := range p.Modules {
			if m.Name != name {
				out = append(out, m)
			}
		}
		p.Modules = out
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
