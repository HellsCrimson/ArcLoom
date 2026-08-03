package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/approvals"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// fakeRunner implements runner.Runner for drift / approval / policy-gate tests;
// only DriftPlan, Plan, Apply(Plan) and Policy are meaningful.
type fakeRunner struct {
	res runner.PlanResult
	err error
	// applyPlanBytes records the plan artifact passed to ApplyPlan (approval gate).
	applyPlanBytes  []byte
	applyPlanCalled bool
	applyCalled     bool
	// applyErr, when set, makes Apply fail (used by the notification tests).
	applyErr   error
	planCalled bool
	// planFile, when set, is written as the saved plan by Plan (what the policy
	// gate reads back to apply exactly what it checked); planRes is what Plan
	// reports about it.
	planFile          []byte
	planRes           runner.PlanResult
	acceptDriftCalled bool
	// tainted / untainted record the address each op received.
	tainted   string
	untainted string
	// unlockedID records the lock ID force-unlock received; unlockErr makes it fail.
	unlockedID string
	unlockErr  error
	// State stubs: what StatePull reports and what StatePush received.
	statePull    string
	statePushed  []byte
	statePushErr error
	// providerSchemas is what ProvidersSchema returns; schemaCalls counts how
	// often it was asked (the state import must batch, not fetch per resource).
	providerSchemas *tfjson.ProviderSchemas
	schemaCalls     int
	// Policy-gate stubs: the result/error Policy returns and what it was given.
	policyRes       runner.PolicyResult
	policyErr       error
	policyCalled    bool
	policyPlanBytes []byte
}

func (f *fakeRunner) Engine() model.Engine { return model.EngineTofu }
func (f *fakeRunner) ExecPath() string     { return "fake" }
func (f *fakeRunner) Version(context.Context) (string, error) {
	return "0.0.0", nil
}
func (f *fakeRunner) ProvidersSchema(context.Context, []model.ProviderInstance) (*tfjson.ProviderSchemas, error) {
	f.schemaCalls++
	return f.providerSchemas, nil
}
func (f *fakeRunner) Plan(_ context.Context, dir string, _ map[string][]byte, _ runner.Vars, _ io.Writer) (runner.PlanResult, error) {
	f.planCalled = true
	// Mimic the real runner leaving a saved plan behind, which the policy gate
	// reads back to apply exactly what it checked.
	if dir != "" && f.planFile != nil {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(dir, planFileName), f.planFile, 0o644)
		}
	}
	return f.planRes, nil
}
func (f *fakeRunner) DriftPlan(context.Context, string, map[string][]byte, runner.Vars, io.Writer) (runner.PlanResult, error) {
	return f.res, f.err
}
func (f *fakeRunner) Apply(context.Context, string, map[string][]byte, runner.Vars, io.Writer) error {
	f.applyCalled = true
	return f.applyErr
}
func (f *fakeRunner) ApplyPlan(_ context.Context, _ string, _ map[string][]byte, _ runner.Vars, planBytes []byte, _ io.Writer) error {
	f.applyPlanCalled = true
	f.applyPlanBytes = planBytes
	return nil
}
func (f *fakeRunner) Destroy(context.Context, string, map[string][]byte, runner.Vars, io.Writer) error {
	return nil
}
func (f *fakeRunner) Outputs(context.Context, string) (map[string]tfexec.OutputMeta, error) {
	return nil, nil
}
func (f *fakeRunner) Import(context.Context, string, map[string][]byte, runner.Vars, string, string, io.Writer) error {
	return nil
}
func (f *fakeRunner) StateRm(context.Context, string, map[string][]byte, runner.Vars, string, io.Writer) error {
	return nil
}
func (f *fakeRunner) StateMv(context.Context, string, map[string][]byte, runner.Vars, string, string, bool, io.Writer) error {
	return nil
}
func (f *fakeRunner) Taint(_ context.Context, _ string, _ map[string][]byte, _ runner.Vars, address string, _ io.Writer) error {
	f.tainted = address
	return nil
}
func (f *fakeRunner) Untaint(_ context.Context, _ string, _ map[string][]byte, _ runner.Vars, address string, _ io.Writer) error {
	f.untainted = address
	return nil
}
func (f *fakeRunner) ForceUnlock(_ context.Context, _ string, _ map[string][]byte, lockID string, _ io.Writer) error {
	f.unlockedID = lockID
	return f.unlockErr
}
func (f *fakeRunner) InitBackend(context.Context, string, map[string][]byte, bool, io.Writer) error {
	return nil
}
func (f *fakeRunner) AcceptDrift(context.Context, string, map[string][]byte, runner.Vars, io.Writer) error {
	f.acceptDriftCalled = true
	return nil
}
func (f *fakeRunner) StatePull(context.Context, string) (string, error) { return f.statePull, nil }
func (f *fakeRunner) StatePush(_ context.Context, _ string, _ map[string][]byte, stateJSON []byte, _ io.Writer) error {
	f.statePushed = stateJSON
	return f.statePushErr
}
func (f *fakeRunner) Validate(context.Context, string, map[string][]byte, runner.Vars, io.Writer) (*tfjson.ValidateOutput, error) {
	return nil, nil
}
func (f *fakeRunner) Graph(context.Context, map[string][]byte) (string, error) { return "", nil }
func (f *fakeRunner) Lint(context.Context, map[string][]byte, io.Writer) (runner.LintResult, error) {
	return runner.LintResult{}, nil
}
func (f *fakeRunner) Infracost(context.Context, string) (map[string]float64, error) {
	return nil, nil
}
func (f *fakeRunner) Policy(_ context.Context, _ string, planBytes []byte, _ map[string][]byte, _ io.Writer) (runner.PolicyResult, error) {
	f.policyCalled = true
	f.policyPlanBytes = planBytes
	return f.policyRes, f.policyErr
}

