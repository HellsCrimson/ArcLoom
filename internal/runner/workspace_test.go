package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// A persistent workspace must remove stale generated files on sync but keep
// everything that carries engine state between runs.
func TestPersistentWorkspaceSync(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")

	ws, err := newPersistentWorkspace(dir, "tf", map[string][]byte{
		"main.tf":            []byte("a"),
		"modules/db/main.tf": []byte("b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ws.close()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("close removed persistent workspace: %v", err)
	}

	// Simulate engine state left behind by a previous run.
	for _, f := range []string{"terraform.tfstate", "terraform.tfstate.backup", ".terraform.lock.hcl"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("state"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".terraform", "providers"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Re-sync with the db module gone and a new file set.
	if _, err := newPersistentWorkspace(dir, "tf", map[string][]byte{
		"main.tf": []byte("c"),
	}); err != nil {
		t.Fatal(err)
	}

	for _, keep := range []string{
		"terraform.tfstate", "terraform.tfstate.backup", ".terraform.lock.hcl",
		filepath.Join(".terraform", "providers"),
	} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("sync removed %s: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "modules")); !os.IsNotExist(err) {
		t.Error("stale modules/ survived sync")
	}
	got, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil || string(got) != "c" {
		t.Errorf("main.tf = %q, %v; want %q", got, err, "c")
	}
}
