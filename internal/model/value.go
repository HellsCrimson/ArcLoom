// Package model is the internal Infrastructure Model. It is pure data and must
// not depend on Terraform/OpenTofu libraries (except go-cty for value typing),
// so that the UI and generator layers can evolve independently of the CLI.
package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// SecretVarName maps a secret reference to the Terraform variable name it is
// exposed as (and the TF_VAR_<name> injected at run time).
func SecretVarName(ref string) string {
	return strings.NewReplacer("/", "_", "-", "_", ".", "_", " ", "_").Replace(ref)
}

// ValueMode describes how an attribute value is sourced.
type ValueMode string

const (
	// ModeLiteral is a concrete typed value entered by the user.
	ModeLiteral ValueMode = "literal"
	// ModeReference points at another resource's attribute, e.g.
	// "random_pet.name.id".
	ModeReference ValueMode = "reference"
	// ModeExpression is a raw HCL expression the user wrote by hand.
	ModeExpression ValueMode = "expression"
	// ModeSecret references a secret backend entry. Stubbed for slice 1: the
	// real value is never stored, only a placeholder reference.
	ModeSecret ValueMode = "secret"
)

// AttrValue is a single configurable value in the infrastructure model. Exactly
// one of the mode-specific fields is meaningful, selected by Mode.
//
// Collections (Mode == ModeLiteral) may be represented in one of two ways:
// either as a single self-contained cty.Value in Literal (the common
// all-literal form, e.g. an imported ["a", "b"] list), or — when an element is
// itself a reference/secret/expression — structurally via Items (list/set) or
// Entries (map/object), whose members are themselves AttrValues. This lets a
// reference or secret appear inside a collection without dropping the whole
// attribute to a raw expression.
type AttrValue struct {
	Mode ValueMode
	// Literal carries both value and type when Mode == ModeLiteral.
	Literal cty.Value
	// Ref is a dotted traversal ("type.name.attr") when Mode == ModeReference.
	Ref string
	// Expr is raw HCL when Mode == ModeExpression.
	Expr string
	// SecretRef names a secret backend entry when Mode == ModeSecret.
	SecretRef string
	// Items is a list/set/tuple literal whose elements are AttrValues. When
	// non-nil it takes precedence over Literal (Mode == ModeLiteral only).
	Items []AttrValue
	// Entries is a map/object literal whose values are AttrValues, ordered.
	// When non-nil it takes precedence over Literal (Mode == ModeLiteral only).
	Entries []AttrEntry
}

// AttrEntry is one key/value pair of a structural map/object literal.
type AttrEntry struct {
	Key   string
	Value AttrValue
}

// Walk invokes fn on this value and, recursively, on every AttrValue nested
// inside its Items/Entries.
func (a AttrValue) Walk(fn func(AttrValue)) {
	fn(a)
	for _, it := range a.Items {
		it.Walk(fn)
	}
	for _, e := range a.Entries {
		e.Value.Walk(fn)
	}
}

// attrWire is the serialized shape of an AttrValue. The literal is stored as a
// native JSON value (its cty type is re-implied on load), keeping both the API
// payload and the persisted YAML/JSON human-readable.
type attrWire struct {
	Mode      ValueMode        `json:"mode" yaml:"mode"`
	Literal   *json.RawMessage `json:"literal,omitempty" yaml:"-"`
	Ref       string           `json:"ref,omitempty" yaml:"ref,omitempty"`
	Expr      string           `json:"expr,omitempty" yaml:"expr,omitempty"`
	SecretRef string           `json:"secretRef,omitempty" yaml:"secretRef,omitempty"`
	Items     []AttrValue      `json:"items,omitempty" yaml:"-"`
	Entries   []attrEntryWire  `json:"entries,omitempty" yaml:"-"`
}

type attrEntryWire struct {
	Key   string    `json:"key" yaml:"key"`
	Value AttrValue `json:"value" yaml:"value"`
}

// ctyToNative converts a cty value to a plain Go value (via its JSON form) so
// generic encoders (json, yaml) can render it natively.
func ctyToNative(v cty.Value) (any, error) {
	if v == cty.NilVal {
		return nil, nil
	}
	raw, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return nil, err
	}
	var native any
	if err := json.Unmarshal(raw, &native); err != nil {
		return nil, err
	}
	return native, nil
}

// LiteralFromNative builds a literal AttrValue from a plain Go value (e.g. a
// decoded JSON/state attribute), implying its cty type.
func LiteralFromNative(native any) (AttrValue, error) {
	v, err := nativeToCty(native)
	if err != nil {
		return AttrValue{}, err
	}
	return AttrValue{Mode: ModeLiteral, Literal: v}, nil
}

