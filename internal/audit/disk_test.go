package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisk_AppendListFilter(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := []*Entry{
		{ID: "1", Time: time.Unix(1, 0), User: "alice", Action: "resource.upsert", ProjectID: "p1"},
		{ID: "2", Time: time.Unix(2, 0), User: "bob", Action: "project.delete", ProjectID: "p2"},
		{ID: "3", Time: time.Unix(3, 0), User: "alice", Action: "git.push", ProjectID: "p1"},
	}
	for _, e := range entries {
		if err := d.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	// Newest first.
	all, err := d.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != "3" || all[2].ID != "1" {
		t.Fatalf("wrong order/count: %+v", all)
	}

	// Filter by project.
	p1, _ := d.List(Filter{ProjectID: "p1"})
	if len(p1) != 2 {
		t.Fatalf("project filter: want 2, got %d", len(p1))
	}
	// Filter by user + limit.
	byUser, _ := d.List(Filter{User: "alice", Limit: 1})
	if len(byUser) != 1 || byUser[0].ID != "3" {
		t.Fatalf("user+limit filter: %+v", byUser)
	}

	// Persists across reopen.
	d2, err := NewDisk(t.TempDir())
	_ = d2
	if err != nil {
		t.Fatal(err)
	}
}

func TestDisk_DeleteProject(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Append(&Entry{ID: "1", Time: time.Unix(1, 0), Action: "x", ProjectID: "keep"})
	_ = d.Append(&Entry{ID: "2", Time: time.Unix(2, 0), Action: "y", ProjectID: "drop"})
	if err := d.DeleteProject("drop"); err != nil {
		t.Fatal(err)
	}
	all, _ := d.List(Filter{})
	if len(all) != 1 || all[0].ProjectID != "keep" {
		t.Fatalf("delete-project left: %+v", all)
	}
}

func TestDisk_Compaction(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxEntries + slack + 10 {
		if err := d.Append(&Entry{ID: fmt.Sprintf("%d", i), Time: time.Unix(int64(i), 0), Action: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := d.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// Compaction keeps growth bounded (to maxEntries+slack), trimming back to
	// maxEntries when the threshold is crossed.
	if len(all) > maxEntries+slack {
		t.Fatalf("compaction failed: %d entries retained (bound %d)", len(all), maxEntries+slack)
	}
	// Newest survives.
	if all[0].ID != fmt.Sprintf("%d", maxEntries+slack+9) {
		t.Fatalf("newest entry lost after compaction: %s", all[0].ID)
	}
}

// TestCompactArchivesRatherThanDeletes is the anti-forensics fix: an attacker
// holding any mutating permission can generate audited events at HTTP speed, so
// if compaction simply dropped the oldest entries they could push the record of
// their own actions off the end with a few thousand cheap requests.
func TestCompactArchivesRatherThanDeletes(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	total := maxEntries + slack + 10
	for i := range total {
		if err := d.Append(&Entry{
			ID: fmt.Sprint(i), Time: day, User: "attacker",
			Action: "resource.upsert", Detail: fmt.Sprintf("entry-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The live log is bounded...
	live, err := d.List(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// Bounded, but not exactly maxEntries: compaction runs on the slack
	// threshold, so the live count drifts up to maxEntries+slack between
	// rotations (d.n is documented as approximate).
	if len(live) > maxEntries+slack {
		t.Errorf("live log not bounded: %d entries", len(live))
	}
	// ...but entry 0, the first thing they did, must still exist on disk.
	archive := filepath.Join(dir, "audit-20260809.log")
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("no archive written: %v", err)
	}
	if !strings.Contains(string(body), `"entry-0"`) {
		t.Error("the oldest entry was dropped instead of archived")
	}
	// The archive holds exactly what left the live log.
	archived := strings.Count(strings.TrimSpace(string(body)), "\n") + 1
	if archived+len(live) != total {
		t.Errorf("entries lost: %d archived + %d live != %d written", archived, len(live), total)
	}
}

// The audit log is the security record; it must not be world-readable.
func TestAuditFilePermissions(t *testing.T) {
	// A fresh directory, because MkdirAll does not chmod one that already
	// exists — t.TempDir()'s is 0755, and silently re-permissioning a directory
	// an operator created is not this constructor's call to make.
	dir := filepath.Join(t.TempDir(), "audit")
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Append(&Entry{ID: "1", Time: time.Now(), Action: "x"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("audit.log mode = %o, want 600", fi.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("audit dir mode = %o, want 700", di.Mode().Perm())
	}
}
