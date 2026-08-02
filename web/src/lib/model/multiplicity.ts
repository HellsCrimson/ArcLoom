// Multiplicity badge for count / for_each.
//
// A resource or module call carrying count/for_each expands to N instances at
// apply time, but the structure tree and the canvas both list ONE entry for it
// — so without an explicit badge the UI silently under-reports how much
// infrastructure a project describes. This is the single source of that badge,
// shared by the canvas cards/module boxes and the structure sidebar so both
// surfaces read identically.
//
// The count is read from the CONFIG, so it is only a real number when the
// expression is one. Two cases produce one: a literal `count`, and a `for_each`
// whose collection expand.ts can evaluate here (a literal collection, or one
// hop through a `var.*` default / `local.*` value) — the same resolution the
// structure tree already does for module-call instances. Everything else is
// the engine's to work out, hence ×N / ∀ and the pointer to the State panel:
// the plan lists instances too, but only those that change.

import { expandInstances, type Scope } from './expand';

export const EXPANSION_HINT = '\nOpen State to list the instances this expanded to.';

export type MultiBadge = { label: string; title: string };

/** Anything with the count/for_each meta-args: a resource, a data source, a
 *  local-module call or a registry module call. */
export type Multiplied = { count?: string; forEach?: string };

/** How many instance keys the tooltip names before collapsing the tail. */
const KEYS_IN_TITLE = 12;

/**
 * multiBadge renders the ×N / ∀ pill for a block's meta-args. `scope` is the
 * declarations a `var.*` / `local.*` in a for_each may refer to (the root's for
 * a root-scope block, the module's inside one); omitting it just means every
 * for_each stays ∀.
 */
export function multiBadge(r: Multiplied, scope?: Scope): MultiBadge | undefined {
	if (r.count) {
		const literal = /^\d+$/.test(r.count.trim());
		const n = literal ? r.count.trim() : 'N';
		return { label: `×${n}`, title: `count = ${r.count}` + (literal ? '' : EXPANSION_HINT) };
	}
	if (!r.forEach) return undefined;
	// Uncapped on purpose: MAX_INSTANCES exists to stop a huge collection from
	// filling the structure tree with rows, but this is a single number.
	const exp = scope ? expandInstances({ forEach: r.forEach }, scope, Infinity) : null;
	if (!exp) return { label: '∀', title: `for_each = ${r.forEach}` + EXPANSION_HINT };
	const shown = exp.keys.slice(0, KEYS_IN_TITLE);
	const more = exp.keys.length - shown.length;
	const keys = exp.keys.length
		? `: ${shown.map((k) => `"${k}"`).join(', ')}${more ? ` +${more} more` : ''}`
		: '';
	return {
		label: `×${exp.keys.length}`,
		title:
			`for_each = ${r.forEach}\n→ ${exp.keys.length} instance${exp.keys.length === 1 ? '' : 's'}` +
			keys
	};
}