// nativeToCty converts a plain Go value back into a cty value, implying the type
// from the JSON shape.
func nativeToCty(native any) (cty.Value, error) {
	raw, err := json.Marshal(native)
	if err != nil {
		return cty.NilVal, err
	}
	t, err := ctyjson.ImpliedType(raw)
	if err != nil {
		return cty.NilVal, err
	}
	return ctyjson.Unmarshal(raw, t)
}

// MarshalJSON renders the AttrValue with its literal as a native JSON value.
func (a AttrValue) MarshalJSON() ([]byte, error) {
	w := attrWire{Mode: a.Mode, Ref: a.Ref, Expr: a.Expr, SecretRef: a.SecretRef}
	if a.Mode == ModeLiteral {
		switch {
		case a.Items != nil:
			w.Items = a.Items
		case a.Entries != nil:
			w.Entries = make([]attrEntryWire, len(a.Entries))
			for i, e := range a.Entries {
				w.Entries[i] = attrEntryWire{Key: e.Key, Value: e.Value}
			}
		case a.Literal != cty.NilVal:
			native, err := ctyToNative(a.Literal)
			if err != nil {
				return nil, fmt.Errorf("marshal literal: %w", err)
			}
			raw, err := json.Marshal(native)
			if err != nil {
				return nil, err
			}
			rm := json.RawMessage(raw)
			w.Literal = &rm
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON restores an AttrValue, re-implying the literal's cty type.
func (a *AttrValue) UnmarshalJSON(data []byte) error {
	var w attrWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	a.Mode, a.Ref, a.Expr, a.SecretRef = w.Mode, w.Ref, w.Expr, w.SecretRef
	a.Literal, a.Items, a.Entries = cty.NilVal, nil, nil
	if w.Mode == ModeLiteral {
		switch {
		case w.Items != nil:
			a.Items = w.Items
		case w.Entries != nil:
			a.Entries = make([]AttrEntry, len(w.Entries))
			for i, e := range w.Entries {
				a.Entries[i] = AttrEntry{Key: e.Key, Value: e.Value}
			}
		case w.Literal != nil:
			var native any
			if err := json.Unmarshal(*w.Literal, &native); err != nil {
				return err
			}
			v, err := nativeToCty(native)
			if err != nil {
				return fmt.Errorf("unmarshal literal: %w", err)
			}
			a.Literal = v
		}
	}
	return nil
}

// MarshalYAML renders the AttrValue for the on-disk project config, with the
// literal as a native YAML value.
func (a AttrValue) MarshalYAML() (any, error) {
	out := map[string]any{"mode": string(a.Mode)}
	switch a.Mode {
	case ModeLiteral:
		switch {
		case a.Items != nil:
			out["items"] = a.Items
		case a.Entries != nil:
			es := make([]map[string]any, len(a.Entries))
			for i, e := range a.Entries {
				ev, err := e.Value.MarshalYAML()
				if err != nil {
					return nil, err
				}
				es[i] = map[string]any{"key": e.Key, "value": ev}
			}
			out["entries"] = es
		case a.Literal != cty.NilVal:
			native, err := ctyToNative(a.Literal)
			if err != nil {
				return nil, err
			}
			out["literal"] = native
		}
	case ModeReference:
		out["ref"] = a.Ref
	case ModeExpression:
		out["expr"] = a.Expr
	case ModeSecret:
		out["secretRef"] = a.SecretRef
	}
	return out, nil
}

// UnmarshalYAML restores an AttrValue from the on-disk project config.
func (a *AttrValue) UnmarshalYAML(unmarshal func(any) error) error {
	var raw struct {
		Mode      ValueMode   `yaml:"mode"`
		Literal   any         `yaml:"literal"`
		Ref       string      `yaml:"ref"`
		Expr      string      `yaml:"expr"`
		SecretRef string      `yaml:"secretRef"`
		Items     []AttrValue `yaml:"items"`
		Entries   []struct {
			Key   string    `yaml:"key"`
			Value AttrValue `yaml:"value"`
		} `yaml:"entries"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	a.Mode, a.Ref, a.Expr, a.SecretRef = raw.Mode, raw.Ref, raw.Expr, raw.SecretRef
	a.Literal, a.Items, a.Entries = cty.NilVal, nil, nil
	if raw.Mode == ModeLiteral {
		switch {
		case raw.Items != nil:
			a.Items = raw.Items
		case raw.Entries != nil:
			a.Entries = make([]AttrEntry, len(raw.Entries))
			for i, e := range raw.Entries {
				a.Entries[i] = AttrEntry{Key: e.Key, Value: e.Value}
			}
		case raw.Literal != nil:
			v, err := nativeToCty(raw.Literal)
			if err != nil {
				return fmt.Errorf("unmarshal literal: %w", err)
			}
			a.Literal = v
		}
	}
	return nil
}
