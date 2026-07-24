package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

const denyDoc = `{"enabled":true,"enforce":true,"docs":[{"name":"deny","rego":"package main\ndeny[msg] { false; msg := \"\" }"}]}`

func policyServer(t *testing.T, fake *fakeRunner) http.Handler {
	t.Helper()
	srv, _ := driftTestServer(t, fake) // auth off, project "p1"
	return srv.Handler()
}

func post(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// An enforcing policy that denies blocks the apply: the engine is never asked to
// apply, and the check ran against the plan the apply would have used.
func TestPolicyGateBlocksApply(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("FRESH-PLAN")}
	fake.policyRes = runner.PolicyResult{
		Available: true, Checked: true, FailureCount: 1,
		Violations: []runner.PolicyViolation{{Severity: "failure", Message: "no public buckets"}},
	}
	h := policyServer(t, fake)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/policy", denyDoc); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := post(t, h, http.MethodPost, "/api/projects/p1/apply", "")
	if rec.Code != http.StatusOK { // SSE: failures arrive as an "error" event
		t.Fatalf("apply = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "policy check failed") {
		t.Fatalf("expected a policy failure in the stream, got:\n%s", body)
	}
	if !strings.Contains(body, "no public buckets") {
		t.Fatalf("expected the violation message in the log, got:\n%s", body)
	}
	if fake.applyCalled || fake.applyPlanCalled {
		t.Fatalf("apply must not run when policy denies (apply=%v applyPlan=%v)", fake.applyCalled, fake.applyPlanCalled)
	}
	if string(fake.policyPlanBytes) != "FRESH-PLAN" {
		t.Fatalf("policy checked %q, want the plan the apply would run", fake.policyPlanBytes)
	}
}

// A passing enforcing policy lets the apply through — and it applies exactly the
// plan that was checked (ApplyPlan, not a fresh re-plan).
func TestPolicyGateAppliesCheckedPlan(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("FRESH-PLAN")}
	fake.policyRes = runner.PolicyResult{Available: true, Checked: true, Successes: 3}
	h := policyServer(t, fake)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/policy", denyDoc); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := post(t, h, http.MethodPost, "/api/projects/p1/apply", "")
	if body := rec.Body.String(); !strings.Contains(body, `event: done`) {
		t.Fatalf("apply should succeed, got:\n%s", body)
	}
	if !fake.applyPlanCalled || fake.applyCalled {
		t.Fatalf("expected ApplyPlan of the checked plan (applyPlan=%v apply=%v)", fake.applyPlanCalled, fake.applyCalled)
	}
	if string(fake.applyPlanBytes) != "FRESH-PLAN" {
		t.Fatalf("applied %q, want the checked plan", fake.applyPlanBytes)
	}
}

// Enforcement fails closed: no conftest on the server means no apply.
func TestPolicyGateFailsClosedWithoutConftest(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("FRESH-PLAN")}
	fake.policyRes = runner.PolicyResult{Available: false}
	h := policyServer(t, fake)

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/policy", denyDoc); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := post(t, h, http.MethodPost, "/api/projects/p1/apply", "")
	if body := rec.Body.String(); !strings.Contains(body, "conftest is not installed") {
		t.Fatalf("expected a fail-closed error, got:\n%s", body)
	}
	if fake.applyCalled || fake.applyPlanCalled {
		t.Fatal("apply must not run when the policy engine is missing")
	}
}

// Advisory mode (enabled, not enforcing) never touches the apply path; the plan
// still carries the annotation.
func TestPolicyAdvisoryDoesNotGateApply(t *testing.T) {
	fake := &fakeRunner{planFile: []byte("FRESH-PLAN")}
	fake.policyRes = runner.PolicyResult{Available: true, Checked: true, FailureCount: 2}
	h := policyServer(t, fake)

	advisory := `{"enabled":true,"enforce":false,"docs":[{"name":"deny","rego":"package main"}]}`
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/policy", advisory); rec.Code != http.StatusOK {
		t.Fatalf("set policy = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := post(t, h, http.MethodPost, "/api/projects/p1/apply", ""); !strings.Contains(rec.Body.String(), "event: done") {
		t.Fatalf("advisory policy must not block apply:\n%s", rec.Body.String())
	}
	if !fake.applyCalled {
		t.Fatal("expected the normal Apply path")
	}

	// A plan reports the violations without blocking anything.
	fake.policyCalled = false
	rec := post(t, h, http.MethodGet, "/api/projects/p1/plan", "")
	if !fake.policyCalled {
		t.Fatal("plan should evaluate policies when enabled")
	}
	if body := rec.Body.String(); !strings.Contains(body, `"policy"`) {
		t.Fatalf("plan result should carry the policy annotation:\n%s", body)
	}
}

// Policy document names become files in a throwaway directory, so they are
// validated; empty and duplicate documents are rejected.
func TestSetPolicyValidatesDocs(t *testing.T) {
	h := policyServer(t, &fakeRunner{})

	bad := []string{
		`{"docs":[{"name":"../escape","rego":"package main"}]}`,
		`{"docs":[{"name":"a/b","rego":"package main"}]}`,
		`{"docs":[{"name":"dup","rego":"package main"},{"name":"dup.rego","rego":"package main"}]}`,
		`{"docs":[{"name":"empty","rego":"   "}]}`,
	}
	for _, body := range bad {
		if rec := post(t, h, http.MethodPut, "/api/projects/p1/policy", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d, want 400", body, rec.Code)
		}
	}
	// A valid document is stored with a .rego suffix added.
	rec := post(t, h, http.MethodPut, "/api/projects/p1/policy", `{"enabled":true,"docs":[{"name":"cost","rego":"package main"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cost.rego"`) {
		t.Fatalf("expected the normalized name, got %s", rec.Body.String())
	}
}

func TestPolicyBlocks(t *testing.T) {
	fail := &runner.PolicyResult{FailureCount: 1}
	warn := &runner.PolicyResult{WarningCount: 1}
	clean := &runner.PolicyResult{Successes: 4}
	cases := []struct {
		name string
		res  *runner.PolicyResult
		cfg  settings.Policy
		want bool
	}{
		{"failure blocks", fail, settings.Policy{}, true},
		{"warning is advisory by default", warn, settings.Policy{}, false},
		{"warning blocks when promoted", warn, settings.Policy{FailOnWarn: true}, true},
		{"clean passes", clean, settings.Policy{FailOnWarn: true}, false},
		{"no result passes", nil, settings.Policy{}, false},
	}
	for _, c := range cases {
		if got := policyBlocks(c.res, c.cfg); got != c.want {
			t.Errorf("%s: policyBlocks = %v, want %v", c.name, got, c.want)
		}
	}
}
