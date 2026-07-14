// Package runner wraps the Terraform/OpenTofu CLI behind a single interface so
// the rest of ArcLoom is agnostic to which engine executes a project.
package runner

import (
	"context"
	"io"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/matthiasrauline/arcloom/internal/model"
)

// PlanResult summarizes a completed plan.
type PlanResult struct {
	HasChanges bool `json:"hasChanges"`
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
	// Plan writes the generated files (main.tf plus any modules/<name>/main.tf)
	// into a fresh workspace, initializes it, and runs plan, streaming CLI output
	// to out. tfVars are injected as TF_VAR_<name> (used for secret values) and
	// never written to disk. It returns whether changes are pending.
	Plan(ctx context.Context, files map[string][]byte, providers []model.ProviderInstance, tfVars map[string]string, out io.Writer) (PlanResult, error)
	// Graph writes the generated files into a fresh workspace, initializes it,
	// and returns the dependency graph in Graphviz DOT format.
	Graph(ctx context.Context, files map[string][]byte) (string, error)
}
