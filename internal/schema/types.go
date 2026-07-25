// Package schema normalizes provider schemas (as produced by
// `terraform providers schema -json`) into a provider-agnostic, widget-driven
// shape that the frontend renders without any Terraform-specific knowledge.
package schema

// WidgetKind is the UI control used to edit a field. The frontend switches on
// this value; it must never need to inspect a raw cty type.
type WidgetKind string

const (
	WidgetText     WidgetKind = "text"     // cty.String
	WidgetTextArea WidgetKind = "textarea" // long strings (heuristic)
	WidgetNumber   WidgetKind = "number"   // cty.Number
	WidgetCheckbox WidgetKind = "checkbox" // cty.Bool
	WidgetList     WidgetKind = "list"     // list/set/tuple -> repeatable rows
	WidgetMap      WidgetKind = "map"      // map(T) -> key/value editor
	WidgetObject   WidgetKind = "object"   // object / single nested -> nested form
	WidgetBlock    WidgetKind = "block"    // list/set nested block -> repeatable form
	// WidgetDynamic is a value whose type the provider does not declare
	// (cty.DynamicPseudoType — a framework DynamicAttribute). There are no child
	// fields to render, so the UI offers a free-form editor: the user builds the
	// object / list / scalar themselves, at any depth.
	WidgetDynamic WidgetKind = "dynamic"
)

// Field describes one editable (or read-only computed) value in a resource.
type Field struct {
	Name        string     `json:"name"`
	Widget      WidgetKind `json:"widget"`
	Type        string     `json:"type"` // human-readable cty type, e.g. "map(string)"
	Description string     `json:"description,omitempty"`
	Required    bool       `json:"required"`
	Optional    bool       `json:"optional"`
	Computed    bool       `json:"computed"`
	Sensitive   bool       `json:"sensitive"`
	Deprecated  bool       `json:"deprecated,omitempty"`
	MinItems    int        `json:"minItems,omitempty"`
	MaxItems    int        `json:"maxItems,omitempty"`
	// Elem is the element field for list/set/map widgets.
	Elem *Field `json:"elem,omitempty"`
	// Fields are the child fields for object/block widgets.
	Fields []Field `json:"fields,omitempty"`
}

// ReadOnly reports whether the field is computed-only (no user input). Such
// fields are shown for reference but must never be sent to the CLI as inputs.
func (f Field) ReadOnly() bool {
	return f.Computed && !f.Required && !f.Optional
}

// NormalizedSchema is the full contract sent to the frontend for one resource
// type. The frontend mirrors these structs in TypeScript.
type NormalizedSchema struct {
	Provider     string  `json:"provider"`     // "hashicorp/random"
	ResourceType string  `json:"resourceType"` // "random_password"
	Version      int64   `json:"version"`      // schema version
	Fields       []Field `json:"fields"`       // required first, then optional, computed-only last
}
