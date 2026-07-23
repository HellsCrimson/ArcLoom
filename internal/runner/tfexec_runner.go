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
	if err := tf.Init(ctx, ws.backendInitOpts()...); err != nil {
		return nil, cleanup, fmt.Errorf("init: %w", err)
	}
	return tf, cleanup, nil
}

// planFileName is the saved plan inside the persistent workspace. It is not in
// persistKeep, so the next workspace sync removes it.
const planFileName = "tfplan"

// Plan runs plan in the project's persistent workspace, so the diff is against
// the state left by the last apply (empty state on a fresh project). The plan
// is saved to a file and re-read as JSON to extract per-resource changes.
func (t *TfExec) Plan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (PlanResult, error) {
	return t.plan(ctx, dir, files, tfVars, out, false)
}

// RefreshPlan runs plan -refresh-only: it only compares the persisted state
// against the real infrastructure (drift), ignoring pending config changes.
// HasChanges then means "state no longer matches reality".
func (t *TfExec) RefreshPlan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer) (PlanResult, error) {
	return t.plan(ctx, dir, files, tfVars, out, true)
}

func (t *TfExec) plan(ctx context.Context, dir string, files map[string][]byte, tfVars map[string]string, out io.Writer, refreshOnly bool) (PlanResult, error) {
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return PlanResult{}, err
	}
	hasChanges, err := tf.Plan(ctx, tfexec.Out(planFileName), tfexec.RefreshOnly(refreshOnly))
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
	plan, err := tf.ShowPlanFile(ctx, planFileName)
	if err != nil || plan == nil {
		return res, nil
	}
	// In a refresh-only plan the interesting set is ResourceDrift (state vs
	// reality); ResourceChanges is all no-ops there.
	changes := plan.ResourceChanges
	if refreshOnly {
		changes = plan.ResourceDrift
	}
	for _, rc := range changes {
		if rc.Change == nil {
			continue
		}
		a := rc.Change.Actions
		var action string
		switch {
		case a.Replace():
			action = "replace"
		case a.Create():
			action = "create"
		case a.Update():
			action = "update"
		case a.Delete():
			action = "delete"
		case a.Read():
			action = "read"
		default:
			continue // no-op
		}
		res.Changes = append(res.Changes, PlanChange{
			Address:       rc.Address,
			ModuleAddress: rc.ModuleAddress,
			Mode:          string(rc.Mode),
			Type:          rc.Type,
			Name:          rc.Name,
			Action:        action,
		})
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
	f, err := os.Create(planJSON)
	if err != nil {
		return nil, err
	}
	defer os.Remove(planJSON)
	var showErr bytes.Buffer
	show := exec.CommandContext(ctx, t.execPath, "show", "-json", planFileName)
	show.Dir = dir
	show.Stdout = f
	show.Stderr = &showErr
	runErr := show.Run()
	f.Close()
	if runErr != nil {
		return nil, fmt.Errorf("read saved plan: %w: %s", runErr, strings.TrimSpace(showErr.String()))
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
