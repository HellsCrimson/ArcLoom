package schema

import (
	"sort"

	tfjson "github.com/hashicorp/terraform-json"
)

// Normalize converts a tfjson resource schema into the provider-agnostic
// NormalizedSchema sent to the UI. provider is the fully-qualified provider
// address (e.g. "registry.terraform.io/hashicorp/random") and resourceType is
// the resource name (e.g. "random_password").
func Normalize(provider, resourceType string, s *tfjson.Schema) NormalizedSchema {
	ns := NormalizedSchema{
		Provider:     provider,
		ResourceType: resourceType,
	}
	if s == nil {
		return ns
	}
	ns.Version = int64(s.Version)
	if s.Block != nil {
		ns.Fields = blockFields(s.Block)
	}
	return ns
}

// blockFields flattens a schema block's attributes and nested blocks into a
// single ordered slice: required first, then optional, then computed-only.
func blockFields(b *tfjson.SchemaBlock) []Field {
	var fields []Field

	// Attributes (handles both bare types and modern nested_type attributes).
	for name, attr := range b.Attributes {
		fields = append(fields, attributeField(name, attr))
	}

	// Legacy nested blocks (block_types).
	for name, bt := range b.NestedBlocks {
		fields = append(fields, blockTypeField(name, bt))
	}

	sortFields(fields)
	return fields
}

// attributeField converts a single schema attribute into a Field.
func attributeField(name string, attr *tfjson.SchemaAttribute) Field {
	var f Field

	if attr.AttributeNestedType != nil {
		// Modern nested attribute type: object / repeatable object / map of
		// objects depending on nesting mode.
		f = nestedAttrField(name, attr.AttributeNestedType)
	} else {
		f = fieldFromType(name, attr.AttributeType)
	}

	f.Name = name
	f.Description = attr.Description
	f.Required = attr.Required
	f.Optional = attr.Optional
	f.Computed = attr.Computed
	f.Sensitive = attr.Sensitive
	f.Deprecated = attr.Deprecated
	return f
}

// nestedAttrField handles SchemaAttribute.AttributeNestedType (the modern way
// providers express nested objects on attributes).
func nestedAttrField(name string, nt *tfjson.SchemaNestedAttributeType) Field {
	children := make([]Field, 0, len(nt.Attributes))
	for cn, ca := range nt.Attributes {
		children = append(children, attributeField(cn, ca))
	}
	sortFields(children)

	f := Field{Name: name, Fields: children}
	switch nt.NestingMode {
	case tfjson.SchemaNestingModeSingle, tfjson.SchemaNestingModeGroup:
		f.Widget = WidgetObject
		f.Type = "object"
	case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
		f.Widget = WidgetBlock
		f.Type = "list(object)"
	case tfjson.SchemaNestingModeMap:
		f.Widget = WidgetMap
		f.Type = "map(object)"
		elem := Field{Widget: WidgetObject, Type: "object", Fields: children}
		f.Elem = &elem
		f.Fields = nil
	default:
		f.Widget = WidgetObject
		f.Type = "object"
	}
	return f
}

// blockTypeField handles legacy nested blocks (SchemaBlockType / block_types).
func blockTypeField(name string, bt *tfjson.SchemaBlockType) Field {
	f := Field{Name: name, MinItems: int(bt.MinItems), MaxItems: int(bt.MaxItems)}
	if bt.Block != nil {
		f.Fields = blockFields(bt.Block)
		f.Description = bt.Block.Description
		f.Deprecated = bt.Block.Deprecated
	}
	switch bt.NestingMode {
	case tfjson.SchemaNestingModeSingle, tfjson.SchemaNestingModeGroup:
		f.Widget = WidgetObject
		f.Type = "object"
	case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
		f.Widget = WidgetBlock
		f.Type = "list(block)"
	case tfjson.SchemaNestingModeMap:
		f.Widget = WidgetMap
		f.Type = "map(block)"
	default:
		f.Widget = WidgetBlock
		f.Type = "list(block)"
	}
	// A nested block is always user-supplied configuration.
	f.Optional = f.MinItems == 0
	f.Required = f.MinItems > 0
	return f
}

// sortFields orders fields for display: required, then optional, then
// computed-only; alphabetical within each group.
func sortFields(fields []Field) {
	rank := func(f Field) int {
		switch {
		case f.Required:
			return 0
		case f.Optional:
			return 1
		default: // computed-only
			return 2
		}
	}
	sort.SliceStable(fields, func(i, j int) bool {
		ri, rj := rank(fields[i]), rank(fields[j])
		if ri != rj {
			return ri < rj
		}
		return fields[i].Name < fields[j].Name
	})
}
