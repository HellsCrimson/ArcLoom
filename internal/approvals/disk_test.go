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

// The stashed plan is per (project, env), replaced by the next plan and dropped
// once approved. It is what makes a plan outlive the CLI workspace it was saved
// in (B8).
func TestDisk_StashedPlanLifecycle(t *testing.T) {
	d, _ := NewDisk(t.TempDir())

	stash := func(env, by string, add int, plan string) {
		t.Helper()
		p := &PendingPlan{ProjectID: "p1", Env: env, RunID: "run-" + plan, Add: add, PlannedBy: by}
		if err := d.StashPlan(p, []byte(plan)); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := d.StashedPlan("p1", ""); err != ErrNotFound {
		t.Fatalf("no stash yet: err = %v, want ErrNotFound", err)
	}
	stash("", "alice", 3, "PLAN-1")
	p, pb, err := d.StashedPlan("p1", "")
	if err != nil || string(pb) != "PLAN-1" {
		t.Fatalf("stashed = %q err=%v", pb, err)
	}
	// The summary rides along, so an approver who did not plan still gets a
	// truthful record.
	if p.Add != 3 || p.PlannedBy != "alice" || p.RunID != "run-PLAN-1" {
		t.Fatalf("pending summary = %+v", p)
	}
	// A named environment has its own stash and does not see the default one.
	if _, _, err := d.StashedPlan("p1", "prod"); err != ErrNotFound {
		t.Fatalf("prod stash = %v, want ErrNotFound", err)
	}
	stash("prod", "bob", 1, "PROD-PLAN")
	// A newer plan replaces the previous stash for its own environment only.
	stash("", "alice", 7, "PLAN-2")
	if p, pb, _ := d.StashedPlan("p1", ""); string(pb) != "PLAN-2" || p.Add != 7 {
		t.Fatalf("stashed after replan = %q %+v", pb, p)
	}
	if _, pb, _ := d.StashedPlan("p1", "prod"); string(pb) != "PROD-PLAN" {
		t.Fatalf("prod stash clobbered: %q", pb)
	}
	// Dropping is idempotent and leaves the other environment alone.
	if err := d.DropStash("p1", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.DropStash("p1", ""); err != nil {
		t.Fatalf("second drop = %v, want nil", err)
	}
	if _, _, err := d.StashedPlan("p1", ""); err != ErrNotFound {
		t.Fatalf("after drop: err = %v, want ErrNotFound", err)
	}
	if _, pb, _ := d.StashedPlan("p1", "prod"); string(pb) != "PROD-PLAN" {
		t.Fatalf("prod stash lost on default drop: %q", pb)
	}
	// An environment literally named "default" gets its own slot, not the default
	// workspace's.
	stash("default", "carol", 2, "NAMED-DEFAULT")
	if _, _, err := d.StashedPlan("p1", ""); err != ErrNotFound {
		t.Fatalf("env %q leaked into the default workspace: err = %v", "default", err)
	}
	// Stashes live under the project folder, so a project delete takes them too.
	if err := d.DeleteProject("p1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.StashedPlan("p1", "prod"); err != ErrNotFound {
		t.Fatalf("after project delete: err = %v, want ErrNotFound", err)
	}
}

// A stashed plan must never be mistaken for an approval record.
func TestDisk_StashDoesNotAppearAsApproval(t *testing.T) {
	d, _ := NewDisk(t.TempDir())
	if err := d.StashPlan(&PendingPlan{ProjectID: "p1"}, []byte("PENDING")); err != nil {
		t.Fatal(err)
	}
	if a, ok := d.Latest("p1", ""); ok {
		t.Fatalf("stash surfaced as an approval: %+v", a)
	}
	if list, err := d.List("p1"); err != nil || len(list) != 0 {
		t.Fatalf("list = %+v err=%v", list, err)
	}
}

func approval(t *testing.T, project, env, by string) Approval {
	t.Helper()
	return Approval{ID: project + "-" + by + "-id", ProjectID: project, Env: env, ApprovedBy: by, ApprovedAt: time.Unix(1, 0)}
}
