package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

func TestUndoLastCommitKeepsFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "arcloom.yaml", "name: demo\n")
	if _, err := CommitChanges(dir, "initial", "T", "t@e.com", nil); err != nil {
		t.Fatal(err)
	}
	first := mustHead(t, dir)

	write(t, dir, "generated/main.tf", "resource \"x\" \"y\" {}\n")
	if _, err := CommitChanges(dir, "second — to be undone", "T", "t@e.com", nil); err != nil {
		t.Fatal(err)
	}

	undone, err := UndoLastCommit(dir, false)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !strings.Contains(undone.Message, "to be undone") {
		t.Errorf("returned the wrong commit: %+v", undone)
	}

	// History moved back to the first commit...
	commits, err := Log(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Hash != first {
		t.Fatalf("history should hold only the first commit, got %d: %+v", len(commits), commits)
	}
	// ...and the undone work is still on disk, now as an uncommitted change.
	if _, err := os.Stat(filepath.Join(dir, "generated/main.tf")); err != nil {
		t.Fatalf("undo must never delete working files: %v", err)
	}
	st, _ := GetStatus(dir)
	if st.Clean {
		t.Fatal("the undone content should read back as uncommitted changes")
	}
	if !hasPath(st.Files, "generated/main.tf") {
		t.Errorf("main.tf should be pending again, got %+v", st.Files)
	}

	// It is re-committable — the point of undoing.
	if _, err := CommitChanges(dir, "second, reworded", "T", "t@e.com", nil); err != nil {
		t.Fatalf("re-commit after undo: %v", err)
	}
	if commits, _ = Log(dir, 10); len(commits) != 2 || !strings.Contains(commits[0].Message, "reworded") {
		t.Fatalf("re-commit did not land: %+v", commits)
	}
}

func TestUndoRootCommit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "arcloom.yaml", "name: demo\n")
	write(t, dir, "generated/main.tf", "resource \"x\" \"y\" {}\n")
	if _, err := CommitChanges(dir, "initial", "T", "t@e.com", nil); err != nil {
		t.Fatal(err)
	}

	// `git reset HEAD~1` has no parent to land on here; undo must still work.
	if _, err := UndoLastCommit(dir, false); err != nil {
		t.Fatalf("undo root commit: %v", err)
	}

	st, err := GetStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.HasCommits {
		t.Error("the repo should be back to having no commits")
	}
	if !st.Initialized {
		t.Error("the repo itself must survive the undo")
	}
	if st.Clean {
		t.Fatal("the files should read back as pending changes")
	}
	for _, p := range []string{"arcloom.yaml", "generated/main.tf"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s must stay on disk: %v", p, err)
		}
		if !hasPath(st.Files, p) {
			t.Errorf("%s should be pending, got %+v", p, st.Files)
		}
	}
	if commits, _ := Log(dir, 10); len(commits) != 0 {
		t.Errorf("history should be empty: %+v", commits)
	}

	// Committing again works and produces a fresh root commit.
	if _, err := CommitChanges(dir, "initial, reworded", "T", "t@e.com", nil); err != nil {
		t.Fatalf("re-commit after root undo: %v", err)
	}
	commits, _ := Log(dir, 10)
	if len(commits) != 1 || !strings.Contains(commits[0].Message, "reworded") {
		t.Fatalf("expected one reworded root commit, got %+v", commits)
	}
}

func TestUndoRefusesPushedCommit(t *testing.T) {
	remote := t.TempDir()
	if _, err := gogit.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write(t, dir, "arcloom.yaml", "name: demo\n")
	if _, err := CommitChanges(dir, "initial", "T", "t@e.com", nil); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "generated/main.tf", "resource \"x\" \"y\" {}\n")
	if _, err := CommitChanges(dir, "published", "T", "t@e.com", nil); err != nil {
		t.Fatal(err)
	}
	if err := SetRemote(dir, remote); err != nil {
		t.Fatal(err)
	}
	if err := Push(dir, Auth{}); err != nil {
		t.Skipf("local push unsupported in this environment: %v", err)
	}

	// Undoing what origin already has would rewrite published history.
	if _, err := UndoLastCommit(dir, false); !errors.Is(err, ErrCommitPushed) {
		t.Fatalf("want ErrCommitPushed, got %v", err)
	}
	if commits, _ := Log(dir, 10); len(commits) != 2 {
		t.Fatalf("a refused undo must not touch history: %+v", commits)
	}

	// force is the explicit opt-in.
	if _, err := UndoLastCommit(dir, true); err != nil {
		t.Fatalf("forced undo: %v", err)
	}
	if commits, _ := Log(dir, 10); len(commits) != 1 {
		t.Fatalf("forced undo should drop the commit: %+v", commits)
	}
}

func TestUndoWithoutCommits(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureInit(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLastCommit(dir, false); err == nil {
		t.Fatal("undoing an empty repo should fail")
	}
}

func hasPath(files []FileStatus, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}
