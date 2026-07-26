package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// The plan → approve → apply gate: with approval required, an apply without an
// approval is refused (409); after a plan is approved, apply runs that exact
// saved plan (ApplyPlan) and consumes the approval.
func TestApprovalGate(t *testing.T) {
	fake := &fakeRunner{}
	srv, _ := driftTestServer(t, fake) // auth off, project "p1", real approvals store
	h := srv.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	// Gate off: apply uses the normal re-plan Apply path.
	if rec := do(http.MethodPost, "/api/projects/p1/apply", ""); rec.Code != http.StatusOK {
		t.Fatalf("ungated apply = %d (%s)", rec.Code, rec.Body.String())
	}
	if !fake.applyCalled || fake.applyPlanCalled {
		t.Fatalf("ungated apply should call Apply, not ApplyPlan (apply=%v applyPlan=%v)", fake.applyCalled, fake.applyPlanCalled)
	}

	// Enable the gate.
	if rec := do(http.MethodPut, "/api/projects/p1/approval", `{"required":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable gate = %d (%s)", rec.Code, rec.Body.String())
	}

	// Apply with no approval is refused (409), before any run record.
	if rec := do(http.MethodPost, "/api/projects/p1/apply", ""); rec.Code != http.StatusConflict {
		t.Fatalf("gated apply w/o approval = %d, want 409", rec.Code)
	}

	// Approving with no saved plan is a 409.
	if rec := do(http.MethodPost, "/api/projects/p1/approve", ""); rec.Code != http.StatusConflict {
		t.Fatalf("approve w/o plan = %d, want 409", rec.Code)
	}

	// Run a plan (the runner saves its artifact in the workspace), then approve it.
	fake.planFile = []byte("APPROVED-PLAN")
	if rec := do(http.MethodGet, "/api/projects/p1/plan", ""); rec.Code != http.StatusOK {
		t.Fatalf("plan = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/projects/p1/approve", `{"add":2}`); rec.Code != http.StatusOK {
		t.Fatalf("approve = %d (%s)", rec.Code, rec.Body.String())
	}

	// Now a gated apply runs the exact approved plan and consumes it.
	fake.applyCalled, fake.applyPlanCalled, fake.applyPlanBytes = false, false, nil
	if rec := do(http.MethodPost, "/api/projects/p1/apply", ""); rec.Code != http.StatusOK {
		t.Fatalf("gated apply w/ approval = %d (%s)", rec.Code, rec.Body.String())
	}
	if !fake.applyPlanCalled || fake.applyCalled {
		t.Fatalf("gated apply should call ApplyPlan (applyPlan=%v apply=%v)", fake.applyPlanCalled, fake.applyCalled)
	}
	if string(fake.applyPlanBytes) != "APPROVED-PLAN" {
		t.Fatalf("applied plan bytes = %q, want the approved artifact", fake.applyPlanBytes)
	}
	// The approval was single-use: a second apply is refused again, and the plan
	// it snapshotted can't simply be re-approved either.
	if rec := do(http.MethodPost, "/api/projects/p1/apply", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second gated apply = %d, want 409 (approval consumed)", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/projects/p1/approve", ""); rec.Code != http.StatusConflict {
		t.Fatalf("re-approve of a consumed plan = %d, want 409", rec.Code)
	}
}

// B8: a pending plan used to live only in the CLI workspace, which every
// subsequent operation wipes (tfplan is not in the runner's persistKeep) — so a
// scheduled drift check, or anyone else's op, between a user's plan and their
// Approve click made the approval fail with "no plan to approve". The plan is
// stashed outside the workspace, so approving still works and applies the exact
// bytes that were planned.
func TestApprovalSurvivesWorkspaceWipe(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("SURVIVING-PLAN")}
	srv, _ := driftTestServer(t, fake)
	h := srv.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	if rec := do(http.MethodPut, "/api/projects/p1/approval", `{"required":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable gate = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/projects/p1/plan", ""); rec.Code != http.StatusOK {
		t.Fatalf("plan = %d (%s)", rec.Code, rec.Body.String())
	}

	// What an intervening operation does: prepare's workspace sync drops tfplan.
	if err := os.Remove(filepath.Join(srv.runDir("p1", ""), planFileName)); err != nil {
		t.Fatalf("expected a saved plan in the workspace to remove: %v", err)
	}

	if rec := do(http.MethodPost, "/api/projects/p1/approve", `{"add":1}`); rec.Code != http.StatusOK {
		t.Fatalf("approve after workspace wipe = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/projects/p1/apply", ""); rec.Code != http.StatusOK {
		t.Fatalf("gated apply = %d (%s)", rec.Code, rec.Body.String())
	}
	if !fake.applyPlanCalled || string(fake.applyPlanBytes) != "SURVIVING-PLAN" {
		t.Fatalf("applied plan bytes = %q (applyPlan=%v)", fake.applyPlanBytes, fake.applyPlanCalled)
	}
}

// The approve permission exists so that the person approving need not be the
// person who planned: the pending plan is reported to any project reader, and
// its summary is recorded from the plan itself rather than from the body of
// whoever clicks Approve (who may never have run it).
func TestApprovalSummaryComesFromThePlan(t *testing.T) {
	fake := &fakeRunner{
		planFile: []byte("SUMMARISED-PLAN"),
		planRes:  runner.PlanResult{HasChanges: true, Add: 3, Change: 2, Destroy: 1, Replace: 4},
	}
	srv, _ := driftTestServer(t, fake)
	h := srv.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	// Nothing pending before the first plan.
	if body := do(http.MethodGet, "/api/projects/p1/approval", "").Body.String(); !strings.Contains(body, `"planPending":null`) {
		t.Fatalf("approval state before planning = %s", body)
	}
	if rec := do(http.MethodGet, "/api/projects/p1/plan", ""); rec.Code != http.StatusOK {
		t.Fatalf("plan = %d (%s)", rec.Code, rec.Body.String())
	}
	body := do(http.MethodGet, "/api/projects/p1/approval", "").Body.String()
	if !strings.Contains(body, `"add":3`) || !strings.Contains(body, `"replace":4`) {
		t.Fatalf("pending plan not reported: %s", body)
	}

	// Approve with an empty body — the counts must still be the plan's.
	rec := do(http.MethodPost, "/api/projects/p1/approve", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("approve = %d (%s)", rec.Code, rec.Body.String())
	}
	a, ok := srv.approvals.Latest("p1", "")
	if !ok {
		t.Fatal("no approval recorded")
	}
	if a.Add != 3 || a.Change != 2 || a.Destroy != 1 || a.Replace != 4 {
		t.Fatalf("approval summary = %+v, want the plan's counts", a)
	}
	// Approving consumed the pending plan.
	if body := do(http.MethodGet, "/api/projects/p1/approval", "").Body.String(); !strings.Contains(body, `"planPending":null`) {
		t.Fatalf("pending plan still advertised after approval: %s", body)
	}
}

// A stashed plan outlives the workspace, so it can outlive the configuration
// too: an edit between plan and approve must not be signed off unreviewed. The
// plan stops being offered and approving it is refused — but the stash is kept,
// so undoing the edit makes it current again.
func TestStashedPlanIsInvalidatedByAModelEdit(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("PRE-EDIT-PLAN")}
	srv, _ := driftTestServer(t, fake)
	h := srv.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	if rec := do(http.MethodGet, "/api/projects/p1/plan", ""); rec.Code != http.StatusOK {
		t.Fatalf("plan = %d (%s)", rec.Code, rec.Body.String())
	}

	// Edit the model (a full-model replace, the undo path).
	edited := `{"locals":[{"name":"answer","value":{"mode":"expression","expr":"42"}}]}`
	if rec := do(http.MethodPut, "/api/projects/p1", edited); rec.Code != http.StatusOK {
		t.Fatalf("edit = %d (%s)", rec.Code, rec.Body.String())
	}
	if body := do(http.MethodGet, "/api/projects/p1/approval", "").Body.String(); !strings.Contains(body, `"planPending":null`) {
		t.Fatalf("stale plan still offered: %s", body)
	}
	rec := do(http.MethodPost, "/api/projects/p1/approve", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "configuration changed") {
		t.Fatalf("approve after edit = %d (%s), want 409", rec.Code, rec.Body.String())
	}

	// Undo the edit: the plan matches the model again and is approvable.
	if rec := do(http.MethodPut, "/api/projects/p1", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("undo = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/projects/p1/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve after undo = %d (%s)", rec.Code, rec.Body.String())
	}
}

// Each environment keeps its own pending plan: planning in one must not make the
// other's plan approvable (or, worse, approvable with the wrong artifact).
func TestStashedPlanIsPerEnvironment(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("DEFAULT-PLAN")}
	srv, _ := driftTestServer(t, fake)
	if err := srv.settings.SetEnvironments("p1", []settings.Environment{{Name: "prod"}}); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	if rec := do(http.MethodGet, "/api/projects/p1/plan", ""); rec.Code != http.StatusOK {
		t.Fatalf("plan = %d (%s)", rec.Code, rec.Body.String())
	}
	// prod has never been planned.
	if rec := do(http.MethodPost, "/api/projects/p1/approve?env=prod", ""); rec.Code != http.StatusConflict {
		t.Fatalf("approve prod without a plan = %d, want 409", rec.Code)
	}

	fake.planFile = []byte("PROD-PLAN")
	if rec := do(http.MethodGet, "/api/projects/p1/plan?env=prod", ""); rec.Code != http.StatusOK {
		t.Fatalf("plan prod = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/projects/p1/approve?env=prod", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve prod = %d (%s)", rec.Code, rec.Body.String())
	}
	// The default environment still holds its own, older plan.
	if rec := do(http.MethodPost, "/api/projects/p1/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve default = %d (%s)", rec.Code, rec.Body.String())
	}
	a, ok := srv.approvals.Latest("p1", "")
	if !ok {
		t.Fatal("no approval for the default environment")
	}
	if pb, err := srv.approvals.Plan("p1", a.ID); err != nil || string(pb) != "DEFAULT-PLAN" {
		t.Fatalf("default approval plan = %q err=%v", pb, err)
	}
}

// The approve endpoint requires the approve permission.
func TestApprovePermissionGated(t *testing.T) {
	h, _, _ := newRBACServer(t)
	editor := login(t, h, "editor", "password1") // resources only, no approve
	// editor lacks approve → 403 (before any project/plan check).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/approve", nil)
	req.AddCookie(editor)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("editor approve = %d, want 403", rec.Code)
	}
}
