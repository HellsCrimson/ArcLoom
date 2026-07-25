package runner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// These exercise the real CLI (init downloads the local provider), so they are
// opt-in: ARCLOOM_CLI_TESTS=1 go test ./internal/runner/. `make test` stays
// offline and fast.
func cliRunner(t *testing.T) (*TfExec, string) {
	t.Helper()
	if os.Getenv("ARCLOOM_CLI_TESTS") == "" {
		t.Skip("set ARCLOOM_CLI_TESTS=1 to run tests that shell out to the engine")
	}
	engine := model.EngineTofu
	if _, err := exec.LookPath(string(engine)); err != nil {
		t.Skipf("%s not on PATH", engine)
	}
	r, err := New(engine, "", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return r, t.TempDir()
}

// localFileConfig is a project whose single resource writes a file, so the test
// can change the world behind the engine's back.
func localFileConfig(path, content string) map[string][]byte {
	return map[string][]byte{"main.tf": []byte(`
terraform {
  required_providers { local = { source = "hashicorp/local" } }
}
resource "local_file" "f" {
  filename = "` + path + `"
  content  = "` + content + `"
}
`)}
}

// DriftPlan reports what a plan would do, not every attribute that moved in the
// recorded state: a difference in a value the configuration does not manage must
// not be reported, while a real out-of-band change must be — and both must leave
// the user's own saved plan alone.
func TestDriftPlan_ReportsOnlyActionableChanges(t *testing.T) {
	r, dir := cliRunner(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "out.txt")
	files := localFileConfig(target, "hello")

	if err := r.Apply(ctx, dir, files, nil, io.Discard); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Clean world: nothing to do.
	res, err := r.DriftPlan(ctx, dir, files, nil, io.Discard)
	if err != nil {
		t.Fatalf("drift plan: %v", err)
	}
	if res.HasChanges || len(res.Changes) != 0 {
		t.Fatalf("clean project reported drift: %+v", res)
	}

	// A value the config does not manage differs in state (as a provider that
	// recomputes a timestamp/hash on every read would produce). Still nothing a
	// plan would act on.
	statePath := filepath.Join(dir, "terraform.tfstate")
	var st map[string]any
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	inst := st["resources"].([]any)[0].(map[string]any)["instances"].([]any)[0].(map[string]any)
	inst["attributes"].(map[string]any)["content_md5"] = "0000000000000000000000000000dead"
	if raw, err = json.Marshal(st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, err = r.DriftPlan(ctx, dir, files, nil, io.Discard); err != nil {
		t.Fatalf("drift plan: %v", err)
	}
	if res.HasChanges || len(res.Changes) != 0 {
		t.Fatalf("an unmanaged value must not be reported as drift: %+v", res.Changes)
	}

	// A real out-of-band change to a managed value IS reported, and shows up in
	// the refresh's drift set too (so the caller can tell it apart from an
	// unapplied config edit).
	if err := os.WriteFile(target, []byte("changed-out-of-band"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err = r.DriftPlan(ctx, dir, files, nil, io.Discard); err != nil {
		t.Fatalf("drift plan: %v", err)
	}
	if !res.HasChanges || len(res.Changes) == 0 {
		t.Fatalf("out-of-band change not reported: %+v", res)
	}
	if len(res.Drift) == 0 {
		t.Fatalf("expected the refresh to also report it as changed outside: %+v", res)
	}

	// The check leaves no artifact behind, and — crucially — never leaves one
	// under the name the approval gate snapshots: a background plan must never
	// become the plan somebody approves and applies. (The workspace sync that
	// precedes every operation removes a pending tfplan regardless; that is
	// pre-existing and applies to any concurrent op, not just this one.)
	if _, err := os.Stat(filepath.Join(dir, driftPlanFileName)); !os.IsNotExist(err) {
		t.Errorf("drift plan artifact left in the workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, planFileName)); !os.IsNotExist(err) {
		t.Errorf("drift check left an approvable %s behind: %v", planFileName, err)
	}
}
