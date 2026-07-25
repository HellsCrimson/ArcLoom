package schema

import (
	"regexp"
	"sort"

	"github.com/zclconf/go-cty/cty"
)

// textareaHint matches attribute names that usually hold long/multiline text,
// so we render them as a textarea rather than a single-line input.
var textareaHint = regexp.MustCompile(`(?i)(body|content|policy|json|template|script|document|config)`)

// fieldFromType builds a Field for a bare cty type (no schema metadata). It is
// used for element/child fields inside lists, maps, and objects, and recurses
// through complex types. name may be empty for anonymous element types.
func fieldFromType(name string, t cty.Type) Field {
	f := Field{Name: name}

	// cty.NilType has no FriendlyName (it panics), so handle it up front.
	if t == cty.NilType {
		f.Widget = WidgetDynamic
		f.Type = "dynamic"
		return f
	}

	f.Type = t.FriendlyName()

	switch {
	case t.Equals(cty.DynamicPseudoType):
		// The provider declares no type at all, so there is nothing to derive
		// child fields from: the UI renders a free-form editor where the value's
		// shape (object / list / scalar) is chosen per node, at any depth.
		f.Widget = WidgetDynamic

	case t.IsPrimitiveType():
		switch {
		case t.Equals(cty.String):
			f.Widget = WidgetText
			if textareaHint.MatchString(name) {
				f.Widget = WidgetTextArea
			}
		case t.Equals(cty.Number):
			f.Widget = WidgetNumber
		case t.Equals(cty.Bool):
			f.Widget = WidgetCheckbox
		default:
			f.Widget = WidgetText
		}

	case t.IsListType() || t.IsSetType():
		f.Widget = WidgetList
		elem := fieldFromType("", t.ElementType())
		f.Elem = &elem

	case t.IsMapType():
		f.Widget = WidgetMap
		elem := fieldFromType("", t.ElementType())
		f.Elem = &elem

	case t.IsObjectType():
		f.Widget = WidgetObject
		f.Fields = objectFields(t.AttributeTypes())

	case t.IsTupleType():
		// Fixed-length heterogeneous list: model as a list with a per-index
		// field set and pin the length via min/max items.
		f.Widget = WidgetList
		ets := t.TupleElementTypes()
		f.MinItems = len(ets)
		f.MaxItems = len(ets)
		for i, et := range ets {
			f.Fields = append(f.Fields, fieldFromType("", et))
			_ = i
		}

	default:
		f.Widget = WidgetText
	}

	return f
}

// objectFields converts a cty object's attribute map into sorted Fields.
func objectFields(attrs map[string]cty.Type) []Field {
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)

	fields := make([]Field, 0, len(attrs))
	for _, n := range names {
		child := fieldFromType(n, attrs[n])
		child.Optional = true // object attributes are optional unless known otherwise
		fields = append(fields, child)
	}
	return fields
}
