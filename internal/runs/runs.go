// Package runs records the history of engine operations (plan/apply/destroy)
// per project: who ran what, when, the outcome, the streamed log, and any
// outputs an apply produced.
package runs

import (
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/runner"
)

// Output is one root output value captured after a successful apply. Sensitive
// values are never stored; Value holds "(sensitive)" instead.
type Output struct {
	Name      string `json:"name"`
	Value     string `json:"value"` // raw JSON encoding of the value
	Sensitive bool   `json:"sensitive"`
}

// Run is one recorded engine operation.
type Run struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	Op         string    `json:"op"`   // "plan" | "apply" | "destroy"
	User       string    `json:"user"` // username; empty when auth is disabled
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Status     string    `json:"status"` // "success" | "error"
	Error      string    `json:"error,omitempty"`
	// Plan carries the structured summary for plan runs (its Cost field holds
	// the cost breakdown when cost estimation is enabled).
	Plan *runner.PlanResult `json:"plan,omitempty"`
	// Lint carries tflint findings for lint runs.
	Lint *runner.LintResult `json:"lint,omitempty"`
	// Outputs carries the root outputs for successful applies.
	Outputs []Output `json:"outputs,omitempty"`
	// Log is the full streamed CLI output.
	Log string `json:"log,omitempty"`
}

// Store persists run records. Implementations must be safe for concurrent use.
type Store interface {
	// Append records a finished run and prunes the project's history to the
	// retention cap.
	Append(r *Run) error
	// List returns a project's runs, newest first, with Log stripped (fetch a
	// single run via Get for the log).
	List(projectID string) ([]*Run, error)
	// Get returns one full run record.
	Get(projectID, id string) (*Run, error)
	// DeleteProject removes all runs recorded for a project.
	DeleteProject(projectID string) error
}
