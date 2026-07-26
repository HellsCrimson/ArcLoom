package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// planEnvMu serializes the process-env mutation used to inject TF_VAR_* secrets
// into a plan/apply/destroy, since the environment is process-global.
var planEnvMu sync.Mutex

// dirLocks serializes operations on the same persistent workspace so a plan and
// an apply on one project cannot race over state and .terraform/.
var dirLocks sync.Map // map[string]*sync.Mutex

func lockDir(dir string) (unlock func()) {
	m, _ := dirLocks.LoadOrStore(dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// TfExec is a Runner backed by terraform-exec. The same code path drives both
// OpenTofu and Terraform; only execPath differs.
type TfExec struct {
	engine   model.Engine
	execPath string
	workBase string
}

// New resolves the CLI binary for the given engine and returns a runner. If
// execPath is empty it is looked up on PATH. The plugin cache is exported to the
// process environment so tfexec (which inherits os.Environ) shares it.
func New(engine model.Engine, execPath, workBase, pluginCacheDir string) (*TfExec, error) {
	if execPath == "" {
		p, err := exec.LookPath(string(engine))
		if err != nil {
			return nil, fmt.Errorf("%s binary not found on PATH: %w", engine, err)
		}
		execPath = p
	}
	if pluginCacheDir != "" {
		if os.MkdirAll(pluginCacheDir, 0o755) == nil {
			os.Setenv("TF_PLUGIN_CACHE_DIR", pluginCacheDir)
		}
	}
	return &TfExec{engine: engine, execPath: execPath, workBase: workBase}, nil
}

func (t *TfExec) Engine() model.Engine { return t.engine }
func (t *TfExec) ExecPath() string     { return t.execPath }

func (t *TfExec) Version(ctx context.Context) (string, error) {
	ws, err := newWorkspace(t.workBase, t.execPath, nil)
	if err != nil {
		return "", err
	}
	defer ws.close()
	tf, err := ws.terraform()
	if err != nil {
		return "", err
	}
	ver, _, err := tf.Version(ctx, false)
	if err != nil {
		return "", err
	}
	return ver.String(), nil
}

func (t *TfExec) ProvidersSchema(ctx context.Context, providers []model.ProviderInstance) (*tfjson.ProviderSchemas, error) {
	cfg, err := hclgen.ProvidersConfig(providers)
	if err != nil {
		return nil, fmt.Errorf("render providers config: %w", err)
	}
	ws, err := newWorkspace(t.workBase, t.execPath, map[string][]byte{
		"providers.tf": cfg,
	})
	if err != nil {
		return nil, err
	}
	defer ws.close()

	tf, err := ws.terraform()
	if err != nil {
		return nil, err
	}
	if err := tf.Init(ctx); err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}
	return tf.ProvidersSchema(ctx)
}

// prepare syncs files into the persistent workspace at dir, injects tfVars as
// TF_VAR_* into the process environment, and returns an initialized tfexec
// handle. The returned cleanup releases the env and the per-dir lock and must
// be called even on error (it is safe when tf is nil).
func (t *TfExec) prepare(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (tf *tfexec.Terraform, cleanup func(), err error) {
	unlockDir := lockDir(dir)
	cleanup = unlockDir

	ws, err := newPersistentWorkspace(dir, t.execPath, files)
	if err != nil {
		return nil, cleanup, err
	}
	tf, err = ws.terraform()
	if err != nil {
		return nil, cleanup, err
	}
	if out != nil {
		tf.SetStdout(out)
		tf.SetStderr(out)
	}

	// Inject secret values as TF_VAR_* into the process environment (tfexec
	// inherits os.Environ; its SetEnv rejects TF_VAR_*). Serialize because the
	// environment is process-global; values are removed by cleanup.
	if len(tfVars) > 0 {
		planEnvMu.Lock()
		keys := make([]string, 0, len(tfVars))
		for name, val := range tfVars {
			key := "TF_VAR_" + name
			os.Setenv(key, val)
			keys = append(keys, key)
		}
		cleanup = func() {
			for _, k := range keys {
				os.Unsetenv(k)
			}
			planEnvMu.Unlock()
			unlockDir()
		}
	}

	// Pass per-env `-backend-config` values when the workspace carries them
	// (partial backend). Within one env's workspace the config is stable across
	// runs, so a plain init stays idempotent; a changed config needs InitBackend
	// (reconfigure), same as any backend change.
	initOpts := ws.backendInitOpts()
	if err := tf.Init(ctx, initOpts...); err != nil {
		// When a provider's version constraint changed, .terraform.lock.hcl (kept
		// across the workspace re-sync) still pins the old version, so a plain init
		// fails asking for -upgrade. Retry once allowing the lock to move to a
		// version satisfying the new constraint. Scoped to this specific failure so
		// -upgrade is not run on every op (which would needlessly re-resolve every
		// provider).
		if !isLockConstraintMismatch(err) {
			return nil, cleanup, fmt.Errorf("init: %w", err)
		}
		if err := tf.Init(ctx, append(initOpts, tfexec.Upgrade(true))...); err != nil {
			return nil, cleanup, fmt.Errorf("init -upgrade: %w", err)
		}
	}
	return tf, cleanup, nil
}

// isLockConstraintMismatch reports whether an init error is the "dependency lock
// no longer satisfies the configured provider constraints" failure — the one
// Terraform/OpenTofu tell you to fix with `init -upgrade`. Matched on the
// diagnostic text (terraform-exec surfaces it in the error) since there is no
// typed error for it.
func isLockConstraintMismatch(err error) bool {
	s := err.Error()
	return strings.Contains(s, "does not match configured version constraint") ||
		strings.Contains(s, "locked dependencies") ||
		strings.Contains(s, "init -upgrade")
}

// planFileName is the saved plan inside the persistent workspace. It is not in
// persistKeep, so the next workspace sync removes it.
const planFileName = "tfplan"

// driftPlanFileName is where a scheduled drift check saves its plan. It must NOT
// be planFileName: that artifact belongs to the user's own plan (the approval
// gate snapshots it, and the policy gate applies it), and a background check must
// never substitute its own.
const driftPlanFileName = "tfplan.drift"

// Plan runs plan in the project's persistent workspace, so the diff is against
// the state left by the last apply (empty state on a fresh project). The plan
// is saved to a file and re-read as JSON to extract per-resource changes.
func (t *TfExec) Plan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (PlanResult, error) {
	return t.plan(ctx, dir, files, tfVars, out, planFileName)
}

// DriftPlan is the scheduled drift check. It runs a NORMAL plan, so what it
// reports is what a plan (and an apply) would act on: the difference between the
// configuration and reality. Comparing the recorded state against reality instead
// — plan -refresh-only, which is what this used to do — flags every attribute a
// provider recomputes on each read (timestamps, statuses, hashes), so a project
// whose infrastructure matches its config perfectly would report drift forever.
// The plan's own resource_drift set is carried in PlanResult.Drift so the caller
// can still tell an out-of-band change from an unapplied config edit.
func (t *TfExec) DriftPlan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (PlanResult, error) {
	return t.plan(ctx, dir, files, tfVars, out, driftPlanFileName)
}

// plan runs a plan into outFile and reads it back. A plan saved anywhere other
// than planFileName is a throwaway (the drift check) and is removed afterwards.
func (t *TfExec) plan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer, outFile string) (PlanResult, error) {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return PlanResult{}, err
	}
	if outFile != planFileName {
		defer os.Remove(filepath.Join(dir, outFile))
	}
	hasChanges, err := tf.Plan(ctx, tfexec.Out(outFile))
	if err != nil {
		return PlanResult{}, fmt.Errorf("plan: %w", err)
	}
	res := PlanResult{HasChanges: hasChanges}

	// Read the structured plan. Detach the streaming writers first: tfexec
	// merges its JSON capture buffer with the configured stdout, which would
	// dump the whole plan JSON into the streamed log. Failure here degrades to
	// a log-only plan rather than failing the operation.
	tf.SetStdout(io.Discard)
	tf.SetStderr(io.Discard)
	plan, err := tf.ShowPlanFile(ctx, outFile)
	if err != nil || plan == nil {
		return res, nil
	}
	// resource_drift is what the refresh found changed outside this workspace
	// (state vs reality). It is informational: only ResourceChanges says what an
	// apply would do.
	for _, rd := range plan.ResourceDrift {
		if rd.Change == nil {
			continue
		}
		res.Drift = append(res.Drift, PlanChange{
			Address: rd.Address, ModuleAddress: rd.ModuleAddress,
			Mode: string(rd.Mode), Type: rd.Type, Name: rd.Name,
			Action: actionName(rd.Change.Actions),
		})
	}
	// diffBudget bounds the attribute-level diffs across the whole plan.
	diffBudget := maxPlanDiffs
	for _, rc := range plan.ResourceChanges {
		if rc.Change == nil {
			continue
		}
		action := actionName(rc.Change.Actions)
		if action == "" {
			continue // no-op
		}
		ch := PlanChange{
			Address:       rc.Address,
			ModuleAddress: rc.ModuleAddress,
			Mode:          string(rc.Mode),
			Type:          rc.Type,
			Name:          rc.Name,
			Action:        action,
		}
		if action == "update" || action == "replace" {
			ch.Diffs, ch.DiffsTruncated = attributeDiffs(rc.Change, min(maxAttrDiffs, diffBudget))
			diffBudget -= len(ch.Diffs)
		}
		res.Changes = append(res.Changes, ch)
		switch action {
		case "create":
			res.Add++
		case "update":
			res.Change++
		case "delete":
			res.Destroy++
		case "replace":
			res.Replace++
		}
	}
	return res, nil
}

