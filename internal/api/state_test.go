package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

func TestStateList(t *testing.T) {
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: dataDir}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil).Handler()

	get := func() []stateEntry {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/state", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("state list = %d (body %s)", rec.Code, rec.Body.String())
		}
		var out []stateEntry
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// No state file -> empty list.
	if out := get(); len(out) != 0 {
		t.Fatalf("expected empty list, got %+v", out)
	}

	runDir := filepath.Join(dataDir, "p1", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":4,"resources":[
		{"mode":"managed","type":"null_resource","name":"a","instances":[{"index_key":0},{"index_key":1}]},
		{"mode":"managed","type":"null_resource","name":"b","module":"module.net","instances":[{}]},
		{"mode":"data","type":"null_data_source","name":"cfg","instances":[{"index_key":"x"}]}
	]}`
	if err := os.WriteFile(filepath.Join(runDir, "terraform.tfstate"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	out := get()
	if len(out) != 3 {
		t.Fatalf("expected 3 entries, got %+v", out)
	}
	byAddr := map[string]stateEntry{}
	for _, e := range out {
		byAddr[e.Address] = e
	}
	if e := byAddr["null_resource.a"]; len(e.Instances) != 2 || e.Instances[0] != "0" {
		t.Errorf("count instances not extracted: %+v", e)
	}
	if e, ok := byAddr["module.net.null_resource.b"]; !ok || len(e.Instances) != 0 {
		t.Errorf("module address wrong: %+v", e)
	}
	if e, ok := byAddr[`data.null_data_source.cfg`]; !ok || len(e.Instances) != 1 || e.Instances[0] != `"x"` {
		t.Errorf("data address wrong: %+v", e)
	}
}

// The terraform endpoint stores the constraint, validates it, and the generated
// HCL carries it — the whole point being tflint's terraform_required_version.
func TestSetTerraformRequiredVersion(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{})
	h := srv.Handler()

	if rec := post(t, h, http.MethodPut, "/api/projects/p1/terraform", `{"requiredVersion":">= 1.6.0"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	p, _ := srv.store.Get("p1")
	if p.RequiredVersion != ">= 1.6.0" {
		t.Fatalf("stored = %q", p.RequiredVersion)
	}
	hcl, err := hclgen.Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hcl), `required_version = ">= 1.6.0"`) {
		t.Fatalf("generated HCL lacks the constraint:\n%s", hcl)
	}

	// A malformed constraint is rejected here, not at plan time.
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/terraform", `{"requiredVersion":"totally bogus"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus constraint = %d, want 400", rec.Code)
	}
	if p, _ := srv.store.Get("p1"); p.RequiredVersion != ">= 1.6.0" {
		t.Fatalf("rejected constraint must not overwrite: %q", p.RequiredVersion)
	}
	// Clearing is always allowed.
	if rec := post(t, h, http.MethodPut, "/api/projects/p1/terraform", `{"requiredVersion":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d", rec.Code)
	}
	if p, _ := srv.store.Get("p1"); p.RequiredVersion != "" {
		t.Fatalf("not cleared: %q", p.RequiredVersion)
	}
	if rec := post(t, h, http.MethodPut, "/api/projects/nope/terraform", `{"requiredVersion":">= 1.0"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404", rec.Code)
	}
}

