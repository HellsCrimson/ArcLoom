package api

import "net/http"

// handleHealth is a public, dependency-free liveness probe. It touches no store,
// runner or filesystem so a container orchestrator can distinguish "process is
// up" from "a backend is slow". Registered outside /api and outside requireAuth.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