// actionName normalizes a plan action set; "" means no-op.
func actionName(a tfjson.Actions) string {
	switch {
	case a.Replace():
		return "replace"
	case a.Create():
		return "create"
	case a.Update():
		return "update"
	case a.Delete():
		return "delete"
	case a.Read():
		return "read"
	}
	return ""
}

// Apply applies the configuration; state is persisted in dir for later plans,
// applies and destroys.
func (t *TfExec) Apply(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.Apply(ctx); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	return nil
}

// ApplyPlan applies a saved plan artifact verbatim. The workspace is synced +
// initialized (prepare) like Apply, then the approved plan bytes are written as
// the plan file and applied with no re-plan, so the applied changes are exactly
// those reviewed. prepare's sync wipes any stale plan file, so it is written
// after prepare returns.
func (t *TfExec) ApplyPlan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, planBytes []byte, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, planFileName), planBytes, 0o644); err != nil {
		return fmt.Errorf("stage approved plan: %w", err)
	}
	if err := tf.Apply(ctx, tfexec.DirOrPlan(planFileName)); err != nil {
		return fmt.Errorf("apply approved plan: %w", err)
	}
	return nil
}

// Outputs reads root output values from the state persisted in dir. It does
// not sync files or init: the workspace is whatever the last operation left.
func (t *TfExec) Outputs(ctx context.Context, dir string) (map[string]tfexec.OutputMeta, error) {
	unlock := lockDir(dir)
	defer unlock()
	tf, err := tfexec.NewTerraform(dir, t.execPath)
	if err != nil {
		return nil, fmt.Errorf("init tfexec: %w", err)
	}
	out, err := tf.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}
	return out, nil
}

