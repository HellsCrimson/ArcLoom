// Package runner wraps the Terraform/OpenTofu CLI behind a single interface so
// the rest of ArcLoom is agnostic to which engine executes a project.
package runner

import (
	"context"
	"io"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// PlanChange is one resource's pending action in a plan, extracted from the
// engine's structured plan JSON.
type PlanChange struct {
	// Address is the full resource address, e.g. "module.vpc.aws_subnet.a".
	Address string `json:"address"`
	// ModuleAddress is the enclosing module ("module.vpc"), empty at root.
	ModuleAddress string `json:"moduleAddress,omitempty"`
	Mode          string `json:"mode"` // "managed" | "data"
	Type          string `json:"type"`
	Name          string `json:"name"`
	// Action is the normalized action: create | update | replace | delete | read.
	Action string `json:"action"`
}

// PlanResult summarizes a completed plan.
type PlanResult struct {
	HasChanges bool `json:"hasChanges"`
	// Changes lists every resource with a pending action (no-ops excluded).
	// Nil when the structured plan could not be read (the raw log still
	// streamed, so the plan itself is not lost).
	Changes []PlanChange `json:"changes"`
	Add     int          `json:"add"`
	Change  int          `json:"change"`
	Destroy int          `json:"destroy"`
	Replace int          `json:"replace"`
	// Cost is the optional cost-estimation annotation. It is populated by the
	// API (from stored price overrides merged with Infracost) after the plan
	// runs, only when the cost feature is licensed and enabled for the project;
	// nil otherwise.
	Cost *CostResult `json:"cost,omitempty"`
}

// CostLine is one resource's estimated monthly cost in a plan's cost breakdown.
type CostLine struct {
	Address string  `json:"address"`
	Type    string  `json:"type"`
	Monthly float64 `json:"monthly"`
	// Source is where the figure came from: "override" (a user-defined flat
	// price) or "infracost" (Infracost's cloud pricing).
	Source string `json:"source"`
}

// CostResult is the estimated monthly cost of a plan's resources, merged from
// user-defined overrides and (optionally) Infracost pricing.
type CostResult struct {
	Currency      string     `json:"currency"`
	TotalMonthly  float64    `json:"totalMonthly"`
	Lines         []CostLine `json:"lines"`
	InfracostUsed bool       `json:"infracostUsed"`
	// Error is a short message when Infracost was expected but failed (e.g. not
	// installed, missing API key); the overrides-only breakdown is still valid.
	Error string `json:"error,omitempty"`
}

// LintIssue is one finding reported by tflint.
type LintIssue struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // "error" | "warning" | "notice"
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// LintResult summarizes a completed tflint run.
type LintResult struct {
	// Available is false when the tflint binary is not installed; Issues is then
	// empty and the UI shows an "install tflint" hint instead of "no issues".
	Available    bool        `json:"available"`
	Issues       []LintIssue `json:"issues"`
	ErrorCount   int         `json:"errorCount"`
	WarningCount int         `json:"warningCount"`
}

// Runner executes engine operations for a project. Implementations must be safe
// for concurrent use (each call operates in its own workspace).
type Runner interface {
	// Engine reports which CLI backend this runner drives.
	Engine() model.Engine
	// ExecPath is the resolved path to the CLI binary.
	ExecPath() string
	// Version returns the CLI version string (e.g. "1.12.1").
	Version(ctx context.Context) (string, error)
	// ProvidersSchema initializes the given providers and returns their raw
	// schemas.
	ProvidersSchema(ctx context.Context, providers []model.ProviderInstance) (*tfjson.ProviderSchemas, error)
	// Plan syncs the generated files (main.tf plus any modules/<name>/main.tf)
	// into the project's persistent workspace at dir, initializes it, and runs
	// plan against the persisted state, streaming CLI output to out. tfVars are
	// injected as TF_VAR_<name> (used for secret values) and never written to
	// disk. It returns whether changes are pending.
	Plan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (PlanResult, error)
	// RefreshPlan is Plan with -refresh-only: it reports drift between the
	// persisted state and the real infrastructure, ignoring config changes.
	// Changes come from the plan's resource_drift set.
	RefreshPlan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (PlanResult, error)
	// Apply syncs the generated files into the persistent workspace at dir,
	// initializes it, and applies the configuration. State is persisted in dir.
	Apply(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) error
	// ApplyPlan applies a previously saved plan artifact VERBATIM (no re-plan):
	// the workspace is synced + initialized like Apply, then planBytes is written
	// as the plan file and applied. Used by the plan → approve → apply gate so the
	// applied changes are exactly the ones that were reviewed. A stale plan (state
	// changed since it was created) is rejected by the engine.
	ApplyPlan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, planBytes []byte, out io.Writer) error
	// Destroy syncs the generated files into the persistent workspace at dir,
	// initializes it, and destroys all managed resources recorded in state.
	Destroy(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) error
	// Outputs reads the root output values from the state persisted in dir
	// (as left by the last apply).
	Outputs(ctx context.Context, dir string) (map[string]tfexec.OutputMeta, error)
	// Import adopts an existing real-world resource (identified by id) into the
	// persisted state under the given address. The workspace is synced and
	// initialized first, so the address must exist in the generated config.
	Import(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, address, id string, out io.Writer) error
	// StateRm removes an address from the persisted state (the real resource is
	// left untouched; a later plan will want to recreate it).
	StateRm(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, address string, out io.Writer) error
	// StateMv renames an address inside the persisted state, e.g. after a
	// resource was renamed in the model.
	StateMv(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, from, to string, out io.Writer) error
	// InitBackend re-initializes the workspace after the backend configuration
	// changed. migrate copies the existing state into the new backend
	// (-force-copy, implies -migrate-state); otherwise the new backend is used
	// as-is (-reconfigure) and the old state is left behind.
	InitBackend(ctx context.Context, dir string, files map[string][]byte, migrate bool, out io.Writer) error
	// StatePull returns the raw state JSON as the configured backend sees it
	// (works for both local and remote backends).
	StatePull(ctx context.Context, dir string) (string, error)
	// Validate syncs the generated files into the persistent workspace,
	// initializes it, and runs the engine's config validation, returning the
	// structured diagnostics.
	Validate(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (*tfjson.ValidateOutput, error)
	// Graph writes the generated files into a fresh workspace, initializes it,
	// and returns the dependency graph in Graphviz DOT format.
	Graph(ctx context.Context, files map[string][]byte) (string, error)
	// Lint runs tflint against the generated files in a throwaway workspace (no
	// engine init or state needed) and returns its findings. A missing tflint
	// binary is not an error: the result reports Available=false.
	Lint(ctx context.Context, files map[string][]byte, out io.Writer) (LintResult, error)
	// Infracost estimates monthly costs from the plan file left in the
	// persistent workspace at dir by the preceding Plan (it reads <dir>/tfplan,
	// so a plan must have run first). It returns address -> monthly USD. A
	// missing infracost binary or any failure returns an error and the caller
	// degrades to overrides-only pricing.
	Infracost(ctx context.Context, dir string) (map[string]float64, error)
}
