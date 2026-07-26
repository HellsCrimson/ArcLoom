package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/approvals"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
)

// approvalDTO is the approval-gate state for a project + environment: whether
// applies are gated, the latest un-consumed approval (null when none), and the
// plan waiting to be approved (null when none). planPending is what lets a user
// who did not run the plan — the reason the approve permission is separate —
// see that there is something to approve.
type approvalDTO struct {
	Required    bool                   `json:"required"`
	Approval    *approvals.Approval    `json:"approval"`
	PlanPending *approvals.PendingPlan `json:"planPending"`
}

// handleGetApproval reports the approval policy + the pending approval for the
// requested environment (any project reader).
func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request) {
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
	out := approvalDTO{Required: s.settings.ApprovalRequired(id)}
	if s.approvals != nil {
		if a, ok := s.approvals.Latest(id, envName); ok {
			out.Approval = a
		}
		// A plan the configuration has moved on from is not offered: the Approve
		// button must never light up for something approve would then refuse.
		if pending, _, err := s.approvals.StashedPlan(id, envName); err == nil && pending.ConfigHash == configHash(p) {
			out.PlanPending = pending
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

// stashPlan copies the artifact a plan just saved in the CLI workspace into the
// approvals store, together with what that plan would do. It has to leave the
// workspace: the next operation of any kind — another user's plan, a scheduled
// drift check — syncs the workspace and deletes the saved plan, which used to
// make the following Approve fail with "no plan to approve". Runs in the same
// window as attachCost/attachPolicy, while the file still exists.
// Best-effort: a plan the user watched succeed must not fail after the fact.
func (s *Server) stashPlan(dir string, p *model.Project, rec *runs.Run, res *runner.PlanResult) {
	planBytes, err := os.ReadFile(filepath.Join(dir, planFileName))
	if err == nil {
		err = s.approvals.StashPlan(&approvals.PendingPlan{
			ProjectID:  rec.ProjectID,
			Env:        rec.Env,
			RunID:      rec.ID,
			Add:        res.Add,
			Change:     res.Change,
			Destroy:    res.Destroy,
			Replace:    res.Replace,
			PlannedBy:  rec.User,
			PlannedAt:  time.Now(),
			ConfigHash: configHash(p),
		}, planBytes)
	}
	if err != nil {
		slog.Warn("stash plan for approval", "project", rec.ProjectID, "env", rec.Env, "err", err)
	}
}

// configHash fingerprints the model a plan was made from, so an edit between
// plan and approve can be detected. json.Marshal is deterministic for a given
// value (fixed field order, map keys sorted), which is all this needs — it is a
// change detector, not a signature. An unmarshalable project (there is no such
// thing) yields "", which compares equal to a stash written the same way.
func configHash(p *model.Project) string {
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// handleApprove snapshots the plan the project's last plan stashed for this
// environment and records an approval (approve perm). The plan is stored
// verbatim so a later gated apply runs exactly what was reviewed. A missing plan
// (none run yet, or superseded by a newer plan that was itself approved) is a 409.
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.store.Get(id)
	if err != nil {
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
	pending, planBytes, err := s.approvals.StashedPlan(id, envName)
	if err != nil {
		writeErr(w, http.StatusConflict, "no plan to approve — run a plan for this environment first")
		return
	}
	// The stash is not dropped on a mismatch: undoing the edit makes this plan
	// current again, which is exactly what the undo history is for.
	if pending.ConfigHash != configHash(p) {
		writeErr(w, http.StatusConflict, "the configuration changed since that plan — run a new plan before approving")
		return
	}
	// The summary describes the stored artifact, so it comes from the plan itself
	// rather than from whoever is clicking Approve — they may never have run it.
	a := &approvals.Approval{
		ID:         uuid.NewString(),
		ProjectID:  id,
		Env:        envName,
		RunID:      pending.RunID,
		Add:        pending.Add,
		Change:     pending.Change,
		Destroy:    pending.Destroy,
		Replace:    pending.Replace,
		ApprovedAt: time.Now(),
	}
	if u, ok := userFromContext(r); ok {
		a.ApprovedBy = u.Username
	}
	if err := s.approvals.Create(a, planBytes); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The approval owns its own copy now. Dropping the stash keeps approval
	// single-use per plan: re-applying means planning again, not re-approving the
	// same artifact after the first apply consumed it.
	if err := s.approvals.DropStash(id, envName); err != nil {
		slog.Warn("drop stashed plan after approval", "project", id, "env", envName, "err", err)
	}
	writeJSON(w, http.StatusOK, a)
}
