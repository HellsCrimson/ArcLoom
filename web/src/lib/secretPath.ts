// Secret names are flat (`global_projectA_varA`) — that name is what the model
// references and what becomes TF_VAR_<name> at plan time. This module reads a
// hierarchy out of them by treating `_` as a path separator, so the same secrets
// can be browsed as a tree without changing anything about how they are stored,
// referenced or injected.
import type { SecretMeta } from '$lib/types';

/** Separator between path segments inside a secret name. */
export const SECRET_SEP = '_';

/** Separator the UI accepts when a user types a path ("global/projectA"). */
export const PATH_SEP = '/';

/** splitSecretName turns `global_projectA_varA` into its segments. */
export function splitSecretName(name: string): string[] {
	return name.split(SECRET_SEP).filter((s) => s !== '');
}

/** joinSecretName composes segments into the stored name. */
export function joinSecretName(segments: string[]): string {
	return segments
		.flatMap((s) => s.split(/[/_]/))
		.map((s) => s.trim())
		.filter((s) => s !== '')
		.join(SECRET_SEP);
}

/** displayPath renders a stored name as the path form shown to the user. */
export function displayPath(name: string): string {
	return splitSecretName(name).join(PATH_SEP);
}

// SecretNode is one level of the tree. A node can be BOTH a folder and a secret:
// `global` may exist as a secret while `global_projectA_varA` also does.
export interface SecretNode {
	/** This level's segment, e.g. "projectA". */
	segment: string;
	/** The composed name of this level, e.g. "global_projectA". */
	name: string;
	children: SecretNode[];
	/** Set when a secret is stored under exactly this name (a leaf). */
	secret?: SecretMeta;
	/** How many secrets live at or below this node. */
	count: number;
}

/**
 * buildSecretTree groups secrets by their `_`-separated segments. Names that
 * contain no separator stay at the root, so a flat setup looks unchanged.
 */
export function buildSecretTree(secrets: SecretMeta[]): SecretNode[] {
	const roots: SecretNode[] = [];

	for (const s of [...secrets].sort((a, b) => a.name.localeCompare(b.name))) {
		const segments = splitSecretName(s.name);
		if (segments.length === 0) continue;
		let level = roots;
		let prefix = '';
		segments.forEach((segment, i) => {
			prefix = prefix ? `${prefix}${SECRET_SEP}${segment}` : segment;
			let node = level.find((n) => n.segment === segment);
			if (!node) {
				node = { segment, name: prefix, children: [], count: 0 };
				level.push(node);
			}
			node.count++;
			if (i === segments.length - 1) node.secret = s;
			level = node.children;
		});
	}
	return roots;
}

/**
 * secretGroups buckets names by their first segment, for the grouped picker in
 * the resource form. Names without a separator land under "" (ungrouped).
 */
export function secretGroups(names: string[]): { group: string; names: string[] }[] {
	const byGroup = new Map<string, string[]>();
	for (const name of names) {
		const segments = splitSecretName(name);
		const group = segments.length > 1 ? segments[0] : '';
		byGroup.set(group, [...(byGroup.get(group) ?? []), name]);
	}
	// Ungrouped first, then alphabetically — the picker reads top-down.
	return [...byGroup.entries()]
		.sort((a, b) => (a[0] === '' ? -1 : b[0] === '' ? 1 : a[0].localeCompare(b[0])))
		.map(([group, names]) => ({ group, names }));
}
