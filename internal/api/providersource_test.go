package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/model"
)

// A resource upsert carries the source of the provider the client THINKS the
// type belongs to. That hint may be stale (the palette holds whichever provider
// was touched last), so it must never be able to rewrite a provider the project
// already knows: the symptom is provider B silently adopting provider A's
// source, keeping only its key, after which every plan fails and the config
// form edits the wrong schema.
func TestUpsertResource_DoesNotRewriteKnownProviderSource(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "proxmox", Source: "telmate/proxmox"},
			{Key: "arcane", Source: "hellscrimson/arcane", Version: "~> 1.0"},
			{Key: "arcane", Alias: "second", Source: "hellscrimson/arcane"},
		}}
	h, st := newImportTestServer(t, p)

	// Saving an arcane resource while the palette still holds proxmox's source.
	body := `{"type":"arcane_vm","name":"vm","providerSource":"telmate/proxmox","attrs":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := st.Get("p1")
	for _, prov := range got.Providers {
		want := "telmate/proxmox"
		if prov.Key == "arcane" {
			want = "hellscrimson/arcane"
		}
		if prov.Source != want {
			t.Errorf("provider %q/%q source = %q, want %q", prov.Key, prov.Alias, prov.Source, want)
		}
	}
	// The aliased instance must be untouched too, version kept.
	for _, prov := range got.Providers {
		if prov.Key == "arcane" && prov.Alias == "" && prov.Version != "~> 1.0" {
			t.Errorf("version lost: %q", prov.Version)
		}
	}
}

// The same rewrite, between two providers that share the hashicorp namespace:
// there the genuine source and the fallback placeholder are the SAME string, so
// only the key check can tell a stale hint from a real one.
func TestUpsertResource_HashicorpProviderNotRewritten(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{
			{Key: "null", Source: "hashicorp/null"},
			{Key: "random", Source: "hashicorp/random"},
		}}
	h, st := newImportTestServer(t, p)

	body := `{"type":"random_pet","name":"pet","providerSource":"hashicorp/null","attrs":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	for _, prov := range got.Providers {
		if prov.Source != "hashicorp/"+prov.Key {
			t.Errorf("provider %q source = %q, want hashicorp/%s", prov.Key, prov.Source, prov.Key)
		}
	}
}

// A hint for a provider the project does not have yet still registers it — but
// only under the key its own type implies.
func TestUpsertResource_MismatchedHintNeverRegistersWrongSource(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu}
	h, st := newImportTestServer(t, p)

	body := `{"type":"arcane_vm","name":"vm","providerSource":"telmate/proxmox","attrs":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if len(got.Providers) != 1 || got.Providers[0].Key != "arcane" || got.Providers[0].Source != "hashicorp/arcane" {
		t.Fatalf("registered %+v, want one arcane provider on the fallback source", got.Providers)
	}
}

// The one rewrite ensureProvider IS allowed: a provider registered under the
// fallback "hashicorp/<key>" placeholder gets the real source once a resource
// save supplies it (and the source really is that provider's).
func TestUpsertResource_UpgradesPlaceholderSource(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{{Key: "arcane", Source: "hashicorp/arcane"}}}
	h, st := newImportTestServer(t, p)

	body := `{"type":"arcane_vm","name":"vm","providerSource":"hellscrimson/arcane","attrs":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if got.Providers[0].Source != "hellscrimson/arcane" {
		t.Errorf("placeholder not upgraded: %q", got.Providers[0].Source)
	}
}
