import type { Resource } from '$lib/types';

const PALETTE = ['#7aa2f7', '#c297ff', '#7ee787', '#ffb86b', '#79c0ff', '#ff9492', '#f8c555', '#56d4bc'];

// typeColor returns a stable color per resource type so like resources match.
export function typeColor(type: string): string {
	let h = 0;
	for (let i = 0; i < type.length; i++) h = (h * 31 + type.charCodeAt(i)) >>> 0;
	return PALETTE[h % PALETTE.length];
}

export function typeLetter(type: string): string {
	const sub = type.includes('_') ? type.slice(type.indexOf('_') + 1) : type;
	return (sub[0] || type[0] || '?').toUpperCase();
}

export interface Edge {
	id: string;
	from: string;
	to: string;
	kind: 'ref' | 'depends';
}

const ADDR_RE = /([a-z][a-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_-]*)/g;

// deriveEdges builds dependency edges from the model: explicit depends_on and
// any attribute reference/expression that mentions another resource's address.
export function deriveEdges(resources: Resource[]): Edge[] {
	const addr = new Map<string, string>();
	for (const r of resources) addr.set(`${r.type}.${r.name}`, r.id);

	const edges: Edge[] = [];
	const seen = new Set<string>();
	const add = (fromId: string | undefined, toId: string, kind: 'ref' | 'depends') => {
		if (!fromId || fromId === toId) return;
		const k = `${fromId}->${toId}:${kind}`;
		if (seen.has(k)) return;
		seen.add(k);
		edges.push({ id: k, from: fromId, to: toId, kind });
	};

	for (const r of resources) {
		for (const dep of r.dependsOn ?? []) {
			const a = dep.split('.').slice(0, 2).join('.');
			add(addr.get(a), r.id, 'depends');
		}
		for (const v of Object.values(r.attrs ?? {})) {
			const text = v.mode === 'reference' ? (v.ref ?? '') : v.mode === 'expression' ? (v.expr ?? '') : '';
			if (!text) continue;
			let m: RegExpExecArray | null;
			ADDR_RE.lastIndex = 0;
			while ((m = ADDR_RE.exec(text))) add(addr.get(`${m[1]}.${m[2]}`), r.id, 'ref');
		}
	}
	return edges;
}
