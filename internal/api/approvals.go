package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/approvals"
)

// approvalDTO is the approval-gate state for a project + environment: whether
// applies are gated, and the latest un-consumed approval (null when none).
type approvalDTO struct {
	Required bool                `json:"required"`
	Approval *approvals.Approval `json:"approval"`
}

// handleGetApproval reports the approval policy + the pending approval for the
// requested environment (any project reader).
func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	envName, _, err := s.resolveEnv(id, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := approvalDTO{Required: s.settings.ApprovalRequired(id)}
	if s.approvals != nil {
		if a, ok := s.approvals.Latest(id, envName); ok {
			out.Approval = a
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetApprovalPolicy enables/disables the apply approval gate (resources
// perm). Disabling is always allowed.
func (s *Server) handleSetApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		Required bool `json:"required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := s.settings.SetApprovalRequired(id, req.Required); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.handleGetApproval(w, r)
}

// handleApprove snapshots the current saved plan for a project + environment and
// records an approval (approve perm). The saved plan is stored verbatim so a
// later gated apply runs exactly what was reviewed. A missing plan (none run yet,
// or superseded) is a 409.
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if s.approvals == nil {
		writeErr(w, http.StatusServiceUnavailable, "approvals are not enabled on this server")
		return
	}
	envName, _, err := s.resolveEnv(id, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	planBytes, err := os.ReadFile(filepath.Join(s.runDir(id, envName), planFileName))
	if err != nil {
		writeErr(w, http.StatusConflict, "no plan to approve — run a plan for this environment first")
		return
	}
	// Display-only summary counts + the linked plan run come from the caller (the
	// UI's last plan); the authoritative artifact is the stored plan.
	var body struct {
		Add, Change, Destroy, Replace int
		RunID                         string `json:"runId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	a := &approvals.Approval{
		ID:         uuid.NewString(),
		ProjectID:  id,
		Env:        envName,
		RunID:      body.RunID,
		Add:        body.Add,
		Change:     body.Change,
		Destroy:    body.Destroy,
		Replace:    body.Replace,
		ApprovedAt: time.Now(),
	}
	if u, ok := userFromContext(r); ok {
		a.ApprovedBy = u.Username
	}
	if err := s.approvals.Create(a, planBytes); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// planFileName mirrors the runner's saved-plan filename, so the approve handler
// can read the artifact the last plan left in the workspace.
const planFileName = "tfplan"
