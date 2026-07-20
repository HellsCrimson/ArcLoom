package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// mixedList is a list literal mixing a plain literal, a reference and a secret.
func mixedList() AttrValue {
	return AttrValue{
		Mode: ModeLiteral,
		Items: []AttrValue{
			{Mode: ModeLiteral, Literal: cty.StringVal("a")},
			{Mode: ModeReference, Ref: "aws_vpc.main.id"},
			{Mode: ModeSecret, SecretRef: "tok"},
		},
	}
}

// mixedObject is an object literal mixing a literal and a reference value.
func mixedObject() AttrValue {
	return AttrValue{
		Mode: ModeLiteral,
		Entries: []AttrEntry{
			{Key: "Name", Value: AttrValue{Mode: ModeLiteral, Literal: cty.StringVal("web")}},
			{Key: "Zone", Value: AttrValue{Mode: ModeReference, Ref: "aws_vpc.main.id"}},
		},
	}
}

func TestAttrValue_JSONRoundTrip_NestedItems(t *testing.T) {
	in := mixedList()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out AttrValue
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("JSON round-trip mismatch:\n in: %#v\nout: %#v\nwire: %s", in, out, data)
	}
}

func TestAttrValue_JSONRoundTrip_NestedEntries(t *testing.T) {
	in := mixedObject()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out AttrValue
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("JSON round-trip mismatch:\n in: %#v\nout: %#v\nwire: %s", in, out, data)
	}
}

func TestAttrValue_YAMLRoundTrip_Nested(t *testing.T) {
	for name, in := range map[string]AttrValue{"list": mixedList(), "object": mixedObject()} {
		data, err := yaml.Marshal(in)
		if err != nil {
			t.Fatalf("%s marshal: %v", name, err)
		}
		var out AttrValue
		if err := yaml.Unmarshal(data, &out); err != nil {
			t.Fatalf("%s unmarshal: %v", name, err)
		}
		if !reflect.DeepEqual(in, out) {
			t.Errorf("%s YAML round-trip mismatch:\n in: %#v\nout: %#v\nyaml:\n%s", name, in, out, data)
		}
	}
}

// A plain (all-literal) collection stays in the Literal cty form and must be
// untouched by the new Items/Entries paths.
func TestAttrValue_JSONRoundTrip_PlainLiteralList(t *testing.T) {
	in := AttrValue{Mode: ModeLiteral, Literal: cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out AttrValue
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Items != nil || out.Entries != nil {
		t.Errorf("plain literal list gained Items/Entries: %#v", out)
	}
	// The native JSON round-trip implies a tuple type for a homogeneous array
	// (pre-existing behavior); the elements must still match.
	if got, want := out.Literal.LengthInt(), in.Literal.LengthInt(); got != want {
		t.Fatalf("plain literal length mismatch: got %d want %d", got, want)
	}
	if !out.Literal.Index(cty.NumberIntVal(0)).RawEquals(cty.StringVal("a")) {
		t.Errorf("plain literal element mismatch: %#v", out.Literal)
	}
}
