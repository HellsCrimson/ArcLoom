import type { Field, WidgetKind } from '$lib/types';

// Parses an HCL *type constraint* — the `type = …` of a variable — into the same
// `Field` shape the provider-schema pipeline produces, so a variable's default
// can be edited by the very same widget-driven form as a resource attribute.
//
// Until this existed, `Variable.Type` was stored and emitted verbatim and read
// by nothing: a `map(object({ name = string, size = number }))` had to be typed
// as raw HCL, or shaped by hand in the value-derived DynamicEditor (which cannot
// know a new entry is supposed to have `name` and `size`).
//
// Grammar (terraform.io "Type Constraints"):
//   primitive := string | number | bool | any
//   collection := list(T) | set(T) | map(T)
//   structural := object({ k = T, … }) | tuple([T, …])
//   modifier  := optional(T) | optional(T, default)   (object attributes only)
//
// Anything unrecognised yields null and the caller falls back to the free-form
// editor — a wrong guess would be worse than no guess.

class Cursor {
	constructor(
		readonly s: string,
		public i = 0
	) {}
	ws() {
		while (this.i < this.s.length && /\s/.test(this.s[this.i])) this.i++;
	}
	peek(): string {
		this.ws();
		return this.s[this.i] ?? '';
	}
	eat(ch: string): boolean {
		if (this.peek() === ch) {
			this.i++;
			return true;
		}
		return false;
	}
	ident(): string {
		this.ws();
		const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(this.s.slice(this.i));
		if (!m) return '';
		this.i += m[0].length;
		return m[0];
	}
	/** An object attribute name: bare identifier or quoted string. */
	key(): string {
		this.ws();
		const q = this.s[this.i];
		if (q === '"' || q === "'") {
			const end = this.s.indexOf(q, this.i + 1);
			if (end < 0) return '';
			const out = this.s.slice(this.i + 1, end);
			this.i = end + 1;
			return out;
		}
		return this.ident();
	}
	/** Skips a balanced expression — used to discard an `optional()` default. */
	skipValue() {
		let depth = 0;
		while (this.i < this.s.length) {
			const c = this.s[this.i];
			if ('([{'.includes(c)) depth++;
			else if (')]}'.includes(c)) {
				if (depth === 0) return;
				depth--;
			} else if (c === ',' && depth === 0) return;
			this.i++;
		}
	}
}

function field(widget: WidgetKind, type: string, extra: Partial<Field> = {}): Field {
	return {
		name: '',
		widget,
		type,
		required: false,
		optional: true,
		computed: false,
		sensitive: false,
		...extra
	};
}

const PRIMITIVES: Record<string, Field | undefined> = {
	string: field('text', 'string'),
	number: field('number', 'number'),
	bool: field('checkbox', 'bool'),
	// `any` is exactly the case DynamicEditor exists for: shape comes from value.
	any: field('dynamic', 'dynamic')
};

function parseType(c: Cursor): Field | null {
	const name = c.ident();
	if (!name) return null;

	if (c.peek() !== '(') {
		const prim = PRIMITIVES[name];
		return prim ? { ...prim } : null;
	}
	c.eat('(');

	let out: Field | null = null;
	switch (name) {
		case 'list':
		case 'set': {
			const elem = parseType(c);
			if (!elem) return null;
			out = field('list', name === 'set' ? 'set' : 'list', { elem });
			break;
		}
		case 'map': {
			const elem = parseType(c);
			if (!elem) return null;
			out = field('map', 'map', { elem });
			break;
		}
		case 'object': {
			if (!c.eat('{')) return null;
			const fields: Field[] = [];
			while (c.peek() !== '}' && c.peek() !== '') {
				const key = c.key();
				if (!key) return null;
				// `name = string` is the documented form; `name: string` also parses.
				if (!c.eat('=') && !c.eat(':')) return null;
				const ft = parseType(c);
				if (!ft) return null;
				fields.push({ ...ft, name: key });
				// Attributes are separated by a comma or simply a newline.
				c.eat(',');
			}
			if (!c.eat('}')) return null;
			out = field('object', 'object', { fields });
			break;
		}
		case 'tuple': {
			if (!c.eat('[')) return null;
			// A tuple is positionally typed; the editors have no notion of that, so
			// it degrades to a list of free-form values rather than claiming a type.
			while (c.peek() !== ']' && c.peek() !== '') {
				if (!parseType(c)) return null;
				c.eat(',');
			}
			if (!c.eat(']')) return null;
			out = field('list', 'tuple', { elem: field('dynamic', 'dynamic') });
			break;
		}
		case 'optional': {
			const inner = parseType(c);
			if (!inner) return null;
			// optional(T, <default>) — the default is not part of the shape.
			if (c.eat(',')) c.skipValue();
			out = { ...inner, required: false, optional: true };
			break;
		}
		default:
			return null;
	}
	if (!c.eat(')')) return null;
	return out;
}

/**
 * Parses an HCL type constraint into a Field, or null when it is empty,
 * malformed, or uses a construct the form cannot render.
 */
export function parseHclType(type: string | undefined, name = ''): Field | null {
	const src = (type ?? '').trim();
	if (!src) return null;
	const c = new Cursor(src);
	const f = parseType(c);
	if (!f) return null;
	c.ws();
	// Trailing junk means we mis-parsed; do not pretend otherwise.
	if (c.i !== src.length) return null;
	return { ...f, name };
}