func driftTestServer(t *testing.T, fake *fakeRunner) (*Server, runs.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	rh, err := runs.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir(), DefaultEngine: "tofu"}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	appr, err := approvals.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, map[model.Engine]runner.Runner{model.EngineTofu: fake}, st, nil, nil, nil,
		authn, session.NewMemory(), user.NewMemory(), testSettings(t), rh, nil, appr)
	return srv, rh
}

func TestDriftConfigEndpoints(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{})
	h := srv.Handler()

	put := func(project, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/"+project+"/drift", strings.NewReader(body)))
		return rec
	}

	if rec := put("p1", `{"enabled":true,"intervalMinutes":15}`); rec.Code != http.StatusOK {
		t.Fatalf("set drift = %d (body %s)", rec.Code, rec.Body.String())
	}
	if d := srv.settings.Drift("p1"); !d.Enabled || d.IntervalMinutes != 15 {
		t.Fatalf("drift not stored: %+v", d)
	}
	// The scheduler was nudged.
	select {
	case <-srv.driftKick:
	default:
		t.Fatal("enable did not kick the scheduler")
	}

	// Interval below the minimum is rejected; 0 defaults to hourly.
	if rec := put("p1", `{"enabled":true,"intervalMinutes":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("tiny interval = %d, want 400", rec.Code)
	}
	if rec := put("p1", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("default interval = %d", rec.Code)
	}
	if d := srv.settings.Drift("p1"); d.IntervalMinutes != 60 {
		t.Fatalf("interval default = %d, want 60", d.IntervalMinutes)
	}

	if rec := put("nope", `{"enabled":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404", rec.Code)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/drift", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"intervalMinutes":60`) {
		t.Fatalf("get drift = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestDriftCheckRecordsRunOnOutcomeChange(t *testing.T) {
	fake := &fakeRunner{res: runner.PlanResult{HasChanges: true, Change: 1,
		Changes: []runner.PlanChange{{Address: "null_resource.a", Action: "update"}}}}
	srv, rh := driftTestServer(t, fake)
	ctx := context.Background()

	// First drifted check records a run and the status.
	srv.runDriftCheck(ctx, "p1")
	list, _ := rh.List("p1")
	if len(list) != 1 || list[0].Op != "drift" || list[0].Status != "success" || list[0].Plan == nil {
		t.Fatalf("first check runs = %+v", list)
	}
	if st := srv.driftStatuses()["p1"]; !st.Drifted || st.Change != 1 {
		t.Fatalf("status = %+v", st)
	}

	// Same outcome again: no new run entry.
	srv.runDriftCheck(ctx, "p1")
	if list, _ = rh.List("p1"); len(list) != 1 {
		t.Fatalf("unchanged outcome recorded again: %d runs", len(list))
	}

	// Drift resolved: the transition is recorded.
	fake.res = runner.PlanResult{}
	srv.runDriftCheck(ctx, "p1")
	if list, _ = rh.List("p1"); len(list) != 2 {
		t.Fatalf("resolution not recorded: %d runs", len(list))
	}
	if st := srv.driftStatuses()["p1"]; st.Drifted {
		t.Fatalf("status still drifted: %+v", st)
	}

	// A deleted project drops its schedule and status on the next check.
	_ = srv.settings.SetDrift("gone", settings.Drift{Enabled: true, IntervalMinutes: 60})
	srv.runDriftCheck(ctx, "gone")
	if d := srv.settings.Drift("gone"); d.Enabled {
		t.Fatalf("orphaned schedule kept: %+v", d)
	}
}

// The status names the drifted resources (capped), so "75 to change" can be
// inspected without opening the run record.
func TestDriftStatusCarriesAddresses(t *testing.T) {
	changes := make([]runner.PlanChange, 0, driftAddressCap+5)
	for i := 0; i < driftAddressCap+5; i++ {
		changes = append(changes, runner.PlanChange{Address: fmt.Sprintf("null_resource.r%d", i), Action: "update"})
	}
	fake := &fakeRunner{res: runner.PlanResult{HasChanges: true, Change: len(changes), Changes: changes}}
	srv, _ := driftTestServer(t, fake)

	srv.runDriftCheck(context.Background(), "p1")
	st := srv.driftStatuses()["p1"]
	if len(st.Addresses) != driftAddressCap {
		t.Fatalf("addresses = %d, want the cap (%d)", len(st.Addresses), driftAddressCap)
	}
	if st.Addresses[0] != "null_resource.r0" || st.Change != driftAddressCap+5 {
		t.Fatalf("status = %+v", st)
	}
}

// Accepting drift persists the refreshed state (apply -refresh-only) and clears
// the status the previous check left behind — otherwise the badge would keep
// reporting drift that no longer exists.
func TestAcceptDriftAbsorbsAndClearsStatus(t *testing.T) {
	fake := &fakeRunner{res: runner.PlanResult{HasChanges: true, Change: 1,
		Changes: []runner.PlanChange{{Address: "null_resource.a", Action: "update"}}}}
	srv, rh := driftTestServer(t, fake)
	h := srv.Handler()

	srv.runDriftCheck(context.Background(), "p1")
	if st := srv.driftStatuses()["p1"]; !st.Drifted {
		t.Fatalf("expected a drifted status to start from: %+v", st)
	}

	rec := post(t, h, http.MethodPost, "/api/projects/p1/state/accept-drift", "")
	if body := rec.Body.String(); !strings.Contains(body, "event: done") {
		t.Fatalf("accept-drift stream = %s", body)
	}
	if !fake.acceptDriftCalled {
		t.Fatal("expected the runner's refresh-only apply to run")
	}
	if _, ok := srv.driftStatuses()["p1"]; ok {
		t.Fatal("stale drift status should be cleared after accepting")
	}
	// It lands in run history like the other state operations.
	list, _ := rh.List("p1")
	found := false
	for _, r := range list {
		if r.Op == "accept-drift" && r.Status == "success" {
			found = true
		}
	}
	if !found {
		t.Fatalf("accept-drift not recorded: %+v", list)
	}
}

// The check reports what a plan would do, and separates infrastructure that
// changed outside ArcLoom from configuration edits nobody applied yet.
func TestDriftSeparatesOutOfBandFromPendingEdits(t *testing.T) {
	fake := &fakeRunner{res: runner.PlanResult{
		HasChanges: true, Change: 2,
		Changes: []runner.PlanChange{
			{Address: "null_resource.touched", Action: "update"},
			{Address: "null_resource.edited", Action: "update"},
		},
		// Only the first one moved in the real world; the second is an unapplied
		// config edit. A third resource drifted in values the config does not
		// manage (a recomputed timestamp) — it must not count at all.
		Drift: []runner.PlanChange{
			{Address: "null_resource.touched", Action: "update"},
			{Address: "null_resource.noisy", Action: "update"},
		},
	}}
	srv, _ := driftTestServer(t, fake)
	srv.runDriftCheck(context.Background(), "p1")

	st := srv.driftStatuses()["p1"]
	if !st.Drifted || st.Change != 2 {
		t.Fatalf("status = %+v", st)
	}
	if st.OutOfBand != 1 {
		t.Fatalf("outOfBand = %d, want 1 (only the resource in both sets)", st.OutOfBand)
	}
	for _, a := range st.Addresses {
		if a == "null_resource.noisy" {
			t.Fatal("a resource that only drifted in unmanaged values must not be reported")
		}
	}
}
