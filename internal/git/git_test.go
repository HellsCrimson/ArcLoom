package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitLifecycle(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "arcloom.yaml", "name: demo\n")
	write(t, dir, "generated/main.tf", "resource \"x\" \"y\" {}\n")

	// Status before init.
	st, err := GetStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Initialized {
		t.Fatal("should not be initialized yet")
	}

	// First commit initializes + records.
	h1, err := CommitChanges(dir, "initial", "Tester", "t@example.com")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(h1) < 7 {
		t.Fatalf("bad hash %q", h1)
	}

	// .gitignore was created and excludes state.
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), "*.tfstate") {
		t.Error(".gitignore missing state exclusion")
	}

	st, _ = GetStatus(dir)
	if !st.Initialized || !st.HasCommits || !st.Clean {
		t.Errorf("post-commit status: %+v", st)
	}

	// Change a file -> working diff shows it, status dirty.
	write(t, dir, "generated/main.tf", "resource \"x\" \"y\" {\n  a = 1\n}\n")
	st, _ = GetStatus(dir)
	if st.Clean {
		t.Error("status should be dirty after edit")
	}
	diff, err := WorkingDiff(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "a = 1") || !strings.Contains(diff, "generated/main.tf") {
		t.Errorf("working diff wrong:\n%s", diff)
	}

	// Second commit.
	h2, err := CommitChanges(dir, "add a", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Log has 2 commits, newest first.
	commits, err := Log(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if commits[0].Message != "add a" || commits[1].Message != "initial" {
		t.Errorf("log order wrong: %+v", commits)
	}

	// Commit diff of h2 shows the change.
	cd, err := CommitDiff(dir, h2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd, "a = 1") {
		t.Errorf("commit diff wrong:\n%s", cd)
	}

	// Restore to h1 -> file reverts to original content on disk.
	if err := Restore(dir, h1); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "generated/main.tf"))
	if strings.Contains(string(got), "a = 1") {
		t.Errorf("restore did not revert file: %s", got)
	}
}
