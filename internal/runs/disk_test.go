package runs

import (
	"fmt"
	"testing"
	"time"
)

func TestDiskRoundtripAndPrune(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range keepPerProject + 5 {
		if err := d.Append(&Run{
			ID: fmt.Sprintf("r%03d", i), ProjectID: "p1", Op: "plan", Status: "success",
			StartedAt: base.Add(time.Duration(i) * time.Minute), Log: "line1\nline2",
		}); err != nil {
			t.Fatal(err)
		}
	}

	list, err := d.List("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != keepPerProject {
		t.Fatalf("List after prune = %d runs, want %d", len(list), keepPerProject)
	}
	if list[0].ID != fmt.Sprintf("r%03d", keepPerProject+4) {
		t.Errorf("List[0] = %s, want newest run", list[0].ID)
	}
	if list[0].Log != "" {
		t.Error("List must strip Log")
	}

	got, err := d.Get("p1", list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Log != "line1\nline2" {
		t.Errorf("Get Log = %q", got.Log)
	}
	if _, err := d.Get("p1", "r000"); err != ErrNotFound {
		t.Errorf("pruned run Get err = %v, want ErrNotFound", err)
	}

	if err := d.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.List("p1"); len(list) != 0 {
		t.Error("runs survived DeleteProject")
	}
}