// Import adopts a live resource into the persisted state. It goes through the
// same prepare path as plan/apply (workspace sync + init + secret TF_VAR_*
// injection) because the provider must authenticate to read the resource.
func (t *TfExec) Import(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, address, id string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.Import(ctx, address, id); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	return nil
}

// StateRm removes an address from the persisted state.
func (t *TfExec) StateRm(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, address string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.StateRm(ctx, address); err != nil {
		return fmt.Errorf("state rm: %w", err)
	}
	return nil
}

// Taint marks an instance as damaged, so the next apply replaces it. The address
// must name a single instance (`x.y`, or `x.y[0]` for a counted resource).
func (t *TfExec) Taint(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, address string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.Taint(ctx, address); err != nil {
		return fmt.Errorf("taint: %w", err)
	}
	return nil
}

// Untaint clears the tainted mark, so a resource the engine considers damaged is
// kept as-is. This is the state op with no alternative: an instance left tainted
// by a failed create-time provisioner is otherwise replaced on the next apply.
func (t *TfExec) Untaint(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, address string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.Untaint(ctx, address); err != nil {
		return fmt.Errorf("untaint: %w", err)
	}
	return nil
}

// StateMv renames an address inside the persisted state.
func (t *TfExec) StateMv(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, from, to string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.StateMv(ctx, from, to); err != nil {
		return fmt.Errorf("state mv: %w", err)
	}
	return nil
}

