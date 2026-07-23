package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/store"
)

var backendTypeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// handleSetBackend replaces (or clears, with an empty type) the project's
// state backend. Saving only updates the model + generated HCL; the state
// itself moves when the deploy-gated init-backend operation runs.
func (s *Server) handleSetBackend(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Type   string                  `json:"type"`
		Config map[string]attrValueDTO `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	var backend *model.Backend
	if req.Type != "" {
		if !backendTypeRe.MatchString(req.Type) {
			writeErr(w, http.StatusBadRequest, "invalid backend type")
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
					writeErr(w, http.StatusBadRequest, name+": "+err.Error())
					return
				}
			}
		}
		if len(config) == 0 {
			config = nil
		}
		backend = &model.Backend{Type: req.Type, Config: config}
	}
	err := s.store.Update(id, func(p *model.Project) error {
		p.Backend = backend
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

// stateEntry is one resource tracked in a project's persisted state, in the
// address form the state ops (import / rm / mv) operate on.
type stateEntry struct {
	Address string `json:"address"`
	Mode    string `json:"mode"` // "managed" | "data"
	Type    string `json:"type"`
	Name    string `json:"name"`
	Module  string `json:"module,omitempty"` // "module.x" when nested
	// Instances holds count/for_each index keys ("0", `"a"`); empty for a
	// single-instance resource.
	Instances []string `json:"instances,omitempty"`
}

// handleStateList returns the resources tracked in the project's state. With
// the default local backend it reads run/terraform.tfstate directly (no CLI
// round-trip); with a configured backend it pulls the state through the CLI so
// the list reflects the remote. A missing or unreadable state is an empty
// list — the project simply has nothing deployed.
func (s *Server) handleStateList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.store.Get(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	envName, _, err := s.resolveEnv(id, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dir := s.runDir(id, envName)
	var data []byte
	if p.Backend != nil {
		run, ok := s.runnerFor(string(p.Engine))
		if !ok {
			writeErr(w, http.StatusBadRequest, "unknown engine")
			return
		}
		raw, perr := run.StatePull(r.Context(), dir)
		if perr != nil {
			// Workspace not initialized against the backend yet (or backend
			// unreachable): report empty rather than failing the panel.
			writeJSON(w, http.StatusOK, []stateEntry{})
			return
		}
		data = []byte(raw)
	} else if data, err = os.ReadFile(filepath.Join(dir, "terraform.tfstate")); err != nil {
		writeJSON(w, http.StatusOK, []stateEntry{})
		return
	}
	var st struct {
		Resources []struct {
			Mode      string `json:"mode"`
			Type      string `json:"type"`
			Name      string `json:"name"`
			Module    string `json:"module"`
			Instances []struct {
				IndexKey any `json:"index_key"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if json.Unmarshal(data, &st) != nil {
		writeJSON(w, http.StatusOK, []stateEntry{})
		return
	}
	out := make([]stateEntry, 0, len(st.Resources))
	for _, res := range st.Resources {
		e := stateEntry{Mode: res.Mode, Type: res.Type, Name: res.Name, Module: res.Module}
		addr := res.Type + "." + res.Name
		if res.Mode == "data" {
			addr = "data." + addr
		}
		if res.Module != "" {
			addr = res.Module + "." + addr
		}
		e.Address = addr
		for _, inst := range res.Instances {
			switch k := inst.IndexKey.(type) {
			case nil:
				// single instance — no index key
			case string:
				e.Instances = append(e.Instances, fmt.Sprintf("%q", k))
			case float64:
				e.Instances = append(e.Instances, fmt.Sprintf("%d", int(k)))
			default:
				e.Instances = append(e.Instances, fmt.Sprintf("%v", k))
			}
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}
