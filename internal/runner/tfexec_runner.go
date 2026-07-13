package runner

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/matthiasrauline/arcloom/internal/hclgen"
	"github.com/matthiasrauline/arcloom/internal/model"
)

// TfExec is a Runner backed by terraform-exec. The same code path drives both
// OpenTofu and Terraform; only execPath differs.
type TfExec struct {
	engine         model.Engine
	execPath       string
	workBase       string
	pluginCacheDir string
}

// New resolves the CLI binary for the given engine and returns a runner. If
// execPath is empty it is looked up on PATH.
func New(engine model.Engine, execPath, workBase, pluginCacheDir string) (*TfExec, error) {
	if execPath == "" {
		p, err := exec.LookPath(string(engine))
		if err != nil {
			return nil, fmt.Errorf("%s binary not found on PATH: %w", engine, err)
		}
		execPath = p
	}
	return &TfExec{
		engine:         engine,
		execPath:       execPath,
		workBase:       workBase,
		pluginCacheDir: pluginCacheDir,
	}, nil
}

func (t *TfExec) Engine() model.Engine { return t.engine }
func (t *TfExec) ExecPath() string     { return t.execPath }

func (t *TfExec) Version(ctx context.Context) (string, error) {
	ws, err := newWorkspace(t.workBase, t.execPath, t.pluginCacheDir, nil)
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
	ws, err := newWorkspace(t.workBase, t.execPath, t.pluginCacheDir, map[string][]byte{
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
func (t *TfExec) Plan(ctx context.Context, hcl []byte, providers []model.ProviderInstance, out io.Writer) (PlanResult, error) {
	_ = providers
	ws, err := newWorkspace(t.workBase, t.execPath, t.pluginCacheDir, map[string][]byte{
		"main.tf": hcl,
	})
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
	if err := tf.Init(ctx); err != nil {
		return PlanResult{}, fmt.Errorf("init: %w", err)
	}
	hasChanges, err := tf.Plan(ctx)
	if err != nil {
		return PlanResult{}, fmt.Errorf("plan: %w", err)
	}
	return PlanResult{HasChanges: hasChanges}, nil
}
