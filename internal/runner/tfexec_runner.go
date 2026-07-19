package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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

	if err := tf.Init(ctx); err != nil {
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
	tf, cleanup, err := t.prepare(ctx, dir, files, tfVars, out)
	defer cleanup()
	if err != nil {
		return PlanResult{}, err
	}
	hasChanges, err := tf.Plan(ctx, tfexec.Out(planFileName))
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
	for _, rc := range plan.ResourceChanges {
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
