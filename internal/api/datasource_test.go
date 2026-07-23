package api

import (
	"encoding/json"
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

// Upserting with kind="data" must land in DataSources (not Resources), and
// deleting with ?kind=data must remove it from there.
func TestUpsertAndDeleteDataSource(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil).Handler()

	body := `{"kind":"data","type":"null_data_source","name":"cfg","attrs":{}}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert data source = %d (body %s)", rec.Code, rec.Body.String())
	}
	var saved model.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}

	got, _ := st.Get("p1")
	if len(got.DataSources) != 1 || got.DataSources[0].Name != "cfg" {
		t.Fatalf("data source not stored in DataSources: %+v", got.DataSources)
	}
	if len(got.Resources) != 0 {
		t.Fatalf("data source leaked into Resources: %+v", got.Resources)
	}
	if len(got.Providers) != 1 || got.Providers[0].Key != "null" {
		t.Fatalf("provider not ensured: %+v", got.Providers)
	}

	// Delete without kind must not remove it; with kind=data it must.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/p1/resources/"+saved.ID, nil))
	if got, _ = st.Get("p1"); len(got.DataSources) != 1 {
		t.Fatal("delete without kind=data removed the data source")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/p1/resources/"+saved.ID+"?kind=data", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete data source = %d", rec.Code)
	}
	if got, _ = st.Get("p1"); len(got.DataSources) != 0 {
		t.Fatalf("data source not deleted: %+v", got.DataSources)
	}

	// Bad kind is rejected.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(`{"kind":"nope","type":"t","name":"n","attrs":{}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind = %d, want 400", rec.Code)
	}
}
