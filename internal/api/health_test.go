package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The health endpoints are public (no session required, auth enabled) and report
// a 200 with a small JSON body, so a container liveness probe never needs a
// cookie.
func TestHealthEndpoints_PublicAndOK(t *testing.T) {
	h := newAuthTestServer(t) // auth enabled, no session

	for _, path := range []string{"/healthz", "/livez"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Errorf("GET %s: body %q, want status ok", path, rec.Body.String())
		}
	}
}
