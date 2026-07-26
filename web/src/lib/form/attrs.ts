import type { AttrValue } from '$lib/types';

// Save-path helpers for schema-form values: the inverse of defaults.ts. What
// survives cleanAttr is exactly what the API receives, so the pruning rules
// here are the form's real contract.

// cleanAttr prunes an AttrValue to what should be sent: unset scalars, empty
// collections, and incomplete references/secrets/expressions collapse to
// undefined so they're dropped. Recurses into structural collections so a
// nested reference/secret is preserved but an empty nested slot is not.
// cleanAttr prunes the untouched parts of a form value. `keyed` marks a value
// the user deliberately created a key for (an object/map entry): an empty
// string there is a real value — `avatarUrl = ""` in a provider's config
// object means something — so only unkeyed blanks are dropped.
export function cleanAttr(v: AttrValue, keyed = false): AttrValue | undefined {
	if (v.mode === 'reference') return v.ref ? { mode: 'reference', ref: v.ref } : undefined;
	if (v.mode === 'expression') return v.expr ? { mode: 'expression', expr: v.expr } : undefined;
	if (v.mode === 'secret') return v.secretRef ? { mode: 'secret', secretRef: v.secretRef } : undefined;
	// literal
	// An empty collection carries nothing and cannot round-trip (an empty slice
	// is omitted on the wire, so `{}` would come back as an untyped scalar) —
	// prune it like any other untouched row.
	if (v.items !== undefined) {
		const items = v.items.map((it) => cleanAttr(it)).filter((x): x is AttrValue => x !== undefined);
		return items.length ? { mode: 'literal', items } : undefined;
	}
	if (v.entries !== undefined) {
		const entries = v.entries
			.map((e) => ({ key: e.key.trim(), value: cleanAttr(e.value, true) }))
			.filter((e): e is { key: string; value: AttrValue } => e.key !== '' && e.value !== undefined);
		return entries.length ? { mode: 'literal', entries } : undefined;
	}
	const lit = v.literal;
	if (lit === undefined || lit === null || (lit === '' && !keyed)) return undefined;
	return { mode: 'literal', literal: lit };
}

export function buildAttrs(values: Record<string, AttrValue>): Record<string, AttrValue> {
	const out: Record<string, AttrValue> = {};
	for (const [name, v] of Object.entries(values)) {
		const cleaned = cleanAttr(v);
		if (cleaned) out[name] = cleaned;
	}
	return out;
}

// attrToText renders an AttrValue as editable raw-expression text (a
// reference or JSON literal is also a valid HCL expression).
export function attrToText(av: AttrValue | undefined): string {
	if (!av) return '';
	return av.mode === 'expression' ? (av.expr ?? '') : av.mode === 'reference' ? (av.ref ?? '') : av.mode === 'literal' ? JSON.stringify(av.literal ?? '') : '';
}

// rowsToAttrs turns the raw key/expression rows the registry-module and backend
// modals edit into expression AttrValues. Blank rows are dropped.
export function rowsToAttrs(rows: { key: string; expr: string }[]): Record<string, AttrValue> {
	const config: Record<string, AttrValue> = {};
	for (const row of rows) {
		const k = row.key.trim();
		const e = row.expr.trim();
		if (k && e) config[k] = { mode: 'expression', expr: e };
	}
	return config;
}
