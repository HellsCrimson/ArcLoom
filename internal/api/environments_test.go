package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

func TestEnvironmentsEndpoints(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{}) // auth disabled, project "p1"
	h := srv.Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/environments", strings.NewReader(body)))
		return rec
	}

	// Valid environments persist.
	if rec := put(`{"environments":[{"name":"dev","vars":{"region":"eu-west-1"}},{"name":"prod","backendConfig":{"key":"prod/tf.state"}}]}`); rec.Code != http.StatusOK {
		t.Fatalf("set environments = %d (%s)", rec.Code, rec.Body.String())
	}
	envs := srv.settings.Environments("p1")
	if len(envs) != 2 || envs[0].Name != "dev" || envs[1].BackendConfig["key"] != "prod/tf.state" {
		t.Fatalf("environments not stored: %+v", envs)
	}

	// GET returns them.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/environments", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"prod"`) {
		t.Fatalf("get environments = %d (%s)", rec.Code, rec.Body.String())
	}

	// Rejections: bad name, duplicate, bad backend key, bad var name.
	for _, body := range []string{
		`{"environments":[{"name":"../etc"}]}`,
		`{"environments":[{"name":"dev"},{"name":"dev"}]}`,
		`{"environments":[{"name":"dev","backendConfig":{"bad key":"x"}}]}`,
		`{"environments":[{"name":"dev","vars":{"1bad":"x"}}]}`,
	} {
		if rec := put(body); rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %s, got %d", body, rec.Code)
		}
	}

	// An empty list clears them.
	if rec := put(`{"environments":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clear environments = %d", rec.Code)
	}
	if envs := srv.settings.Environments("p1"); envs != nil {
		t.Fatalf("environments not cleared: %+v", envs)
	}
}

// A deploy/state request naming an environment that isn't configured is rejected
// (400) rather than silently hitting the default workspace.
func TestUnknownEnvironmentRejected(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{})
	h := srv.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/state?env=ghost", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("state with unknown env = %d, want 400", rec.Code)
	}

	// A configured env is accepted (empty state -> 200 empty list).
	if err := srv.settings.SetEnvironments("p1", []settings.Environment{{Name: "dev"}}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/p1/state?env=dev", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("state with known env = %d, want 200", rec.Code)
	}
}

func TestHCLLiteral(t *testing.T) {
	cases := map[string]string{
		"my-bucket": `"my-bucket"`,
		"true":      "true",
		"false":     "false",
		"42":        "42",
		"3.14":      "3.14",
		"us east":   `"us east"`,
	}
	for in, want := range cases {
		if got := hclLiteral(in); got != want {
			t.Errorf("hclLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}
