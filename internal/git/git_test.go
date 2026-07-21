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
	h1, err := CommitChanges(dir, "initial", "Tester", "t@example.com", nil)
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
	h2, err := CommitChanges(dir, "add a", "", "", nil)
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

func TestPerFileStaging(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "arcloom.yaml", "name: demo\n")
	if _, err := CommitChanges(dir, "initial", "T", "t@e.com", nil); err != nil {
		t.Fatal(err)
	}

	// Two new files; commit only one of them.
	write(t, dir, "generated/a.tf", "resource \"a\" \"a\" {}\n")
	write(t, dir, "generated/b.tf", "resource \"b\" \"b\" {}\n")
	if _, err := CommitChanges(dir, "add a only", "T", "t@e.com", []string{"generated/a.tf"}); err != nil {
		t.Fatalf("partial commit: %v", err)
	}

	// b.tf remains an uncommitted change.
	st, _ := GetStatus(dir)
	if st.Clean {
		t.Fatal("b.tf should still be an uncommitted change")
	}
	if len(st.Files) != 1 || st.Files[0].Path != "generated/b.tf" {
		t.Fatalf("expected only b.tf pending, got %+v", st.Files)
	}

	// The committed tree contains a.tf but not b.tf.
	cd, err := CommitDiff(dir, mustHead(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd, "generated/a.tf") || strings.Contains(cd, "generated/b.tf") {
		t.Errorf("commit should include a.tf only:\n%s", cd)
	}

	// Staging a deletion: remove a.tf and commit that path.
	if err := os.Remove(filepath.Join(dir, "generated/a.tf")); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitChanges(dir, "drop a", "T", "t@e.com", []string{"generated/a.tf"}); err != nil {
		t.Fatalf("commit deletion: %v", err)
	}
	cd, _ = CommitDiff(dir, mustHead(t, dir))
	if !strings.Contains(cd, "generated/a.tf") {
		t.Errorf("deletion commit should reference a.tf:\n%s", cd)
	}
}

func mustHead(t *testing.T, dir string) string {
	t.Helper()
	commits, err := Log(dir, 1)
	if err != nil || len(commits) == 0 {
		t.Fatalf("no head commit: %v", err)
	}
	return commits[0].Hash
}

func TestSSHURLDetection(t *testing.T) {
	cases := []struct {
		url  string
		ssh  bool
		user string
	}{
		{"https://github.com/you/repo.git", false, ""},
		{"http://example.com/r.git", false, ""},
		{"git@github.com:you/repo.git", true, "git"},
		{"ssh://git@example.com:22/you/repo.git", true, "git"},
		{"ssh://deploy@host/path.git", true, "deploy"},
	}
	for _, c := range cases {
		if got := isSSHURL(c.url); got != c.ssh {
			t.Errorf("isSSHURL(%q) = %v, want %v", c.url, got, c.ssh)
		}
		if c.ssh {
			if got := sshUser(c.url); got != c.user {
				t.Errorf("sshUser(%q) = %q, want %q", c.url, got, c.user)
			}
		}
	}
}

func TestSSHAuthRequiresKey(t *testing.T) {
	if _, err := sshAuth("git@github.com:you/repo.git", Auth{}); err == nil {
		t.Fatal("expected error when SSH remote has no key")
	}
}
