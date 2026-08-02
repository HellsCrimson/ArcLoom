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

func TestSetMovedBlocks(t *testing.T) {
	st := store.NewMemory()
	if err := st.Create(&model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil).Handler()

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/projects/p1/moved", strings.NewReader(body)))
		return rec
	}

	rec := put(`{"moved":[
		{"from":"null_resource.www","to":"null_resource.sub[\"www\"]"},
		{"from":"null_resource.api","to":"null_resource.sub[\"api\"]"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set moved = %d (body %s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.Moved) != 2 {
		t.Fatalf("want 2 moved blocks, got %+v", got.Moved)
	}
	// The server mints an id per block so the UI can delete one.
	if got.Moved[0].ID == "" || got.Moved[0].ID == got.Moved[1].ID {
		t.Errorf("ids not assigned: %+v", got.Moved)
	}

	// An address the engine resolves before evaluation cannot hold an
	// expression — refuse it here rather than at plan time.
	for _, body := range []string{
		`{"moved":[{"from":"null_resource.a","to":"null_resource.sub[each.key]"}]}`,
		`{"moved":[{"from":"toset([\"a\"])","to":"null_resource.a"}]}`,
		`{"moved":[{"from":"null_resource.a","to":"]] nope"}]}`,
		`{"moved":[{"from":"","to":"null_resource.a"}]}`,
		`{"moved":[{"from":"null_resource.a","to":""}]}`,
		`{"moved":[{"from":"null_resource.a","to":"null_resource.a"}]}`,
	} {
		if rec := put(body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400 (body %s)", body, rec.Code, rec.Body.String())
		}
	}
	// A rejected request must not have touched the stored list.
	if got, _ = st.Get("p1"); len(got.Moved) != 2 {
		t.Fatalf("stored list changed by a rejected request: %+v", got.Moved)
	}

	// Replace-the-list: an empty array is how the UI clears applied blocks.
	if rec := put(`{"moved":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d", rec.Code)
	}
	if got, _ = st.Get("p1"); len(got.Moved) != 0 {
		t.Fatalf("moved blocks not cleared: %+v", got.Moved)
	}
}
