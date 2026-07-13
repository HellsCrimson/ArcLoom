package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// workspace is a temporary directory containing generated HCL for one CLI
// operation. It is removed by close unless it is a persistent init cache.
type workspace struct {
	dir      string
	execPath string
	pluginCacheDir string
}

// newWorkspace creates a fresh temp directory under base and writes the given
// files into it. files maps filename -> contents.
func newWorkspace(base, execPath, pluginCacheDir string, files map[string][]byte) (*workspace, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create work base: %w", err)
	}
	dir, err := os.MkdirTemp(base, "ws-")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	return &workspace{dir: dir, execPath: execPath, pluginCacheDir: pluginCacheDir}, nil
}

// terraform returns a configured tfexec handle for this workspace.
func (w *workspace) terraform() (*tfexec.Terraform, error) {
	tf, err := tfexec.NewTerraform(w.dir, w.execPath)
	if err != nil {
		return nil, fmt.Errorf("init tfexec: %w", err)
	}
	if w.pluginCacheDir != "" {
		if err := os.MkdirAll(w.pluginCacheDir, 0o755); err == nil {
			tf.SetEnv(mergeEnv(map[string]string{
				"TF_PLUGIN_CACHE_DIR": w.pluginCacheDir,
			}))
		}
	}
	return tf, nil
}

func (w *workspace) close() {
	if w.dir != "" {
		os.RemoveAll(w.dir)
	}
}

// mergeEnv overlays the given vars on top of the current process environment,
// which tfexec requires (SetEnv replaces the whole environment).
func mergeEnv(extra map[string]string) map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}
