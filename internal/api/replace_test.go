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

// PUT /projects/{id} swaps the whole editable model (undo/redo restore) while
// preserving identity fields and the state backend, and rejects models that
// cannot generate.
func TestReplaceProject(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Backend:   &model.Backend{Type: "s3"},
		Providers: []model.ProviderInstance{{Key: "null", Source: "hashicorp/null"}},
		Resources: []model.Resource{{ID: "r1", Type: "null_resource", Name: "keep"}},
	}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil, nil).Handler()

	do := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, path, strings.NewReader(body)))
		return rec
	}

	// Snapshot with different resources AND attempts to change identity/backend.
	body := `{"id":"evil","name":"renamed","engine":"terraform","backend":{"type":"gcs"},
		"providers":[{"key":"null","source":"hashicorp/null"}],
		"resources":[{"id":"r2","type":"null_resource","name":"restored","attrs":{}}]}`
	rec := do("/api/projects/p1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.Resources) != 1 || got.Resources[0].Name != "restored" {
		t.Fatalf("resources not replaced: %+v", got.Resources)
	}
	if got.ID != "p1" || got.Name != "demo" || got.Engine != model.EngineTofu {
		t.Fatalf("identity not preserved: %+v", got)
	}
	if got.Backend == nil || got.Backend.Type != "s3" {
		t.Fatalf("backend not preserved: %+v", got.Backend)
	}

	// A model that cannot generate (count + for_each) is rejected unchanged.
	bad := `{"resources":[{"id":"r3","type":"null_resource","name":"x","count":"1","forEach":"toset([1])"}]}`
	if rec := do("/api/projects/p1", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid model = %d, want 400", rec.Code)
	}
	got, _ = st.Get("p1")
	if got.Resources[0].Name != "restored" {
		t.Fatalf("rejected replace mutated the project: %+v", got.Resources)
	}

	if rec := do("/api/projects/nope", `{"resources":[]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d, want 404", rec.Code)
	}
}
