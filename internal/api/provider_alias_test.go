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

// Aliased provider instances: created via PUT with an alias (inheriting the
// default instance's source/version), selected by resources via the provider
// field, and protected from deletion while referenced.
func TestProviderAliasLifecycle(t *testing.T) {
	st := store.NewMemory()
	p := &model.Project{ID: "p1", Name: "demo", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{{Key: "aws", Source: "hashicorp/aws", Version: "~> 5.0"}},
	}
	if err := st.Create(p); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthEnabled: false, CookieName: "arcloom_session", DataDir: t.TempDir()}
	authn := auth.New(auth.NewLocalProvider(user.NewMemory()))
	h := New(cfg, nil, st, nil, nil, nil, authn, session.NewMemory(), user.NewMemory(), testSettings(t), nil, nil, nil, nil, nil).Handler()

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		var rd *strings.Reader
		if body == "" {
			rd = strings.NewReader("")
		} else {
			rd = strings.NewReader(body)
		}
		h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
		return rec
	}

	// Create an aliased instance; it inherits source/version from the default.
	rec := do(http.MethodPut, "/api/projects/p1/providers/aws",
		`{"alias":"euwest","config":{"region":{"mode":"literal","literal":"eu-west-1"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create alias = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.Providers) != 2 {
		t.Fatalf("providers = %+v, want 2 instances", got.Providers)
	}
	ali := got.Providers[1]
	if ali.Alias != "euwest" || ali.Source != "hashicorp/aws" || ali.Version != "~> 5.0" {
		t.Fatalf("aliased instance = %+v, want inherited source/version", ali)
	}

	// A bad alias is rejected.
	if rec := do(http.MethodPut, "/api/projects/p1/providers/aws", `{"alias":"eu west","config":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad alias = %d, want 400", rec.Code)
	}

	// Save a resource selecting the alias.
	rec = do(http.MethodPost, "/api/projects/p1/resources",
		`{"type":"aws_instance","name":"eu","provider":"aws.euwest","attrs":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert = %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if got.Resources[0].Provider != "aws.euwest" {
		t.Fatalf("resource provider = %q", got.Resources[0].Provider)
	}

	// A malformed provider ref is rejected.
	if rec := do(http.MethodPost, "/api/projects/p1/resources",
		`{"type":"aws_instance","name":"x","provider":"aws.eu.west","attrs":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad provider ref = %d, want 400", rec.Code)
	}

	// Deleting the referenced instance is refused.
	if rec := do(http.MethodDelete, "/api/projects/p1/providers/aws?alias=euwest", ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete in-use alias = %d, want 409", rec.Code)
	}

	// Unselect the alias, then delete it.
	rid := got.Resources[0].ID
	rec = do(http.MethodPost, "/api/projects/p1/resources",
		`{"id":"`+rid+`","type":"aws_instance","name":"eu","attrs":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear provider = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodDelete, "/api/projects/p1/providers/aws?alias=euwest", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete alias = %d", rec.Code)
	}
	got, _ = st.Get("p1")
	if len(got.Providers) != 1 || got.Providers[0].Alias != "" {
		t.Fatalf("providers after delete = %+v", got.Providers)
	}

	// Deleting an unknown instance 404s.
	if rec := do(http.MethodDelete, "/api/projects/p1/providers/aws?alias=nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown alias = %d, want 404", rec.Code)
	}
}
