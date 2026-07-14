package state

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/matthiasrauline/arcloom/internal/model"
)

const sampleState = `{
  "version": 4,
  "resources": [
    {
      "mode": "managed",
      "type": "random_password",
      "name": "db",
      "provider": "provider[\"registry.opentofu.org/hashicorp/random\"]",
      "instances": [
        { "attributes": { "id": "abc", "length": 32, "special": true, "result": "SECRET" } }
      ]
    },
    {
      "mode": "data",
      "type": "http",
      "name": "x",
      "provider": "provider[\"registry.terraform.io/hashicorp/http\"]",
      "instances": [ { "attributes": {} } ]
    }
  ]
}`

func TestParse_ManagedOnly(t *testing.T) {
	res, warnings, err := Parse([]byte(sampleState))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 managed resource, got %d", len(res))
	}
	if res[0].Type != "random_password" || res[0].Name != "db" {
		t.Errorf("wrong resource: %+v", res[0])
	}
	if len(warnings) == 0 {
		t.Error("expected a warning for the skipped data resource")
	}
}

func TestSettableAttrs_DropsComputedOnly(t *testing.T) {
	// Schema: length/special settable; id/result computed-only.
	rs := &tfjson.Schema{
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"length":  {AttributeType: cty.Number, Required: true},
				"special": {AttributeType: cty.Bool, Optional: true},
				"id":      {AttributeType: cty.String, Computed: true},
				"result":  {AttributeType: cty.String, Computed: true, Sensitive: true},
			},
		},
	}
	attrs := map[string]any{
		"length":  float64(32),
		"special": true,
		"id":      "abc",
		"result":  "SECRET",
	}
	out, _ := SettableAttrs(rs, attrs)

	if _, ok := out["length"]; !ok {
		t.Error("length should be imported")
	}
	if _, ok := out["special"]; !ok {
		t.Error("special should be imported")
	}
	if _, ok := out["id"]; ok {
		t.Error("id (computed-only) must NOT be imported")
	}
	if _, ok := out["result"]; ok {
		t.Error("result (computed/sensitive) must NOT be imported")
	}
	if v := out["length"]; v.Mode != model.ModeLiteral {
		t.Errorf("length should be a literal, got %s", v.Mode)
	}
}
