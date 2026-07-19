package api

import (
	"encoding/json"
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

func TestStateList(t *testing.T) {
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: dataDir}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil).Handler()

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