// InitBackend re-initializes the persistent workspace after a backend change.
// It cannot reuse prepare (whose plain init would fail with "backend
// configuration changed"); everything else — sync, lock — matches.
func (t *TfExec) InitBackend(ctx context.Context, dir string, files map[string][]byte, migrate bool, out io.Writer) error {
	unlock := lockDir(dir)
	defer unlock()

	ws, err := newPersistentWorkspace(dir, t.execPath, files)
	if err != nil {
		return err
	}
	tf, err := ws.terraform()
	if err != nil {
		return err
	}
	if out != nil {
		tf.SetStdout(out)
		tf.SetStderr(out)
	}
	// Include any per-env backend config (partial backend) alongside the
	// migrate/reconfigure flag.
	bc := ws.backendInitOpts()
	if migrate {
		// -force-copy implies -migrate-state and answers the copy prompt.
		if err := tf.Init(ctx, append(bc, tfexec.ForceCopy(true))...); err != nil {
			return fmt.Errorf("init -migrate-state: %w", err)
		}
		return nil
	}
	if err := tf.Init(ctx, append(bc, tfexec.Reconfigure(true))...); err != nil {
		return fmt.Errorf("init -reconfigure: %w", err)
	}
	return nil
}

// StatePull returns the raw state JSON from whatever backend the workspace is
// initialized against. Like Outputs it does not sync or init.
func (t *TfExec) StatePull(ctx context.Context, dir string) (string, error) {
	unlock := lockDir(dir)
	defer unlock()
	tf, err := tfexec.NewTerraform(dir, t.execPath)
	if err != nil {
		return "", fmt.Errorf("init tfexec: %w", err)
	}
	state, err := tf.StatePull(ctx)
	if err != nil {
		return "", fmt.Errorf("state pull: %w", err)
	}
	return state, nil
}

// AcceptDrift persists the refreshed state without touching any infrastructure
// (apply -refresh-only): the state catches up to reality, which is the only way
// to clear a refresh-only drift report — a plan -refresh-only never writes the
// refreshed values back, so the same drift is reported on every check. Nothing
// is created, updated or destroyed.
func (t *TfExec) AcceptDrift(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.Apply(ctx, tfexec.RefreshOnly(true)); err != nil {
		return fmt.Errorf("apply -refresh-only: %w", err)
	}
	return nil
}

// StatePush writes a caller-supplied state into whatever backend the workspace
// is configured for, so an existing project's state can be adopted. It goes
// through prepare (sync + init) because a remote backend must be initialized
// first, and pushes with -force: an imported state always has a different
// lineage than the (usually empty) one the workspace holds, which the engine
// would otherwise refuse. Guarding against overwriting a live state is the
// caller's job.
func (t *TfExec) StatePush(ctx context.Context, dir string, files map[string][]byte, stateJSON []byte, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, nil, out)
	defer cleanup()
	if err != nil {
		return err
	}
	// The state is staged next to the workspace (not inside it, where a sync
	// would wipe it and the engine might read it as its own state file).
	f, err := os.CreateTemp("", "arcloom-state-*.tfstate")
	if err != nil {
		return fmt.Errorf("stage state: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(stateJSON); err != nil {
		f.Close()
		return fmt.Errorf("stage state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("stage state: %w", err)
	}
	if err := tf.StatePush(ctx, path, tfexec.Force(true)); err != nil {
		return fmt.Errorf("state push: %w", err)
	}
	return nil
}

// Validate runs config validation in the persistent workspace (same prepare
// path as plan: sync + init). The streaming writers are detached before the
// validate call itself — tfexec merges its JSON capture buffer with the
// configured stdout, which would dump the raw diagnostics JSON into the log.
func (t *TfExec) Validate(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (*tfjson.ValidateOutput, error) {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return nil, err
	}
	tf.SetStdout(io.Discard)
	tf.SetStderr(io.Discard)
	res, err := tf.Validate(ctx)
	if err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	return res, nil
}

// Destroy destroys everything recorded in the persisted state.
func (t *TfExec) Destroy(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) error {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return err
	}
	if err := tf.Destroy(ctx); err != nil {
		return fmt.Errorf("destroy: %w", err)
	}
	return nil
}

