package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

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

// handleStateList reads run/terraform.tfstate directly (no CLI round-trip) and
// returns the tracked resources. A missing or unreadable state is an empty
// list — the project simply has nothing deployed.
func (s *Server) handleStateList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.runDir(id), "terraform.tfstate"))
	if err != nil {
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
