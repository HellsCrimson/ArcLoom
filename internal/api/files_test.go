package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

func TestCompanionFilesCRUD(t *testing.T) {
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil).Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	// Create.
	if rec := do(http.MethodPut, "/api/projects/p1/files",
		`{"path":"templates/cloud-init.tftpl","content":"#cloud-config\nhostname: ${name}\n"}`); rec.Code != http.StatusOK {
		t.Fatalf("create file = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.Files) != 1 || got.Files[0].Path != "templates/cloud-init.tftpl" {
		t.Fatalf("file not stored: %+v", got.Files)
	}

	// Upsert by path replaces content, does not duplicate.
	if rec := do(http.MethodPut, "/api/projects/p1/files",
		`{"path":"templates/cloud-init.tftpl","content":"updated"}`); rec.Code != http.StatusOK {
		t.Fatalf("update = %d", rec.Code)
	}
	got, _ = st.Get("p1")
	if len(got.Files) != 1 || got.Files[0].Content != "updated" {
		t.Fatalf("upsert should replace in place: %+v", got.Files)
	}

	// Rename via oldPath moves it in one update (no duplicate left behind).
	if rec := do(http.MethodPut, "/api/projects/p1/files",
		`{"path":"templates/init.tftpl","oldPath":"templates/cloud-init.tftpl","content":"updated"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if len(got.Files) != 1 || got.Files[0].Path != "templates/init.tftpl" {
		t.Fatalf("rename should leave exactly one file: %+v", got.Files)
	}

	// The path is validated at the API, not merely at generation time.
	for _, body := range []string{
		`{"path":"../escape.txt","content":"x"}`,
		`{"path":"/etc/passwd","content":"x"}`,
		`{"path":"main.tf","content":"x"}`,
		`{"path":"terraform.tfstate","content":"x"}`,
		`{"path":"","content":"x"}`,
	} {
		if rec := do(http.MethodPut, "/api/projects/p1/files", body); rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %s, got %d", body, rec.Code)
		}
	}

	// Delete by ?path=.
	if rec := do(http.MethodDelete, "/api/projects/p1/files?path=templates/init.tftpl", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if len(got.Files) != 0 {
		t.Fatalf("file not deleted: %+v", got.Files)
	}
}

// TestGenerateFilesIncludesCompanionFiles is the point of the whole feature:
// the files map is the single funnel into both the on-disk project and every
// runner workspace, so landing in it is what makes file()/templatefile() work.
func TestGenerateFilesIncludesCompanionFiles(t *testing.T) {
	p := &model.Project{
		ID: "p1", Name: "demo", Engine: model.EngineTofu,
		LocalModules: []model.LocalModule{{Name: "team"}},
		Files: []model.ProjectFile{
			{Path: "templates/cloud-init.tftpl", Content: "hostname: ${name}"},
			{Path: "modules/team/user-data.yaml", Content: "k: v"},
		},
	}
	files, err := hclgen.GenerateFiles(p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// terraform.tf, not main.tf: the generated config is split by theme, and this
	// project declares no resources — an empty main.tf is deliberately not
	// emitted. The module's stub main.tf IS, so its folder exists.
	for _, want := range []string{"terraform.tf", "modules/team/main.tf", "templates/cloud-init.tftpl", "modules/team/user-data.yaml"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %q; got %v", want, keysOf(files))
		}
	}
	if string(files["templates/cloud-init.tftpl"]) != "hostname: ${name}" {
		t.Errorf("content mangled: %q", files["templates/cloud-init.tftpl"])
	}

	// A companion file must never displace generated HCL, even if the stored
	// model is hand-edited past the API's validation.
	p.Files = []model.ProjectFile{{Path: "modules/team/main.tf", Content: "resource \"x\" \"y\" {}"}}
	if _, err := hclgen.GenerateFiles(p); err == nil {
		t.Error("expected a file colliding with generated HCL to be rejected")
	}
	p.Files = []model.ProjectFile{{Path: "../escape.txt", Content: "x"}}
	if _, err := hclgen.GenerateFiles(p); err == nil {
		t.Error("expected a traversing path to be rejected at generation")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
