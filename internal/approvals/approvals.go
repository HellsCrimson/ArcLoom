// Package approvals persists plan-approval records for the plan → approve →
// apply gate. When a project requires approval, an apply may only run a plan
// that a user with the approve permission has explicitly signed off on; the
// exact saved plan artifact is stored here and applied verbatim, so what is
// applied is precisely what was reviewed.
//
// It is a sibling of internal/runs: one JSON metadata file plus the raw saved
// plan (a Terraform/OpenTofu `-out` plan file) per approval, on disk per project.
package approvals

import (
	"time"
)

// Approval records that a specific saved plan was signed off for apply. It is
// per (project, environment) and single-use: a successful gated apply consumes
// it. The plan summary counts are display metadata; the authoritative artifact
// is the stored plan file applied verbatim.
type Approval struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	Env        string    `json:"env,omitempty"` // "" = default workspace
	RunID      string    `json:"runId,omitempty"`
	Add        int       `json:"add"`
	Change     int       `json:"change"`
	Destroy    int       `json:"destroy"`
	Replace    int       `json:"replace"`
	ApprovedBy string    `json:"approvedBy"`
	ApprovedAt time.Time `json:"approvedAt"`
	Consumed   bool      `json:"consumed"`
	ConsumedAt time.Time `json:"consumedAt,omitempty"`
}

// PendingPlan is a plan waiting to be approved: the artifact a project's latest
// plan produced for an environment, plus the summary of what it would do. The
// summary is taken from the plan itself, so the approval record is truthful even
// when the person approving is not the person who planned — which is the whole
// point of a separate approve permission.
type PendingPlan struct {
	ProjectID string    `json:"projectId"`
	Env       string    `json:"env,omitempty"` // "" = default workspace
	RunID     string    `json:"runId,omitempty"`
	Add       int       `json:"add"`
	Change    int       `json:"change"`
	Destroy   int       `json:"destroy"`
	Replace   int       `json:"replace"`
	PlannedBy string    `json:"plannedBy,omitempty"`
	PlannedAt time.Time `json:"plannedAt"`
	// ConfigHash fingerprints the model the plan was made from. A stashed plan now
	// outlives the workspace, so it can outlive the configuration too: approving it
	// after an edit would sign off changes nobody reviewed (and the engine would
	// then reject the stale artifact mid-apply). The approve handler compares.
	ConfigHash string `json:"configHash,omitempty"`
}

// Store persists approval records + their saved plan artifacts. Implementations
// must be safe for concurrent use.
type Store interface {
	// Create records an approval and stores its saved plan bytes verbatim.
	Create(a *Approval, planBytes []byte) error
	// Latest returns the newest UNCONSUMED approval for a (project, env), if any.
	Latest(projectID, env string) (*Approval, bool)
	// Plan returns the stored plan artifact for an approval id.
	Plan(projectID, id string) ([]byte, error)
	// Consume marks an approval used (after a successful apply).
	Consume(projectID, id string) error
	// InvalidateEnv consumes every pending approval for a (project, env). Called
	// when a new plan supersedes them, so a gated apply can never run a saved plan
	// that no longer reflects the latest review.
	InvalidateEnv(projectID, env string) error
	// StashPlan keeps the artifact a plan just produced, replacing any previous
	// stash for its (project, env). The CLI workspace is not a safe home for it:
	// every subsequent operation (another user's plan, a scheduled drift check)
	// syncs the workspace and deletes the saved plan, which would leave nothing
	// for a later approval to snapshot.
	StashPlan(p *PendingPlan, planBytes []byte) error
	// StashedPlan returns the plan waiting to be approved for (project, env), or
	// ErrNotFound when there is none.
	StashedPlan(projectID, env string) (*PendingPlan, []byte, error)
	// DropStash removes the stashed plan for (project, env). Called once it has
	// been turned into an approval, which owns its own copy.
	DropStash(projectID, env string) error
	// List returns a project's approvals, newest first.
	List(projectID string) ([]*Approval, error)
	// DeleteProject removes all approvals recorded for a project.
	DeleteProject(projectID string) error
}
