package git

import (
	"testing"
)

func TestBranchLifecycle(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "arcloom.yaml", "name: demo\n")

	// Branches on an uninitialized repo: none, no error.
	if bs, err := Branches(dir); err != nil || len(bs) != 0 {
		t.Fatalf("branches pre-init: %v %v", bs, err)
	}
	// Create a branch before any commit fails clearly.
	if err := CreateBranch(dir, "feature"); err == nil {
		t.Fatal("expected error creating a branch with no commits")
	}

	if _, err := CommitChanges(dir, "initial", "T", "t@e.com"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	base := branchTip(t, dir)

	// Create + switch to a new branch.
	if err := CreateBranch(dir, "feature"); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	if got := currentBranch(t, dir); got != "feature" {
		t.Fatalf("expected current branch feature, got %q", got)
	}
	// Duplicate create is rejected.
	if err := CreateBranch(dir, "feature"); err == nil {
		t.Fatal("expected duplicate branch error")
	}
	// Invalid names rejected.
	if err := CreateBranch(dir, "bad name"); err == nil {
		t.Fatal("expected invalid branch name error")
	}

	// Commit on feature so it advances past base.
	write(t, dir, "arcloom.yaml", "name: demo\nchanged: true\n")
	if _, err := CommitChanges(dir, "feature work", "T", "t@e.com"); err != nil {
		t.Fatalf("commit on feature: %v", err)
	}
	featTip := branchTip(t, dir)

	// Listing shows both, feature current.
	bs, err := Branches(dir)
	if err != nil {
		t.Fatalf("branches: %v", err)
	}
	names := map[string]bool{}
	var currentName string
	for _, b := range bs {
		names[b.Name] = true
		if b.Current {
			currentName = b.Name
		}
	}
	if !names[baseBranch(t, dir)] || !names["feature"] || currentName != "feature" {
		t.Fatalf("unexpected branch list %+v (current %q)", bs, currentName)
	}

	// Switch back to the base branch.
	main := baseBranch(t, dir)
	if err := SwitchBranch(dir, main); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if currentBranch(t, dir) != main {
		t.Fatalf("expected %q after switch", main)
	}
	// Working tree reverted to base content.
	if tip := branchTip(t, dir); tip != base {
		t.Fatalf("expected tip %s after switch, got %s", base, tip)
	}

	// Fast-forward merge feature into base.
	if err := MergeBranch(dir, "feature"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if tip := branchTip(t, dir); tip != featTip {
		t.Fatalf("expected fast-forward to %s, got %s", featTip, tip)
	}

	// Delete feature (now merged); current branch cannot be deleted.
	if err := DeleteBranch(dir, main); err == nil {
		t.Fatal("expected error deleting current branch")
	}
	if err := DeleteBranch(dir, "feature"); err != nil {
		t.Fatalf("delete feature: %v", err)
	}
	if bs, _ := Branches(dir); len(bs) != 1 {
		t.Fatalf("expected 1 branch after delete, got %+v", bs)
	}
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	st, err := GetStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st.Branch
}

// baseBranch returns the name of the initial branch (master or main depending
// on the go-git default) — the one that is not "feature".
func baseBranch(t *testing.T, dir string) string {
	t.Helper()
	bs, err := Branches(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.Name != "feature" {
			return b.Name
		}
	}
	t.Fatal("no base branch found")
	return ""
}

func branchTip(t *testing.T, dir string) string {
	t.Helper()
	cs, err := Log(dir, 1)
	if err != nil || len(cs) == 0 {
		t.Fatalf("log: %v", err)
	}
	return cs[0].Hash
}
