package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestShimScrubsArcloomEnv is the layer that must hold on every host, sandbox or
// not: ARCLOOM_SECRET_KEY decrypts every stored secret, and the engine process
// has no business seeing it. Nor should one project's run see another project's
// ARCLOOM_SECRET_<name> env secrets.
func TestShimScrubsArcloomEnv(t *testing.T) {
	t.Setenv("ARCLOOM_SECRET_KEY", "master-key-value")
	t.Setenv("ARCLOOM_SECRET_DBPASS", "another-project-secret")
	t.Setenv("ARCLOOM_VAULT_TOKEN", "vault-token")
	t.Setenv("AWS_PROFILE", "prod") // a provider credential: must SURVIVE

	dir := t.TempDir()
	shim := filepath.Join(dir, "engine")
	// A stand-in for the engine that just reports what it inherited.
	script := shimScript("/usr/bin/env", Sandbox{Mode: SandboxOff}, false)
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(shim).CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v (%s)", err, out)
	}
	got := string(out)
	for _, leaked := range []string{"master-key-value", "another-project-secret", "vault-token"} {
		if strings.Contains(got, leaked) {
			t.Errorf("child inherited %q; the scrub did not remove it\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "AWS_PROFILE=prod") {
		t.Error("the scrub removed a provider credential; only ARCLOOM_* should go")
	}
}

// TestShimPassesArguments guards the transparency the shim depends on: tfexec
// builds the whole argv, so anything the wrapper drops or re-splits changes the
// command the engine actually runs.
func TestShimPassesArguments(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "engine")
	if err := os.WriteFile(shim, []byte(shimScript("/bin/echo", Sandbox{Mode: SandboxOff}, false)), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(shim, "plan", "-no-color", "-var", "a b=c d").CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "plan -no-color -var a b=c d" {
		t.Errorf("arguments mangled: %q", got)
	}
}

// TestSandboxHidesArcloomState is the finding-2 containment claim, exercised
// against real bubblewrap: a command running as the engine must not be able to
// read the secret store, even though it runs as the same uid as the server.
func TestSandboxHidesArcloomState(t *testing.T) {
	if err := probeSandbox(); err != nil {
		t.Skipf("bubblewrap unusable here: %v", err)
	}
	root := t.TempDir()
	secretsDir := filepath.Join(root, "secrets")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{secretsDir, workDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secretFile := filepath.Join(secretsDir, "p1.enc")
	if err := os.WriteFile(secretFile, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}

	shim := filepath.Join(root, "engine")
	script := shimScript("/bin/sh", Sandbox{Mode: SandboxAuto, HideDirs: []string{secretsDir}}, true)
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	// Run FROM the work dir, as tfexec does (the shim binds $PWD).
	cmd := exec.Command(shim, "-c", "cat "+secretFile+" 2>&1; echo ---; ls "+secretsDir+" 2>&1")
	cmd.Dir = workDir
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "ciphertext") {
		t.Errorf("the sandbox did not hide the secret store:\n%s", out)
	}

	// ...while the run's own working directory stays writable, or no plan could
	// ever run inside the sandbox.
	cmd = exec.Command(shim, "-c", "echo written > ./probe && cat ./probe")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "written") {
		t.Fatalf("the run workspace must stay writable: %v (%s)", err, out)
	}
}

// TestUseSandboxRequireFailsLoudly: an operator who asked for containment must
// not silently get none.
func TestUseSandboxRequireFailsLoudly(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no bwrap on this PATH
	tf := &TfExec{engine: "tofu", execPath: "/usr/bin/tofu", enginePath: "/usr/bin/tofu"}
	if err := tf.UseSandbox(Sandbox{Mode: SandboxRequire}); err == nil {
		t.Fatal("require mode must fail when the sandbox cannot start")
	}
	// auto degrades instead.
	tf2 := &TfExec{engine: "tofu", execPath: "/usr/bin/tofu", enginePath: "/usr/bin/tofu"}
	if err := tf2.UseSandbox(Sandbox{Mode: SandboxAuto}); err != nil {
		t.Fatalf("auto must degrade, not fail: %v", err)
	}
	defer tf2.Close()
	if tf2.Sandboxed() {
		t.Error("auto reported a sandbox it could not start")
	}
	if tf2.ExecPath() != "/usr/bin/tofu" {
		t.Errorf("ExecPath must report the engine, not the shim: %s", tf2.ExecPath())
	}
	if tf2.execPath == tf2.enginePath {
		t.Error("child processes should still go through the shim for the env scrub")
	}
}
