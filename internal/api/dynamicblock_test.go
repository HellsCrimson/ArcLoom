package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/zclconf/go-cty/cty"
)

// withDynamic is a resource carrying the nested blocks an import produces: a
// labelled `dynamic "ingress"` wrapping a `content` block.
func withDynamic() *model.Project {
	return &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		Providers: []model.ProviderInstance{{Key: "aws", Source: "hashicorp/aws"}},
		Resources: []model.Resource{{
			ID: "r1", Type: "aws_security_group", Name: "web",
			Attrs: map[string]model.AttrValue{
				"name": {Mode: model.ModeLiteral, Literal: cty.StringVal("web")},
			},
			Blocks: map[string][]model.Resource{
				"dynamic": {{
					Labels: []string{"ingress"},
					Attrs: map[string]model.AttrValue{
						"for_each": {Mode: model.ModeReference, Ref: "var.ports"},
					},
					Blocks: map[string][]model.Resource{
						"content": {{Attrs: map[string]model.AttrValue{
							"from_port": {Mode: model.ModeReference, Ref: "ingress.value"},
						}}},
					},
				}},
			},
		}},
	}
}

// The upsert replaces the whole resource, and the form does not model nested
// blocks — so before this, opening an imported resource and pressing Save
// silently deleted its dynamic/provisioner/lifecycle blocks. An absent "blocks"
// field means "keep the stored ones".
func TestUpsertResource_KeepsNestedBlocksWhenAbsent(t *testing.T) {
	h, st := newImportTestServer(t, withDynamic())

	// Exactly what the resource form sends: attrs + meta-args, no blocks.
	body := `{"id":"r1","type":"aws_security_group","name":"web",
		"attrs":{"name":{"mode":"literal","literal":"web2"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := st.Get("p1")
	dyn := got.Resources[0].Blocks["dynamic"]
	if len(dyn) != 1 {
		t.Fatalf("saving the form deleted the dynamic block: %+v", got.Resources[0].Blocks)
	}
	if len(dyn[0].Labels) != 1 || dyn[0].Labels[0] != "ingress" {
		t.Errorf("labels = %v, want [ingress]", dyn[0].Labels)
	}
	if len(dyn[0].Blocks["content"]) != 1 {
		t.Errorf("content block lost: %+v", dyn[0].Blocks)
	}
	// The edit itself still applied.
	if v := got.Resources[0].Attrs["name"].Literal.AsString(); v != "web2" {
		t.Errorf("attribute edit lost: name = %q", v)
	}
}

// A non-nil "blocks" REPLACES, so a client that does model them can edit and
// clear them. `{}` is the explicit clear.
func TestUpsertResource_ExplicitBlocksReplace(t *testing.T) {
	h, st := newImportTestServer(t, withDynamic())

	body := `{"id":"r1","type":"aws_security_group","name":"web","attrs":{},
		"blocks":{"lifecycle":[{"attrs":{"create_before_destroy":{"mode":"literal","literal":true}}}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := st.Get("p1")
	blocks := got.Resources[0].Blocks
	if len(blocks["dynamic"]) != 0 {
		t.Errorf("explicit blocks must replace, dynamic still present: %+v", blocks)
	}
	if len(blocks["lifecycle"]) != 1 {
		t.Fatalf("lifecycle not stored: %+v", blocks)
	}
	if !blocks["lifecycle"][0].Attrs["create_before_destroy"].Literal.True() {
		t.Error("nested attribute did not decode")
	}

	// And the explicit clear.
	body = `{"id":"r1","type":"aws_security_group","name":"web","attrs":{},"blocks":{}}`
	req = httptest.NewRequest(http.MethodPost, "/api/projects/p1/resources", strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if len(got.Resources[0].Blocks) != 0 {
		t.Errorf(`"blocks":{} must clear, got %+v`, got.Resources[0].Blocks)
	}
}

// The undo/redo path PUTs the whole model back, so nested blocks have to
// survive a decode of model.Project too — and the generation check that guards
// that endpoint must accept them.
func TestReplaceProject_KeepsNestedBlocks(t *testing.T) {
	h, st := newImportTestServer(t, withDynamic())

	body := `{"providers":[{"key":"aws","source":"hashicorp/aws"}],
		"resources":[{"id":"r1","type":"aws_security_group","name":"web","attrs":{},
		"blocks":{"dynamic":[{"labels":["ingress"],
			"attrs":{"for_each":{"mode":"reference","ref":"var.ports"}},
			"blocks":{"content":[{"attrs":{"from_port":{"mode":"reference","ref":"ingress.value"}}}]}}]}}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/projects/p1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("replace status %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := st.Get("p1")
	dyn := got.Resources[0].Blocks["dynamic"]
	if len(dyn) != 1 || len(dyn[0].Labels) != 1 || dyn[0].Labels[0] != "ingress" {
		t.Fatalf("undo dropped the dynamic block or its label: %+v", got.Resources[0].Blocks)
	}
	if len(dyn[0].Blocks["content"]) != 1 {
		t.Errorf("undo dropped the content block: %+v", dyn[0].Blocks)
	}
}

// A module call save must not wipe an imported `providers` mapping: the call
// form has no field for it, so an absent "providers" keeps the stored one. Same
// hazard as the nested blocks above — the upsert replaces the whole call.
func TestSetModuleCall_KeepsProvidersWhenAbsent(t *testing.T) {
	p := &model.Project{ID: "p1", Name: "x", Engine: model.EngineTofu,
		Providers:    []model.ProviderInstance{{Key: "opennebula", Source: "OpenNebula/opennebula"}},
		LocalModules: []model.LocalModule{{Name: "vms"}},
		Modules: []model.Module{{
			Name: "lab", Source: model.LocalSource("vms"),
			Providers: "{\n  opennebula = opennebula.lab\n}",
		}},
	}
	h, st := newImportTestServer(t, p)

	body := `{"name":"lab","source":"./modules/vms","config":{}}`
	req := httptest.NewRequest(http.MethodPut, "/api/projects/p1/modules/call", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ := st.Get("p1")
	if !strings.Contains(got.Modules[0].Providers, "opennebula.lab") {
		t.Fatalf("providers mapping wiped by a save: %q", got.Modules[0].Providers)
	}

	// An explicit "" clears it, going back to the derived mapping.
	body = `{"name":"lab","source":"./modules/vms","config":{},"providers":""}`
	req = httptest.NewRequest(http.MethodPut, "/api/projects/p1/modules/call", strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status %d: %s", rec.Code, rec.Body.String())
	}
	got, _ = st.Get("p1")
	if got.Modules[0].Providers != "" {
		t.Errorf(`explicit "" must clear, got %q`, got.Modules[0].Providers)
	}
}
