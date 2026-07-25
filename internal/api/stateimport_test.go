package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// A minimal state with two managed null_resources, as written by the engine.
const twoResourceState = `{
  "version": 4,
  "terraform_version": "1.12.1",
  "serial": 3,
  "lineage": "aaaa-bbbb",
  "resources": [
    {"mode":"managed","type":"null_resource","name":"one","provider":"provider[\"registry.opentofu.org/hashicorp/null\"]",
     "instances":[{"attributes":{"id":"111","triggers":{"msg":"a"}}}]},
    {"mode":"managed","type":"null_resource","name":"two","provider":"provider[\"registry.opentofu.org/hashicorp/null\"]",
     "instances":[{"attributes":{"id":"222","triggers":{"msg":"b"}}}]}
  ]
}`

// nullSchemas is the provider schema the import consults to keep only settable
// attributes (id is computed, triggers is settable).
func nullSchemas() *tfjson.ProviderSchemas {
	return &tfjson.ProviderSchemas{
		FormatVersion: "1.0",
		Schemas: map[string]*tfjson.ProviderSchema{
			"registry.opentofu.org/hashicorp/null": {
				ResourceSchemas: map[string]*tfjson.Schema{
					"null_resource": {Block: &tfjson.SchemaBlock{
						Attributes: map[string]*tfjson.SchemaAttribute{
							"id":       {Computed: true},
							"triggers": {Optional: true},
						},
					}},
				},
			},
		},
	}
}

func importState(t *testing.T, h http.Handler, query, state string) (*int, importResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"state": state})
	if err != nil {
		t.Fatal(err)
	}
	rec := post(t, h, http.MethodPost, "/api/projects/p1/import/state"+query, string(body))
	code := rec.Code
	var resp importResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if code != http.StatusOK {
		t.Logf("import/state -> %d: %s", code, rec.Body.String())
	}
	return &code, resp
}

// The import adopts BOTH halves: the model gets the resources and the state file
// becomes the project's own, so the State panel lists it and the next plan does
// not propose creating everything again.
func TestImportStateAdoptsModelAndState(t *testing.T) {
	fake := &fakeRunner{providerSchemas: nullSchemas()}
	srv, _ := driftTestServer(t, fake)
	h := srv.Handler()

	code, resp := importState(t, h, "", twoResourceState)
	if *code != http.StatusOK {
		t.Fatalf("import = %d", *code)
	}
	if resp.AddedResources != 2 || resp.AdoptedState != 2 {
		t.Fatalf("resp = %+v", resp)
	}

	// The model now describes the resources...
	p, err := srv.store.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Resources) != 2 {
		t.Fatalf("model has %d resources, want 2", len(p.Resources))
	}
	if _, ok := p.Resources[0].Attrs["triggers"]; !ok {
		t.Fatalf("settable attrs lost: %+v", p.Resources[0].Attrs)
	}
	if _, ok := p.Resources[0].Attrs["id"]; ok {
		t.Error("computed id should not be imported into the config")
	}

	// ...and the workspace state exists, so the State panel is not empty.
	raw, err := os.ReadFile(filepath.Join(srv.cfg.DataDir, "p1", "run", "terraform.tfstate"))
	if err != nil {
		t.Fatalf("state not adopted into the workspace: %v", err)
	}
	if string(raw) != twoResourceState {
		t.Error("adopted state should be the file the user imported, verbatim")
	}
	rec := post(t, h, http.MethodGet, "/api/projects/p1/state", "")
	var entries []stateEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Address != "null_resource.one" {
		t.Fatalf("state list = %+v", entries)
	}

	// One engine call for the whole state, not one per resource (that made a
	// modest state take tens of seconds).
	if fake.schemaCalls != 1 {
		t.Errorf("ProvidersSchema called %d times, want 1 (batched)", fake.schemaCalls)
	}
}

