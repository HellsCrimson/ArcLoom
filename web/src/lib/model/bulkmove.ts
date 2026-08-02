// Pairing state addresses with the instances they should become.
//
// The motivating refactor: several hand-written resources (`null_resource.www`,
// `null_resource.api`, …) are folded into one `for_each`'d block, which changes
// every address. Unless the state moves with them the next apply DESTROYS and
// recreates the real objects — so the rename is not optional, it is the point.
//
// Everything here is a SUGGESTION over addresses the user can still edit: the
// keys come from `expandInstances` when the collection is resolvable, and the
// name match below is a convenience, never a claim. Nothing is derived from the
// state's contents, only from its addresses.

import { indexSuffix } from './expand';

/**
 * resourceName pulls the block name out of a state address:
 * `module.team["a"].null_resource.www[0]` -> `www`.
 */
export function resourceName(addr: string): string {
	const noIndex = addr.replace(/\[[^\]]*\]/g, '');
	const parts = noIndex.split('.').filter(Boolean);
	return parts[parts.length - 1] ?? '';
}

/**
 * suggestKey picks the instance key a source address should move to: its own
 * name when the collection has a key by that name (`null_resource.www` ->
 * `["www"]`, which is what a `toset(["www", …])` refactor produces), then a
 * near match, then the first key still unused. Returns "" when nothing is left,
 * and the row is typed instead.
 */
export function suggestKey(from: string, keys: string[], used: Set<string>): string {
	const name = resourceName(from);
	const free = keys.filter((k) => !used.has(k));
	if (free.includes(name)) return name;
	const near = free.find((k) => k !== '' && (name.includes(k) || k.includes(name)));
	if (near) return near;
	return free[0] ?? '';
}

/**
 * moveTo composes the destination address. A target with no count/for_each
 * takes no index — that is an ordinary rename, which is a legitimate move too.
 */
export function moveTo(target: string, kind: 'count' | 'for_each' | null, key: string): string {
	const t = target.trim();
	const k = key.trim();
	if (!t) return '';
	if (!kind || !k) return t;
	return t + indexSuffix({ kind }, k);
}

/** A source address paired with the instance key it moves to. */
export interface MoveRow {
	from: string;
	key: string;
}

/**
 * pairRows assigns a key to every selected source in order, never reusing one.
 * Existing keys are kept: re-running this when the selection changes must not
 * shuffle rows the user already corrected.
 */
export function pairRows(rows: MoveRow[], keys: string[]): MoveRow[] {
	const used = new Set(rows.map((r) => r.key).filter((k) => k !== '' && keys.includes(k)));
	return rows.map((r) => {
		if (r.key && keys.includes(r.key)) return r;
		if (keys.length === 0) return r;
		const key = suggestKey(r.from, keys, used);
		if (key) used.add(key);
		return { ...r, key };
	});
}
