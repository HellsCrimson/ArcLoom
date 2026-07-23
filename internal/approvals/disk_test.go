package approvals

import (
	"testing"
	"time"
)

func TestDisk_ApprovalLifecycle(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// No approval yet.
	if _, ok := d.Latest("p1", ""); ok {
		t.Fatal("expected no approval initially")
	}
	av := approval(t, "p1", "", "alice")
	a := &av
	if err := d.Create(a, []byte("PLAN-BYTES")); err != nil {
		t.Fatal(err)
	}
	// Latest returns it; the stored plan bytes come back verbatim.
	got, ok := d.Latest("p1", "")
	if !ok || got.ID != a.ID || got.ApprovedBy != "alice" {
		t.Fatalf("latest = %+v ok=%v", got, ok)
	}
	if pb, err := d.Plan("p1", a.ID); err != nil || string(pb) != "PLAN-BYTES" {
		t.Fatalf("plan bytes = %q err=%v", pb, err)
	}
	// A different environment does not see it.
	if _, ok := d.Latest("p1", "prod"); ok {
		t.Fatal("env prod should have no approval")
	}
	// Consuming it removes it from Latest but keeps it in List.
	if err := d.Consume("p1", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Latest("p1", ""); ok {
		t.Fatal("consumed approval should not be Latest")
	}
	if list, _ := d.List("p1"); len(list) != 1 || !list[0].Consumed {
		t.Fatalf("list = %+v", list)
	}
	// Delete drops everything.
	if err := d.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.List("p1"); len(list) != 0 {
		t.Fatalf("after delete list = %+v", list)
	}
}

// Latest returns the newest unconsumed approval per env.
func TestDisk_LatestNewestUnconsumed(t *testing.T) {
	d, _ := NewDisk(t.TempDir())
	older := approval(t, "p", "", "u1")
	older.ApprovedAt = time.Unix(100, 0)
	newer := approval(t, "p", "", "u2")
	newer.ApprovedAt = time.Unix(200, 0)
	_ = d.Create(&older, []byte("a"))
	_ = d.Create(&newer, []byte("b"))
	got, ok := d.Latest("p", "")
	if !ok || got.ApprovedBy != "u2" {
		t.Fatalf("latest = %+v", got)
	}
}

func approval(t *testing.T, project, env, by string) Approval {
	t.Helper()
	return Approval{ID: project + "-" + by + "-id", ProjectID: project, Env: env, ApprovedBy: by, ApprovedAt: time.Unix(1, 0)}
}
