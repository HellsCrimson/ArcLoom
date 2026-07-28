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
// expression is one; a for_each (or a computed count) can only be resolved by
// the engine, hence the pointer to the State panel — the plan lists instances
// too, but only those that change.

export const EXPANSION_HINT = '\nOpen State to list the instances this expanded to.';

export type MultiBadge = { label: string; title: string };

/** Anything with the count/for_each meta-args: a resource, a data source, a
 *  local-module call or a registry module call. */
export type Multiplied = { count?: string; forEach?: string };

export function multiBadge(r: Multiplied): MultiBadge | undefined {
	if (r.count) {
		const literal = /^\d+$/.test(r.count.trim());
		const n = literal ? r.count.trim() : 'N';
		return { label: `×${n}`, title: `count = ${r.count}` + (literal ? '' : EXPANSION_HINT) };
	}
	if (r.forEach) return { label: '∀', title: `for_each = ${r.forEach}` + EXPANSION_HINT };
	return undefined;
}
