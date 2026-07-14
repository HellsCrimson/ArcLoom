package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/matthiasrauline/arcloom/internal/hclgen"
	"github.com/matthiasrauline/arcloom/internal/model"
)

// planEnvMu serializes the process-env mutation used to inject TF_VAR_* secrets
// into a plan, since the environment is process-global.
var planEnvMu sync.Mutex

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

// Plan writes the full generated config (terraform + providers + resources) as
// a single main.tf. The providers argument is unused here because the config
// already declares them; it is retained for interface symmetry.
func (t *TfExec) Plan(ctx context.Context, files map[string][]byte, providers []model.ProviderInstance, tfVars map[string]string, out io.Writer) (PlanResult, error) {
	_ = providers
	ws, err := newWorkspace(t.workBase, t.execPath, files)
	if err != nil {
		return PlanResult{}, err
	}
	defer ws.close()

	tf, err := ws.terraform()
	if err != nil {
		return PlanResult{}, err
	}
	if out != nil {
		tf.SetStdout(out)
		tf.SetStderr(out)
	}

	// Inject secret values as TF_VAR_* into the process environment (tfexec
	// inherits os.Environ; its SetEnv rejects TF_VAR_*). Serialize because the
	// environment is process-global; values are removed immediately after.
	if len(tfVars) > 0 {
		planEnvMu.Lock()
		defer planEnvMu.Unlock()
		for name, val := range tfVars {
			key := "TF_VAR_" + name
			os.Setenv(key, val)
			defer os.Unsetenv(key)
		}
	}

	if err := tf.Init(ctx); err != nil {
		return PlanResult{}, fmt.Errorf("init: %w", err)
	}
	hasChanges, err := tf.Plan(ctx)
	if err != nil {
		return PlanResult{}, fmt.Errorf("plan: %w", err)
	}
	return PlanResult{HasChanges: hasChanges}, nil
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
