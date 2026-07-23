package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// Deleting a project whose persisted state still tracks managed resources must
// be refused with 409 unless forced.
func TestDeleteProjectGuardsDeployedState(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: dataDir}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil).Handler()

	// State with one managed and one data resource → one deployed resource.
	runDir := filepath.Join(dataDir, "p1", "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"version":4,"resources":[{"mode":"managed","type":"null_resource","name":"a"},{"mode":"data","type":"null_data_source","name":"d"}]}`
	if err := os.WriteFile(filepath.Join(runDir, "terraform.tfstate"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	del := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
		return rec
	}

	if rec := del("/api/projects/p1"); rec.Code != http.StatusConflict {
		t.Fatalf("delete with deployed state = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if _, err := st.Get("p1"); err != nil {
		t.Fatal("project was deleted despite the guard")
	}

	if rec := del("/api/projects/p1?force=true"); rec.Code != http.StatusNoContent {
		t.Fatalf("forced delete = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if _, err := st.Get("p1"); err == nil {
		t.Fatal("forced delete did not remove the project")
	}

	// Empty/missing state must not block deletion.
	p2 := &model.Project{ID: "p2", Name: "empty", Engine: model.EngineTofu}
	if err := st.Create(p2); err != nil {
		t.Fatal(err)
	}
	if rec := del("/api/projects/p2"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete without state = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
}
