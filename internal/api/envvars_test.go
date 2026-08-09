package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/envvar"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// envVarServer builds a server with a real secret store, so the secret-backed
// value path is exercised rather than mocked.
func envVarServer(t *testing.T) (http.Handler, *Server) {
	t.Helper()
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	sec := secrets.NewStore(t.TempDir(), "master-key-for-tests")
	if err := sec.Set("p1", "one_password", "hunter2"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	srv := New(cfg, nil, st, nil, sec, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil)
	return srv.Handler(), srv
}

func putEnvVars(t *testing.T, h http.Handler, query, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/projects/p1/env-vars"+query, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEnvVars_SetAndGet(t *testing.T) {
	h, _ := envVarServer(t)

	body := `{"vars":[
		{"name":"OPENNEBULA_ENDPOINT","value":"https://one/RPC2"},
		{"name":"OPENNEBULA_PASSWORD","secretRef":"one_password"}]}`
	if rec := putEnvVars(t, h, "", body); rec.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/env-vars", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status %d: %s", rec.Code, rec.Body.String())
	}
	var got envVarsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Vars) != 2 {
		t.Fatalf("got %d rows: %+v", len(got.Vars), got.Vars)
	}
	// A secret-backed row must never carry a value on the wire.
	for _, v := range got.Vars {
		if v.SecretRef != "" && v.Value != "" {
			t.Errorf("secret-backed row leaked a value: %+v", v)
		}
	}
	if len(got.Blocked) == 0 {
		t.Error("the blocked list should be reported so the UI can warn before a save fails")
	}
}

// The API is the first of two gates on the name (the runner re-checks at the
// sink). A `resources` user must not be able to set PATH on a process the
// server spawns.
func TestEnvVars_RefusesEscalatingNames(t *testing.T) {
	h, _ := envVarServer(t)
	for _, name := range []string{"PATH", "TF_CLI_ARGS", "TF_VAR_password", "ARCLOOM_SECRET_KEY", "LD_PRELOAD"} {
		body := `{"vars":[{"name":"` + name + `","value":"x"}]}`
		rec := putEnvVars(t, h, "", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// A secret-backed variable resolves through the same store a plan's TF_VAR_*
// secrets use, so the value is never persisted in settings.yaml.
func TestResolveEnvVars_ResolvesSecrets(t *testing.T) {
	_, srv := envVarServer(t)
	if err := srv.settings.SetEnvVars("p1", []envvar.Var{
		{Name: "OPENNEBULA_ENDPOINT", Value: "https://one/RPC2"},
		{Name: "OPENNEBULA_PASSWORD", SecretRef: "one_password"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := srv.resolveEnvVars("p1", settings.Environment{}, func(string, string) {})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got["OPENNEBULA_PASSWORD"] != "hunter2" {
		t.Errorf("secret not resolved: %q", got["OPENNEBULA_PASSWORD"])
	}
	if got["OPENNEBULA_ENDPOINT"] != "https://one/RPC2" {
		t.Errorf("literal not passed through: %q", got["OPENNEBULA_ENDPOINT"])
	}
}

// A missing secret OMITS the variable rather than setting it to "": an empty
// credential makes a provider report "authentication failed", which sends the
// user looking in the wrong place.
func TestResolveEnvVars_MissingSecretIsOmitted(t *testing.T) {
	_, srv := envVarServer(t)
	if err := srv.settings.SetEnvVars("p1", []envvar.Var{
		{Name: "OPENNEBULA_PASSWORD", SecretRef: "nope"},
	}); err != nil {
		t.Fatal(err)
	}
	warned := ""
	got, err := srv.resolveEnvVars("p1", settings.Environment{}, func(name, _ string) { warned = name })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, present := got["OPENNEBULA_PASSWORD"]; present {
		t.Error("a missing secret must not become an empty credential")
	}
	if warned != "OPENNEBULA_PASSWORD" {
		t.Errorf("the run should be warned, got %q", warned)
	}
}

// The per-environment override is the reason this is per project rather than
// per process: prod and dev point the same credential at different clusters.
func TestResolveEnvVars_EnvOverridesProject(t *testing.T) {
	_, srv := envVarServer(t)
	if err := srv.settings.SetEnvVars("p1", []envvar.Var{
		{Name: "OPENNEBULA_ENDPOINT", Value: "https://dev/RPC2"},
		{Name: "OPENNEBULA_USERNAME", Value: "dev"},
	}); err != nil {
		t.Fatal(err)
	}
	env := settings.Environment{Name: "prod", EnvVars: []envvar.Var{
		{Name: "OPENNEBULA_ENDPOINT", Value: "https://prod/RPC2"},
	}}
	got, err := srv.resolveEnvVars("p1", env, func(string, string) {})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got["OPENNEBULA_ENDPOINT"] != "https://prod/RPC2" {
		t.Errorf("env override lost: %q", got["OPENNEBULA_ENDPOINT"])
	}
	if got["OPENNEBULA_USERNAME"] != "dev" {
		t.Errorf("project row should survive: %q", got["OPENNEBULA_USERNAME"])
	}
}

// A name stored before a rule existed (or by an older binary) must not reach
// the process environment — resolve refuses rather than filtering silently.
func TestResolveEnvVars_RefusesStoredBadName(t *testing.T) {
	_, srv := envVarServer(t)
	// Bypass the API's validation the way an older binary would have.
	if err := srv.settings.SetEnvVars("p1", []envvar.Var{{Name: "PATH", Value: "/tmp/evil"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.resolveEnvVars("p1", settings.Environment{}, func(string, string) {}); err == nil {
		t.Fatal("a stored PATH must be refused at resolve time")
	}
}

// Deleting a project drops its environment variables with everything else.
func TestEnvVars_DroppedOnProjectDelete(t *testing.T) {
	h, srv := envVarServer(t)
	if err := srv.settings.SetEnvVars("p1", []envvar.Var{{Name: "A", Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/p1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("delete status %d: %s", rec.Code, rec.Body.String())
	}
	if got := srv.settings.EnvVars("p1"); len(got) != 0 {
		t.Errorf("env vars survived project delete: %+v", got)
	}
}