// Replacing a state that still tracks resources would orphan them, so it is
// refused until the caller explicitly forces it — and the refusal changes nothing.
func TestImportStateRefusesToReplaceLiveState(t *testing.T) {
	fake := &fakeRunner{providerSchemas: nullSchemas()}
	srv, _ := driftTestServer(t, fake)
	h := srv.Handler()

	dir := filepath.Join(srv.cfg.DataDir, "p1", "run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"version":4,"resources":[{"mode":"managed","type":"null_resource","name":"live","instances":[{"attributes":{}}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _ := importState(t, h, "", twoResourceState)
	if *code != http.StatusConflict {
		t.Fatalf("import over a live state = %d, want 409", *code)
	}
	// Nothing was touched: neither the state nor the model.
	raw, _ := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if string(raw) != existing {
		t.Fatal("refused import must leave the state alone")
	}
	if p, _ := srv.store.Get("p1"); len(p.Resources) != 0 {
		t.Fatalf("refused import must not change the model, got %d resources", len(p.Resources))
	}

	// Forcing it through replaces the state.
	code, resp := importState(t, h, "?force=true", twoResourceState)
	if *code != http.StatusOK {
		t.Fatalf("forced import = %d", *code)
	}
	if resp.AdoptedState != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if string(raw) != twoResourceState {
		t.Fatal("forced import should replace the state")
	}
}

// With a remote backend the state has to go through the CLI, so it reaches the
// backend instead of a local file the engine would ignore.
func TestImportStateWithBackendPushes(t *testing.T) {
	fake := &fakeRunner{providerSchemas: nullSchemas()}
	srv, _ := driftTestServer(t, fake)
	if err := srv.store.Update("p1", func(p *model.Project) error {
		p.Backend = &model.Backend{Type: "local"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	code, resp := importState(t, h, "", twoResourceState)
	if *code != http.StatusOK {
		t.Fatalf("import = %d", *code)
	}
	if resp.AdoptedState != 2 {
		t.Fatalf("resp = %+v (warnings %v)", resp, resp.Warnings)
	}
	if string(fake.statePushed) != twoResourceState {
		t.Fatalf("state not pushed to the backend: %q", fake.statePushed)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "p1", "run", "terraform.tfstate")); err == nil {
		t.Error("with a backend the state must not be written as a local file")
	}
}

// A state import targets the selected environment's workspace, like every other
// state operation.
func TestImportStateHonoursEnvironment(t *testing.T) {
	fake := &fakeRunner{providerSchemas: nullSchemas()}
	srv, _ := driftTestServer(t, fake)
	if err := srv.settings.SetEnvironments("p1", []settings.Environment{{Name: "dev"}}); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	if code, _ := importState(t, h, "?env=dev", twoResourceState); *code != http.StatusOK {
		t.Fatalf("import = %d", *code)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "p1", "run", "envs", "dev", "terraform.tfstate")); err != nil {
		t.Fatalf("state not adopted into the env workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.DataDir, "p1", "run", "terraform.tfstate")); err == nil {
		t.Error("the default workspace must be left alone")
	}
	// An unknown environment fails before anything is written.
	if code, _ := importState(t, h, "?env=nope", twoResourceState); *code != http.StatusBadRequest {
		t.Fatalf("unknown env = %d, want 400", *code)
	}
}

// A name already used in the model is imported under a new name, which breaks the
// link to its state address — the response has to say so.
func TestImportStateWarnsOnRename(t *testing.T) {
	fake := &fakeRunner{providerSchemas: nullSchemas()}
	srv, _ := driftTestServer(t, fake)
	if err := srv.store.Update("p1", func(p *model.Project) error {
		p.Resources = []model.Resource{{ID: "r1", Type: "null_resource", Name: "one"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()

	code, resp := importState(t, h, "", twoResourceState)
	if *code != http.StatusOK {
		t.Fatalf("import = %d", *code)
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "null_resource.one") && strings.Contains(w, "no longer matches") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a rename warning, got %v", resp.Warnings)
	}
}
