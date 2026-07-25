import type { AttrValue, Field } from '$lib/types';

// defaultForField returns the initial literal value for a scalar field's widget.
// Scalars start unset (undefined) so untouched optional attributes are dropped
// from the payload.
export function defaultForField(f: Field): unknown {
	switch (f.widget) {
		case 'number':
		case 'checkbox':
			return undefined;
		default:
			return '';
	}
}

// objectElem synthesizes an object-typed element field for a repeatable block,
// so a `block` widget can be edited as a list whose rows are nested forms.
export function objectElem(f: Field): Field {
	return {
		name: '',
		widget: 'object',
		type: 'object',
		fields: f.fields ?? [],
		required: false,
		optional: false,
		computed: false,
		sensitive: false
	};
}

// isContainer reports whether a widget holds nested values (list/map/object,
// or a dynamic value the user may shape into either).
export function isContainer(f: Field): boolean {
	return (
		f.widget === 'list' ||
		f.widget === 'block' ||
		f.widget === 'map' ||
		f.widget === 'object' ||
		f.widget === 'dynamic'
	);
}

// elemField returns the element field of a list/set/block (the type of each
// item), synthesizing a text element when the schema left it implicit.
export function elemField(field: Field): Field {
	if (field.widget === 'block') return objectElem(field);
	return field.elem ?? ({ ...field, widget: 'text' } as Field);
}

// rawToAttr wraps a legacy raw JS value (from a pre-existing literal collection)
// into an AttrValue for the given field, recursing into container structure so
// old projects load into the mode-aware editors.
export function rawToAttr(field: Field, raw: unknown): AttrValue {
	const av: AttrValue = { mode: 'literal' };
	if (field.widget === 'list' || field.widget === 'block') {
		const arr = Array.isArray(raw) ? raw : [];
		av.items = arr.map((x) => rawToAttr(elemField(field), x));
	} else if (field.widget === 'map') {
		const obj = isPlainObject(raw) ? raw : {};
		av.entries = Object.entries(obj).map(([k, v]) => ({ key: k, value: rawToAttr(elemField(field), v) }));
	} else if (field.widget === 'object') {
		const obj = isPlainObject(raw) ? raw : {};
		av.entries = (field.fields ?? []).map((c) => ({ key: c.name, value: rawToAttr(c, obj[c.name]) }));
	} else if (field.widget === 'dynamic') {
		return rawToDynamic(raw);
	} else {
		av.literal = raw;
	}
	return av;
}

// rawToDynamic converts a raw JS value into the structural AttrValue shape, with
// the layout taken from the VALUE (a dynamic attribute has no schema to consult).
// This is what turns an imported `config = { events = { … } }` — stored as one
// opaque literal object — into something the editor can walk.
export function rawToDynamic(raw: unknown): AttrValue {
	if (Array.isArray(raw)) {
		return { mode: 'literal', items: raw.map(rawToDynamic) };
	}
	if (isPlainObject(raw)) {
		return {
			mode: 'literal',
			entries: Object.entries(raw).map(([k, v]) => ({ key: k, value: rawToDynamic(v) }))
		};
	}
	return { mode: 'literal', literal: raw === undefined || raw === null ? '' : raw };
}

// ensureLiteralShape makes a literal-mode container AttrValue editable by the
// structural editors: it seeds `items`/`entries` (from a legacy `literal` value
// or empty), then clears the now-redundant `literal`. Idempotent.
//
// It reseeds from a non-empty legacy `literal` even when the structural array is
// already an empty [] — a child editor may default the slot to [] before this
// runs, and dropping the legacy data then would silently lose it.
export function ensureLiteralShape(field: Field, av: AttrValue): void {
	if (av.mode !== 'literal') return;
	if (field.widget === 'list' || field.widget === 'block') {
		const legacy = Array.isArray(av.literal) ? av.literal : [];
		if (!av.items || (av.items.length === 0 && legacy.length > 0)) {
			av.items = legacy.map((x) => rawToAttr(elemField(field), x));
		}
		av.literal = undefined;
	} else if (field.widget === 'map') {
		const legacy = isPlainObject(av.literal) ? av.literal : {};
		if (!av.entries || (av.entries.length === 0 && Object.keys(legacy).length > 0)) {
			av.entries = Object.entries(legacy).map(([k, v]) => ({ key: k, value: rawToAttr(elemField(field), v) }));
		}
		av.literal = undefined;
	} else if (field.widget === 'object') {
		const legacy = isPlainObject(av.literal) ? av.literal : {};
		if (!av.entries || (av.entries.length === 0 && Object.keys(legacy).length > 0)) {
			av.entries = (field.fields ?? []).map((c) => ({ key: c.name, value: rawToAttr(c, legacy[c.name]) }));
		}
		av.literal = undefined;
	} else if (field.widget === 'dynamic') {
		// A dynamic value keeps whichever shape it already has; only a legacy raw
		// object/array literal needs lifting into the structural form (that is the
		// value that used to render as "[object Object]" in a text input).
		if (av.items === undefined && av.entries === undefined) {
			if (Array.isArray(av.literal) || isPlainObject(av.literal)) {
				const lifted = rawToDynamic(av.literal);
				av.items = lifted.items;
				av.entries = lifted.entries;
				av.literal = undefined;
			}
		}
	}
}

// defaultAttr builds a fresh literal AttrValue for a field, seeding container
// shape so nested editors can bind immediately.
export function defaultAttr(field: Field): AttrValue {
	const av: AttrValue = { mode: 'literal' };
	ensureLiteralShape(field, av);
	return av;
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
	return typeof v === 'object' && v !== null && !Array.isArray(v);
}
