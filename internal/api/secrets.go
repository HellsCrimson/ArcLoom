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

// handleRevealSecret returns ONE secret's plaintext value.
//
// The only endpoint in the product that hands a secret back to the browser, so
// the shape is deliberate:
//
//   - It is a POST, not a GET. A GET would be cacheable, would land in proxy
//     access logs as a URL that implies its own response, and is the shape a
//     cross-site navigation can trigger; POST is none of those and, like every
//     route in this group, goes through csrfGuard.
//   - It reveals ONE named secret per call, never the list. `handleListSecrets`
//     stays value-free, so the blast radius of a mistake is one name.
//   - It resolves through the SAME path a plan does (Store.Resolve): the writable
//     backend first, then the env fallback. Showing anything else would be a lie
//     — the point of looking is to see what a run would actually inject.
//   - It is gated by PermResources (plus the project's ACL, applied to this whole
//     route group), i.e. exactly the users who can already overwrite the value,
//     and it is audited as `secret.reveal` with the name.
func (s *Server) handleRevealSecret(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	name := chi.URLParam(r, "name")
	value, ok, err := s.secrets.Resolve(id, name)
	if err != nil {
		// A configured remote store that cannot be read must fail loudly rather
		// than fall back to the local files — same rule as plan-time resolution.
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "secret not found")
		return
	}
	// This is the one response in the API that carries a plaintext secret, so it
	// must not be retained anywhere on the way back. A POST is not cached by
	// browsers by default, but "by default" is not the guarantee wanted for a
	// credential — an intermediary, a debugging proxy or a future client library
	// with its own cache would all be within their rights. The token endpoint
	// already says this (oauth.go); this is the same rule.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "value": value})
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