// Graph initializes a workspace with the given config and returns the
// dependency graph as Graphviz DOT.
func (t *TfExec) Graph(ctx context.Context, files map[string][]byte) (string, error) {
	ws, err := newWorkspace(t.workBase, t.execPath, files)
	if err != nil {
		return "", err
	}
	defer ws.close()

	tf, err := ws.terraform()
	if err != nil {
		return "", err
	}
	if err := tf.Init(ctx); err != nil {
		return "", fmt.Errorf("init: %w", err)
	}
	dot, err := tf.Graph(ctx)
	if err != nil {
		return "", fmt.Errorf("graph: %w", err)
	}
	return dot, nil
}

// Lint runs tflint against the generated files in a throwaway workspace. tflint
// analyzes the HCL directly, so no engine init, state or providers are needed.
// A missing tflint binary yields Available=false rather than an error, so a
// homelab without tflint installed still gets a clean (empty) result.
func (t *TfExec) Lint(ctx context.Context, files map[string][]byte, out io.Writer) (LintResult, error) {
	binPath, err := exec.LookPath("tflint")
	if err != nil {
		return LintResult{Available: false}, nil
	}
	ws, err := newWorkspace(t.workBase, t.execPath, files)
	if err != nil {
		return LintResult{}, err
	}
	defer ws.close()

	// Best-effort ruleset install (only does anything when a .tflint.hcl config
	// is present); failure is non-fatal — the bundled rules still run.
	initCmd := exec.CommandContext(ctx, binPath, "--init")
	initCmd.Dir = ws.dir
	if out != nil {
		initCmd.Stdout = out
		initCmd.Stderr = out
	}
	_ = initCmd.Run()

	// tflint exits non-zero (2) when it finds issues; the JSON on stdout is
	// still valid, so parse regardless of exit status. stderr streams to the log.
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, binPath, "--format=json")
	cmd.Dir = ws.dir
	cmd.Stdout = &stdout
	if out != nil {
		cmd.Stderr = out
	}
	_ = cmd.Run()

	res, err := parseTflint(stdout.Bytes())
	if err != nil {
		return LintResult{Available: true}, fmt.Errorf("parse tflint output: %w", err)
	}
	res.Available = true
	return res, nil
}

// tflintOutput is the subset of `tflint --format json` we consume.
type tflintOutput struct {
	Issues []struct {
		Rule struct {
			Name     string `json:"name"`
			Severity string `json:"severity"`
		} `json:"rule"`
		Message string `json:"message"`
		Range   struct {
			Filename string `json:"filename"`
			Start    struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
	} `json:"issues"`
	Errors []struct {
		Message  string `json:"message"`
		Severity string `json:"severity"`
	} `json:"errors"`
}

func parseTflint(data []byte) (LintResult, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return LintResult{Issues: []LintIssue{}}, nil
	}
	var out tflintOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return LintResult{}, err
	}
	res := LintResult{Issues: make([]LintIssue, 0, len(out.Issues)+len(out.Errors))}
	add := func(iss LintIssue) {
		switch iss.Severity {
		case "error":
			res.ErrorCount++
		case "warning":
			res.WarningCount++
		}
		res.Issues = append(res.Issues, iss)
	}
	for _, i := range out.Issues {
		add(LintIssue{
			Rule:     i.Rule.Name,
			Severity: strings.ToLower(i.Rule.Severity),
			Message:  i.Message,
			Filename: i.Range.Filename,
			Line:     i.Range.Start.Line,
		})
	}
	// tflint reports config/parse problems in a separate "errors" array.
	for _, e := range out.Errors {
		sev := strings.ToLower(e.Severity)
		if sev == "" {
			sev = "error"
		}
		add(LintIssue{Rule: "tflint", Severity: sev, Message: e.Message})
	}
	return res, nil
}

