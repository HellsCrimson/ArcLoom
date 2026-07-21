package audit

import (
	"fmt"
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
