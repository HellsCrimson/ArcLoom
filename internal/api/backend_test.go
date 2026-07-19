package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

func TestSetAndClearBackend(t *testing.T) {
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil).Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/backend", strings.NewReader(body)))
		return rec
	}

	// Set an s3 backend.
	rec := put(`{"type":"s3","config":{"bucket":{"mode":"expression","expr":"\"my-state\""}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set backend = %d (body %s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if got.Backend == nil || got.Backend.Type != "s3" || len(got.Backend.Config) != 1 {
		t.Fatalf("backend not stored: %+v", got.Backend)
	}

	// Invalid type / bad expression are rejected.
	if rec := put(`{"type":"s3; rm -rf","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type = %d, want 400", rec.Code)
	}
	if rec := put(`{"type":"s3","config":{"x":{"mode":"expression","expr":"]] nope"}}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad expr = %d, want 400", rec.Code)
	}

	// Empty type clears the backend (back to local state).
	if rec := put(`{"type":"","config":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("clear backend = %d", rec.Code)
	}
	if got, _ = st.Get("p1"); got.Backend != nil {
		t.Fatalf("backend not cleared: %+v", got.Backend)
	}
}
