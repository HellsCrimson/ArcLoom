package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"

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

// fakeRunner implements runner.Runner for drift tests; only RefreshPlan is
// meaningful.
type fakeRunner struct {
	res runner.PlanResult
	err error
}

func (f *fakeRunner) Engine() model.Engine { return model.EngineTofu }
func (f *fakeRunner) ExecPath() string     { return "fake" }
func (f *fakeRunner) Version(context.Context) (string, error) {
	return "0.0.0", nil
}
func (f *fakeRunner) ProvidersSchema(context.Context, []model.ProviderInstance) (*tfjson.ProviderSchemas, error) {
	return nil, nil
}
func (f *fakeRunner) Plan(context.Context, string, map[string][]byte, map[string]string, io.Writer) (runner.PlanResult, error) {
	return runner.PlanResult{}, nil
}
func (f *fakeRunner) RefreshPlan(context.Context, string, map[string][]byte, map[string]string, io.Writer) (runner.PlanResult, error) {
	return f.res, f.err
}
func (f *fakeRunner) Apply(context.Context, string, map[string][]byte, map[string]string, io.Writer) error {
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
	srv := New(cfg, map[model.Engine]runner.Runner{model.EngineTofu: fake}, st, nil, nil, nil,
		authn, session.NewMemory(), user.NewMemory(), testSettings(t), rh)
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