// showPlanJSON converts a saved plan file in dir into JSON at outPath using the
// engine's `show -json` (which needs no provider access — the plan is already
// resolved). Callers hold the workspace lock.
func (t *TfExec) showPlanJSON(ctx context.Context, dir, planFile, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	var showErr bytes.Buffer
	show := exec.CommandContext(ctx, t.execPath, "show", "-json", planFile)
	show.Dir = dir
	show.Stdout = f
	show.Stderr = &showErr
	runErr := show.Run()
	f.Close()
	if runErr != nil {
		return fmt.Errorf("read saved plan: %w: %s", runErr, strings.TrimSpace(showErr.String()))
	}
	return nil
}

// policyPlanFile is where an explicitly supplied plan artifact (an approved
// plan) is staged for a policy check, so the workspace's own saved plan is left
// alone. Like tfplan it is not in persistKeep.
const policyPlanFile = "tfplan.policy"

// Policy evaluates the project's Rego documents against the plan JSON with
// conftest. The plan is converted to JSON the same way Infracost consumes it, so
// the policies see the standard Terraform plan representation
// (input.resource_changes, input.planned_values, ...). A missing conftest binary
// yields Available=false rather than an error; the caller decides whether that
// blocks (enforcing) or is ignored (advisory).
func (t *TfExec) Policy(ctx context.Context, dir string, planBytes []byte, policies map[string][]byte, out io.Writer) (PolicyResult, error) {
	binPath, err := exec.LookPath("conftest")
	if err != nil {
		return PolicyResult{Available: false}, nil
	}
	if len(policies) == 0 {
		return PolicyResult{Available: true}, nil
	}
	unlock := lockDir(dir)
	defer unlock()

	planFile := planFileName
	if planBytes != nil {
		planFile = policyPlanFile
		if err := os.WriteFile(filepath.Join(dir, planFile), planBytes, 0o600); err != nil {
			return PolicyResult{Available: true}, fmt.Errorf("stage plan for policy check: %w", err)
		}
		defer os.Remove(filepath.Join(dir, planFile))
	}

	planJSON := filepath.Join(dir, "policy-plan.json")
	defer os.Remove(planJSON)
	if err := t.showPlanJSON(ctx, dir, planFile, planJSON); err != nil {
		return PolicyResult{Available: true}, err
	}

	// Policies live in a throwaway directory: they are stored per project (not
	// on disk in the workspace) and must not survive the check.
	policyDir, err := os.MkdirTemp("", "arcloom-policy-")
	if err != nil {
		return PolicyResult{Available: true}, err
	}
	defer os.RemoveAll(policyDir)
	for name, src := range policies {
		if err := os.WriteFile(filepath.Join(policyDir, filepath.Base(name)), src, 0o600); err != nil {
			return PolicyResult{Available: true}, fmt.Errorf("write policy %s: %w", name, err)
		}
	}

	// conftest exits 1 when a policy denies, so the exit status is not an error
	// signal; the JSON on stdout is authoritative. --all-namespaces picks up
	// rules regardless of the package name the user wrote.
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binPath, "test", "--policy", policyDir, "--output", "json", "--all-namespaces", planJSON)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	res, parseErr := parseConftest(stdout.Bytes())
	if parseErr != nil {
		// No usable JSON: a Rego compile error or a conftest failure. Surface it
		// (an enforcing check must not pass because the policies are broken).
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		if msg == "" {
			msg = parseErr.Error()
		}
		if out != nil {
			fmt.Fprintln(out, "policy: "+msg)
		}
		return PolicyResult{Available: true}, fmt.Errorf("conftest: %s", firstLine(msg))
	}
	res.Available = true
	res.Checked = true
	return res, nil
}

