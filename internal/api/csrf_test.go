package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRFSafe(t *testing.T) {
	s := &Server{} // csrfSafe reads only the request
	mk := func(fetchSite, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://arcloom.example/api/x", nil)
		if fetchSite != "" {
			r.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	cases := []struct {
		name      string
		fetchSite string
		origin    string
		want      bool
	}{
		{"fetch same-origin", "same-origin", "", true},
		{"fetch direct navigation", "none", "", true},
		{"fetch same-site subdomain", "same-site", "", false},
		{"fetch cross-site", "cross-site", "", false},
		{"cross-site wins over matching origin", "cross-site", "http://arcloom.example", false},
		{"no metadata, matching origin", "", "http://arcloom.example", true},
		{"no metadata, foreign origin", "", "http://evil.example", false},
		{"no metadata, no origin (curl)", "", "", true},
	}
	for _, tc := range cases {
		if got := s.csrfSafe(mk(tc.fetchSite, tc.origin)); got != tc.want {
			t.Errorf("%s: csrfSafe = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// doWithHeaders is do() plus arbitrary request headers.
func doWithHeaders(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A cross-site request is rejected by the guard on both a mutating route and a
// side-effectful GET SSE route — after the permission gate, so the caller must
// have the perm for the check to be reached.
func TestCSRFGuard_BlocksCrossSite(t *testing.T) {
	h, _, _ := newRBACServer(t)
	editor := login(t, h, "editor", "password1") // resources perm
	admin := login(t, h, "admin", "password1")   // all perms (reaches deploy routes)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}

	// Mutating route (resources group).
	if rec := doWithHeaders(t, h, http.MethodPost, "/api/projects", editor, `{"name":"x","engine":"tofu"}`, cross); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site create: got %d, want 403", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "cross-origin") {
		t.Errorf("cross-site create: want cross-origin message, got %q", rec.Body.String())
	}

	// GET SSE plan (deploy group) — admin passes the perm gate, then the guard bites.
	if rec := doWithHeaders(t, h, http.MethodGet, "/api/projects/p1/plan", admin, "", cross); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site plan: got %d, want 403", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "cross-origin") {
		t.Errorf("cross-site plan: want cross-origin message, got %q", rec.Body.String())
	}

	// A same-origin request passes the guard (reaches the handler; not a csrf 403).
	if rec := doWithHeaders(t, h, http.MethodPost, "/api/projects", editor, `{"name":"x","engine":"tofu"}`, map[string]string{"Sec-Fetch-Site": "same-origin"}); strings.Contains(rec.Body.String(), "cross-origin") {
		t.Errorf("same-origin create was rejected as cross-origin: %d %q", rec.Code, rec.Body.String())
	}
}

// An oversized request body is rejected (the login route carries the small cap).
func TestLimitBody_RejectsOversized(t *testing.T) {
	h, _, _ := newRBACServer(t)
	big := `{"username":"a","password":"` + strings.Repeat("A", maxBodySmall+1024) + `"}`
	rec := do(t, h, http.MethodPost, "/api/auth/login", nil, big)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized login body: got %d, want 400", rec.Code)
	}
}
