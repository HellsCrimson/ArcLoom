package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// workspace is a temporary directory containing generated HCL for one CLI
// operation. It is removed by close.
//
// The CLI environment (TF_PLUGIN_CACHE_DIR, and TF_VAR_* for secrets) is set on
// the arcloom process itself rather than via tfexec.SetEnv, because tfexec's
// SetEnv rejects TF_VAR_* and replaces the whole environment. tfexec inherits
// os.Environ() when SetEnv is never called.
type workspace struct {
	dir      string
	execPath string
}

// newWorkspace creates a fresh temp directory under base and writes the given
// files into it. files maps filename -> contents.
func newWorkspace(base, execPath string, files map[string][]byte) (*workspace, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create work base: %w", err)
	}
	dir, err := os.MkdirTemp(base, "ws-")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("mkdir for %s: %w", name, err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	return &workspace{dir: dir, execPath: execPath}, nil
}

// terraform returns a tfexec handle that inherits the process environment.
func (w *workspace) terraform() (*tfexec.Terraform, error) {
	tf, err := tfexec.NewTerraform(w.dir, w.execPath)
	if err != nil {
		return nil, fmt.Errorf("init tfexec: %w", err)
	}
	return tf, nil
}

func (w *workspace) close() {
	if w.dir != "" {
		os.RemoveAll(w.dir)
	}
}
