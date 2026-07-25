package api

import (
	"context"
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
	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// fakeRunner implements runner.Runner for drift / approval / policy-gate tests;
// only RefreshPlan, Plan, Apply(Plan) and Policy are meaningful.
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
	// gate reads back to apply exactly what it checked).
	planFile []byte
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
	return nil, nil
}
func (f *fakeRunner) Plan(_ context.Context, dir string, _ map[string][]byte, _ map[string]string, _ io.Writer) (runner.PlanResult, error) {
	f.planCalled = true
	// Mimic the real runner leaving a saved plan behind, which the policy gate
	// reads back to apply exactly what it checked.
	if dir != "" && f.planFile != nil {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(dir, planFileName), f.planFile, 0o644)
		}
	}
	return runner.PlanResult{}, nil
}
func (f *fakeRunner) RefreshPlan(context.Context, string, map[string][]byte, map[string]string, io.Writer) (runner.PlanResult, error) {
	return f.res, f.err
}
func (f *fakeRunner) Apply(context.Context, string, map[string][]byte, map[string]string, io.Writer) error {
	f.applyCalled = true
	return f.applyErr
}
func (f *fakeRunner) ApplyPlan(_ context.Context, _ string, _ map[string][]byte, _ map[string]string, planBytes []byte, _ io.Writer) error {
	f.applyPlanCalled = true
	f.applyPlanBytes = planBytes
	return nil
}
func (f *fakeRunner) Destroy(context.Context, string, map[string][]byte, map[string]string, io.Writer) error {
	return nil
}
func (f *fakeRunner) Outputs(context.Context, string) (map[string]tfexec.OutputMeta, error) {
	return nil, nil
}
func (f *fakeRunner) Import(context.Context, string, map[string][]byte, map[string]string, string, string, io.Writer) error {
	return nil
}
func (f *fakeRunner) StateRm(context.Context, string, map[string][]byte, map[string]string, string, io.Writer) error {
	return nil
}
func (f *fakeRunner) StateMv(context.Context, string, map[string][]byte, map[string]string, string, string, io.Writer) error {
	return nil
}
func (f *fakeRunner) InitBackend(context.Context, string, map[string][]byte, bool, io.Writer) error {
	return nil
}
func (f *fakeRunner) StatePull(context.Context, string) (string, error) { return "", nil }
func (f *fakeRunner) Validate(context.Context, string, map[string][]byte, map[string]string, io.Writer) (*tfjson.ValidateOutput, error) {
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
	// Scheduled drift is enterprise-gated; run these tests as a licensed install.
	srv.license.Install(&license.License{Tier: license.TierEnterprise})
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
