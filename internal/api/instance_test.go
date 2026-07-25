package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The instance panel reports what the operator needs to debug a deployment —
// and nothing that could leak: no secret key, no license token, no credentials.
func TestInstanceInfo(t *testing.T) {
	srv, _ := driftTestServer(t, &fakeRunner{})
	srv.SetBuildInfo(BuildInfo{Version: "v1.2.3", Commit: "abc1234", Date: "2026-01-01T00:00:00Z"})
	h := srv.Handler()

	rec := post(t, h, http.MethodGet, "/api/admin/instance", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET instance = %d (%s)", rec.Code, rec.Body.String())
	}
	var info instanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "v1.2.3" || info.Commit != "abc1234" {
		t.Fatalf("build metadata missing: %+v", info)
	}
	if info.Go == "" || info.Platform == "" || info.StartedAt == "" {
		t.Fatalf("runtime info missing: %+v", info)
	}
	if info.SchemaFormat == 0 {
		t.Error("schema format version should be reported (it keys the schema cache)")
	}
	if info.Projects != 1 {
		t.Errorf("projects = %d, want the seeded one", info.Projects)
	}
	if _, ok := info.Paths["data"]; !ok {
		t.Errorf("paths should include the data dir: %+v", info.Paths)
	}
	// Every optional tool is reported either way, so "not installed" is visible
	// rather than absent.
	if len(info.Tools) != len(optionalTools) {
		t.Fatalf("tools = %d, want %d", len(info.Tools), len(optionalTools))
	}
	body := rec.Body.String()
	for _, leak := range []string{"ARCLOOM_SECRET_KEY=", "secretKey", "licenseToken", "clientSecret"} {
		if strings.Contains(body, leak) {
			t.Errorf("instance payload leaked %q", leak)
		}
	}
}