// conftestOutput is the subset of `conftest test --output json` we consume. It
// is an array with one entry per (file, namespace). successes is an int in
// current conftest and an array in older releases, hence the raw decode.
type conftestOutput []struct {
	Namespace string          `json:"namespace"`
	Successes json.RawMessage `json:"successes"`
	Failures  []conftestRule  `json:"failures"`
	Warnings  []conftestRule  `json:"warnings"`
}

type conftestRule struct {
	Msg      string `json:"msg"`
	Metadata struct {
		Query string `json:"query"`
	} `json:"metadata"`
}

func parseConftest(data []byte) (PolicyResult, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return PolicyResult{}, fmt.Errorf("conftest produced no output")
	}
	var out conftestOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return PolicyResult{}, err
	}
	res := PolicyResult{Violations: []PolicyViolation{}}
	for _, r := range out {
		res.Successes += countSuccesses(r.Successes)
		for _, f := range r.Failures {
			res.Violations = append(res.Violations, PolicyViolation{
				Severity: "failure", Message: f.Msg, Namespace: r.Namespace, Rule: f.Metadata.Query,
			})
			res.FailureCount++
		}
		for _, wv := range r.Warnings {
			res.Violations = append(res.Violations, PolicyViolation{
				Severity: "warning", Message: wv.Msg, Namespace: r.Namespace, Rule: wv.Metadata.Query,
			})
			res.WarningCount++
		}
	}
	return res, nil
}

// countSuccesses reads conftest's "successes" field, which is a count in
// current releases and was a list of rules in older ones.
func countSuccesses(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		return len(list)
	}
	return 0
}

// Infracost estimates monthly costs from the binary plan file left by the
// preceding Plan. It converts <dir>/tfplan to JSON via the engine's `show
// -json`, then runs `infracost breakdown` against that JSON (so Infracost needs
// neither provider access nor the secret TF_VARs — the plan is already
// resolved). Returns address -> monthly USD.
func (t *TfExec) Infracost(ctx context.Context, dir string) (map[string]float64, error) {
	binPath, err := exec.LookPath("infracost")
	if err != nil {
		return nil, fmt.Errorf("infracost not installed")
	}
	unlock := lockDir(dir)
	defer unlock()

	planJSON := filepath.Join(dir, "plan.json")
	defer os.Remove(planJSON)
	if err := t.showPlanJSON(ctx, dir, planFileName, planJSON); err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	ic := exec.CommandContext(ctx, binPath, "breakdown", "--path", planJSON, "--format", "json", "--no-color")
	ic.Dir = dir
	ic.Stdout = &stdout
	ic.Stderr = &stderr
	if err := ic.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("infracost: %s", firstLine(msg))
	}
	return parseInfracost(stdout.Bytes())
}

// infracostOutput is the subset of `infracost breakdown --format json` we use.
type infracostOutput struct {
	Projects []struct {
		Breakdown struct {
			Resources []struct {
				Name        string `json:"name"`
				MonthlyCost string `json:"monthlyCost"`
			} `json:"resources"`
		} `json:"breakdown"`
	} `json:"projects"`
}

func parseInfracost(data []byte) (map[string]float64, error) {
	var out infracostOutput
	if err := json.Unmarshal(bytes.TrimSpace(data), &out); err != nil {
		return nil, fmt.Errorf("parse infracost output: %w", err)
	}
	prices := map[string]float64{}
	for _, p := range out.Projects {
		for _, r := range p.Breakdown.Resources {
			if r.MonthlyCost == "" {
				continue // Infracost couldn't price this resource
			}
			v, err := strconv.ParseFloat(r.MonthlyCost, 64)
			if err != nil {
				continue
			}
			prices[r.Name] = v
		}
	}
	return prices, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
