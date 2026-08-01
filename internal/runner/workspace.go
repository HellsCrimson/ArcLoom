package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// workspace is a directory containing generated HCL for one CLI operation.
// Ephemeral workspaces (schema/version/graph) are removed by close; persistent
// ones (plan/apply/destroy) live at a fixed per-project path so state, the
// dependency lock file and .terraform/ survive between operations.
//
// The CLI environment (TF_PLUGIN_CACHE_DIR, and TF_VAR_* for secrets) is set on
// the arcloom process itself rather than via tfexec.SetEnv, because tfexec's
// SetEnv rejects TF_VAR_* and replaces the whole environment. tfexec inherits
// os.Environ() when SetEnv is never called.
type workspace struct {
	dir        string
	execPath   string
	persistent bool
}

// backendConfigFile is the optional partial backend configuration the API
// writes into a workspace (`-backend-config` values: an environment's state
// key, and any backend setting bound to a secret — Terraform forbids variables
// inside a backend block, so a credential can only stay out of the generated
// HCL by arriving here). Its extension is not one Terraform auto-loads (only
// *.tf / *.tf.json / *.tfvars are), so it is used solely via `-backend-config`
// at init. It is NOT in persistKeep — it is rewritten from the generated files
// on every op, and removed when nothing supplies backend config any more.
const backendConfigFile = ".arcloom.tfbackend"

// fileMode is 0600 for the backend config file, which may hold a resolved
// secret (backend credentials), and 0644 for generated HCL.
func fileMode(name string) os.FileMode {
	if filepath.Base(name) == backendConfigFile {
		return 0o600
	}
	return 0o644
}

// backendInitOpts returns the -backend-config init option when the workspace has
// a per-env backend config file, else nil.
func (w *workspace) backendInitOpts() []tfexec.InitOption {
	if _, err := os.Stat(filepath.Join(w.dir, backendConfigFile)); err == nil {
		return []tfexec.InitOption{tfexec.BackendConfig(backendConfigFile)}
	}
	return nil
}

// persistKeep are workspace entries that carry engine state between runs and
// must survive a config re-sync. Everything else in a persistent workspace is
// owned by ArcLoom and rewritten from the generated files on every operation.
var persistKeep = map[string]bool{
	".terraform":                   true,
	".terraform.lock.hcl":          true,
	"terraform.tfstate":            true,
	"terraform.tfstate.backup":     true,
	".terraform.tfstate.lock.info": true,
	"errored.tfstate":              true, // written when apply cannot persist state
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
		if err := os.WriteFile(full, content, fileMode(name)); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	return &workspace{dir: dir, execPath: execPath}, nil
}

// newPersistentWorkspace syncs the generated files into the fixed directory
// dir, removing stale config left from earlier runs but keeping state and the
// provider install (persistKeep). close is a no-op for persistent workspaces.
func newPersistentWorkspace(dir, execPath string, files map[string][]byte) (*workspace, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read workspace: %w", err)
	}
	for _, e := range entries {
		if persistKeep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return nil, fmt.Errorf("clear stale %s: %w", e.Name(), err)
		}
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for %s: %w", name, err)
		}
		if err := os.WriteFile(full, content, fileMode(name)); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	return &workspace{dir: dir, execPath: execPath, persistent: true}, nil
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
	if w.dir != "" && !w.persistent {
		os.RemoveAll(w.dir)
	}
}