// A tainted instance is reported with the exact address untaint takes, so the UI
// needs no address arithmetic — including the indexed case.
func TestStateListReportsTaintedInstances(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{})
	h := srv.Handler()

	runDir := filepath.Join(srv.cfg.DataDir, "p1", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":4,"resources":[
		{"mode":"managed","type":"null_resource","name":"single","instances":[{"status":"tainted"}]},
		{"mode":"managed","type":"null_resource","name":"counted","instances":[{"index_key":0},{"index_key":1,"status":"tainted"}]},
		{"mode":"managed","type":"null_resource","name":"clean","instances":[{}]}
	]}`
	if err := os.WriteFile(filepath.Join(runDir, "terraform.tfstate"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := post(t, h, http.MethodGet, "/api/projects/p1/state", "")
	var out []stateEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byName := map[string]stateEntry{}
	for _, e := range out {
		byName[e.Name] = e
	}
	if got := byName["single"].Tainted; len(got) != 1 || got[0] != "null_resource.single" {
		t.Fatalf("single tainted = %v", got)
	}
	if got := byName["counted"].Tainted; len(got) != 1 || got[0] != "null_resource.counted[1]" {
		t.Fatalf("counted tainted = %v, want only the indexed instance", got)
	}
	if got := byName["clean"].Tainted; len(got) != 0 {
		t.Fatalf("clean resource reported as tainted: %v", got)
	}
}

// Force-unlock streams like the other state ops, passes the lock ID straight
// through and lands in run history; a missing ID fails before the stream starts.
func TestForceUnlockEndpoint(t *testing.T) {
	fake := &fakeRunner{}
	srv, rh := driftTestServer(t, fake)
	h := srv.Handler()

	rec := post(t, h, http.MethodPost, "/api/projects/p1/state/force-unlock?lockId=abc-123", "")
	if body := rec.Body.String(); !strings.Contains(body, "event: done") {
		t.Fatalf("force-unlock stream = %s", body)
	}
	if fake.unlockedID != "abc-123" {
		t.Fatalf("runner received lock id %q", fake.unlockedID)
	}
	list, _ := rh.List("p1")
	found := false
	for _, r := range list {
		if r.Op == "force-unlock" && r.Status == "success" {
			found = true
		}
	}
	if !found {
		t.Fatalf("force-unlock not recorded: %+v", list)
	}

	// No ID: rejected before streaming, so the engine is never called.
	fake.unlockedID = ""
	for _, path := range []string{
		"/api/projects/p1/state/force-unlock",
		"/api/projects/p1/state/force-unlock?lockId=",
		"/api/projects/p1/state/force-unlock?lockId=has%20space",
	} {
		if rec := post(t, h, http.MethodPost, path, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST %s = %d, want 400", path, rec.Code)
		}
	}
	if fake.unlockedID != "" {
		t.Fatalf("engine called for a rejected request: %q", fake.unlockedID)
	}
}

// Taint and untaint stream like the other state ops, pass the address through and
// land in run history; a missing address fails before the stream starts.
func TestTaintUntaintEndpoints(t *testing.T) {
	fake := &fakeRunner{}
	srv, rh := driftTestServer(t, fake)
	h := srv.Handler()

	for _, c := range []struct{ op, path, want string }{
		{"taint", "/api/projects/p1/state/taint?address=null_resource.a", "null_resource.a"},
		{"untaint", "/api/projects/p1/state/untaint?address=null_resource.a%5B1%5D", "null_resource.a[1]"},
	} {
		rec := post(t, h, http.MethodPost, c.path, "")
		if body := rec.Body.String(); !strings.Contains(body, "event: done") {
			t.Fatalf("%s stream = %s", c.op, body)
		}
		got := fake.tainted
		if c.op == "untaint" {
			got = fake.untainted
		}
		if got != c.want {
			t.Fatalf("%s received %q, want %q", c.op, got, c.want)
		}
	}

	list, _ := rh.List("p1")
	ops := map[string]bool{}
	for _, r := range list {
		ops[r.Op] = true
	}
	if !ops["taint"] || !ops["untaint"] {
		t.Fatalf("state ops not recorded in history: %+v", ops)
	}

	// No address: rejected before streaming, so no run record and no engine call.
	for _, path := range []string{"/api/projects/p1/state/taint", "/api/projects/p1/state/untaint?address="} {
		if rec := post(t, h, http.MethodPost, path, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST %s = %d, want 400", path, rec.Code)
		}
	}
}
