package schema

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

func fieldByName(fields []Field, name string) (Field, bool) {
	for _, f := range fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

func TestFieldFromType_Primitives(t *testing.T) {
	cases := map[cty.Type]WidgetKind{
		cty.String: WidgetText,
		cty.Number: WidgetNumber,
		cty.Bool:   WidgetCheckbox,
	}
	for typ, want := range cases {
		if got := fieldFromType("x", typ).Widget; got != want {
			t.Errorf("%s: got widget %q, want %q", typ.FriendlyName(), got, want)
		}
	}
}

func TestFieldFromType_Collections(t *testing.T) {
	list := fieldFromType("tags", cty.List(cty.String))
	if list.Widget != WidgetList || list.Elem == nil || list.Elem.Widget != WidgetText {
		t.Errorf("list(string): got %+v", list)
	}
	m := fieldFromType("labels", cty.Map(cty.String))
	if m.Widget != WidgetMap || m.Elem == nil || m.Elem.Widget != WidgetText {
		t.Errorf("map(string): got %+v", m)
	}
	set := fieldFromType("s", cty.Set(cty.Number))
	if set.Widget != WidgetList || set.Elem == nil || set.Elem.Widget != WidgetNumber {
		t.Errorf("set(number): got %+v", set)
	}
}

func TestFieldFromType_Object(t *testing.T) {
	obj := fieldFromType("cfg", cty.Object(map[string]cty.Type{
		"name":    cty.String,
		"enabled": cty.Bool,
	}))
	if obj.Widget != WidgetObject {
		t.Fatalf("want object widget, got %q", obj.Widget)
	}
	if _, ok := fieldByName(obj.Fields, "name"); !ok {
		t.Error("missing nested field 'name'")
	}
	if f, ok := fieldByName(obj.Fields, "enabled"); !ok || f.Widget != WidgetCheckbox {
		t.Error("nested field 'enabled' not a checkbox")
	}
}

func TestFieldFromType_TextareaHeuristic(t *testing.T) {
	if got := fieldFromType("policy_json", cty.String).Widget; got != WidgetTextArea {
		t.Errorf("expected textarea for policy_json, got %q", got)
	}
}

func TestFieldFromType_Unknown(t *testing.T) {
	// Dynamic/unknown types must fall back to text, never panic.
	if got := fieldFromType("x", cty.DynamicPseudoType).Widget; got != WidgetText {
		t.Errorf("dynamic: got %q, want text", got)
	}
	if got := fieldFromType("x", cty.NilType).Widget; got != WidgetText {
		t.Errorf("nil type: got %q, want text", got)
	}
}

// TestNormalize_LegacyBlockTypes covers the legacy block_types nesting mechanism.
func TestNormalize_LegacyBlockTypes(t *testing.T) {
	s := &tfjson.Schema{
		Version: 1,
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"name": {AttributeType: cty.String, Required: true},
			},
			NestedBlocks: map[string]*tfjson.SchemaBlockType{
				"rule": {
					NestingMode: tfjson.SchemaNestingModeList,
					MinItems:    1,
					MaxItems:    5,
					Block: &tfjson.SchemaBlock{
						Attributes: map[string]*tfjson.SchemaAttribute{
							"action": {AttributeType: cty.String, Optional: true},
						},
					},
				},
			},
		},
	}
	ns := Normalize("registry/hashicorp/x", "x_thing", s)
	rule, ok := fieldByName(ns.Fields, "rule")
	if !ok {
		t.Fatal("legacy nested block 'rule' dropped")
	}
	if rule.Widget != WidgetBlock {
		t.Errorf("rule widget: got %q, want block", rule.Widget)
	}
	if rule.MinItems != 1 || rule.MaxItems != 5 {
		t.Errorf("rule min/max: got %d/%d", rule.MinItems, rule.MaxItems)
	}
	if _, ok := fieldByName(rule.Fields, "action"); !ok {
		t.Error("nested block child 'action' missing")
	}
}

// TestNormalize_NestedType covers the modern nested_type nesting mechanism.
func TestNormalize_NestedType(t *testing.T) {
	s := &tfjson.Schema{
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"filter": {
					Optional: true,
					AttributeNestedType: &tfjson.SchemaNestedAttributeType{
						NestingMode: tfjson.SchemaNestingModeSingle,
						Attributes: map[string]*tfjson.SchemaAttribute{
							"key":   {AttributeType: cty.String, Required: true},
							"value": {AttributeType: cty.String, Optional: true},
						},
					},
				},
			},
		},
	}
	ns := Normalize("registry/hashicorp/x", "x_thing", s)
	filter, ok := fieldByName(ns.Fields, "filter")
	if !ok {
		t.Fatal("modern nested_type attribute 'filter' dropped")
	}
	if filter.Widget != WidgetObject {
		t.Errorf("filter widget: got %q, want object", filter.Widget)
	}
	if len(filter.Fields) != 2 {
		t.Errorf("filter should have 2 child fields, got %d", len(filter.Fields))
	}
}

// TestNormalize_Ordering verifies required < optional < computed-only.
func TestNormalize_Ordering(t *testing.T) {
	s := &tfjson.Schema{
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"zzz_required":  {AttributeType: cty.String, Required: true},
				"aaa_optional":  {AttributeType: cty.String, Optional: true},
				"mmm_computed":  {AttributeType: cty.String, Computed: true},
			},
		},
	}
	ns := Normalize("p", "x", s)
	if len(ns.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(ns.Fields))
	}
	if ns.Fields[0].Name != "zzz_required" || ns.Fields[1].Name != "aaa_optional" || ns.Fields[2].Name != "mmm_computed" {
		t.Errorf("bad ordering: %s, %s, %s", ns.Fields[0].Name, ns.Fields[1].Name, ns.Fields[2].Name)
	}
	if !ns.Fields[2].ReadOnly() {
		t.Error("computed-only field should be ReadOnly")
	}
}
