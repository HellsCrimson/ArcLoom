import type { Field } from '$lib/types';

// defaultForField returns the initial literal value for a field's widget.
// Scalars start unset (undefined) so untouched optional attributes are dropped
// from the payload; containers start empty so their editors can bind safely.
export function defaultForField(f: Field): unknown {
	switch (f.widget) {
		case 'list':
		case 'block':
			return [];
		case 'map':
		case 'object':
			return {};
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
