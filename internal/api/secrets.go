package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleListSecrets returns secret names + backend (never values).
func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	metas, err := s.secrets.List(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		// writable: whether secrets can be created here at all (the local store or
		// an external one); backend: which store that is, for the UI to name it.
		"writable": s.secrets.Writable(),
		"backend":  s.secrets.BackendName(),
		"secrets":  metas,
	})
}

// handleSetSecret stores a secret value in the writable backend.
func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := s.secrets.Set(id, name, body.Value); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSecret removes a secret from the writable backend.
func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	if err := s.secrets.Delete(id, name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
