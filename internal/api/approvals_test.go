package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	// Stage a saved plan artifact where the runner would have left it, then approve.
	dir := srv.runDir("p1", "")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tfplan"), []byte("APPROVED-PLAN"), 0o644); err != nil {
		t.Fatal(err)
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
	// The approval was single-use: a second apply is refused again.
	if rec := do(http.MethodPost, "/api/projects/p1/apply", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second gated apply = %d, want 409 (approval consumed)", rec.Code)
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
