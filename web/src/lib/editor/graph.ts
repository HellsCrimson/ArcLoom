import type { AttrValue, Project, Resource } from '$lib/types';

// Shared node geometry (a resource card, and the padding/header of a module box).
export const NODE_W = 180;
export const NODE_H = 84;
export const DOT_Y = 42; // vertical centre of a resource card's connection dots
export const MOD_HEADER_H = 34;
export const MOD_PAD = 16;
export const MOD_GAP_X = 40;
export const MOD_GAP_Y = 28;

const PALETTE = ['#7aa2f7', '#c297ff', '#7ee787', '#ffb86b', '#79c0ff', '#ff9492', '#f8c555', '#56d4bc'];

// Plan action styling (shared by the run-output diff list and the canvas),
// matching terraform's own symbols.
export const ACTION_COLOR: Record<string, string> = {
	create: '#3fb950', update: '#d29922', replace: '#f0883e', delete: '#f85149', read: '#8b95a5'
};
export const ACTION_SYMBOL: Record<string, string> = {
	create: '+', update: '~', replace: '±', delete: '−', read: '→'
};

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
const MODULE_RE = /module\.([A-Za-z_][A-Za-z0-9_-]*)/g;

// modId is the canvas node id for a local module group.
export function modId(name: string): string {
	return `mod:${name}`;
}

// attrText returns the reference/expression text of an attribute value (or "").
function attrText(v: AttrValue): string {
	if (v.mode === 'reference') return v.ref ?? '';
	if (v.mode === 'expression') return v.expr ?? '';
	return '';
}

// crossScopeEdges derives edges between the root scope and local module groups
// from the model: a root resource that references `module.<m>.<out>` depends on
// module m; a module input that references a root `type.name` (or another
// `module.<m>`) makes the module depend on that resource/module. Endpoints are
// root resource ids or modId(name).
export function crossScopeEdges(project: Project): Edge[] {
	const edges: Edge[] = [];
	const seen = new Set<string>();
	const add = (from: string, to: string) => {
		if (from === to) return;
		const k = `${from}->${to}`;
		if (seen.has(k)) return;
		seen.add(k);
		edges.push({ id: `x:${k}`, from, to, kind: 'ref' });
	};

	const localNames = new Set((project.localModules ?? []).map((m) => m.name));
	const rootAddr = new Map<string, string>(); // "type.name" -> resource id
	for (const r of project.resources) rootAddr.set(`${r.type}.${r.name}`, r.id);

	// Root resource -> module output reference: module is the dependency.
	for (const r of project.resources) {
		for (const v of Object.values(r.attrs ?? {})) {
			const text = attrText(v);
			if (!text) continue;
			let m: RegExpExecArray | null;
			MODULE_RE.lastIndex = 0;
			while ((m = MODULE_RE.exec(text))) {
				if (localNames.has(m[1])) add(modId(m[1]), r.id);
			}
		}
	}

	// Module inputs -> references to root resources / other modules.
	for (const lm of project.localModules ?? []) {
		for (const v of Object.values(lm.inputs ?? {})) {
			const text = attrText(v);
			if (!text) continue;
			let a: RegExpExecArray | null;
			ADDR_RE.lastIndex = 0;
			while ((a = ADDR_RE.exec(text))) {
				const id = rootAddr.get(`${a[1]}.${a[2]}`);
				if (id) add(id, modId(lm.name));
			}
			let mm: RegExpExecArray | null;
			MODULE_RE.lastIndex = 0;
			while ((mm = MODULE_RE.exec(text))) {
				if (localNames.has(mm[1]) && mm[1] !== lm.name) add(modId(mm[1]), modId(lm.name));
			}
		}
	}
	return edges;
}

// interiorLayout places a module's child resources in a compact grid relative to
// the module box origin, and returns the box size needed to contain them.
export function interiorLayout(resources: Resource[]): {
	pos: Record<string, { x: number; y: number }>;
	w: number;
	h: number;
} {
	const n = resources.length;
	const cols = Math.max(1, Math.ceil(Math.sqrt(n)));
	const rows = Math.max(1, Math.ceil(n / cols));
	const pos: Record<string, { x: number; y: number }> = {};
	resources.forEach((r, i) => {
		pos[r.id] = {
			x: MOD_PAD + (i % cols) * (NODE_W + MOD_GAP_X),
			y: MOD_HEADER_H + MOD_PAD + Math.floor(i / cols) * (NODE_H + MOD_GAP_Y)
		};
	});
	const w = Math.max(220, MOD_PAD * 2 + cols * NODE_W + (cols - 1) * MOD_GAP_X);
	const h =
		n === 0
			? MOD_HEADER_H + 56
			: MOD_HEADER_H + MOD_PAD * 2 + rows * NODE_H + (rows - 1) * MOD_GAP_Y;
	return { pos, w, h };
}

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

// ── Auto-layout ────────────────────────────────────────────────────────────
export type LayoutMode = 'layered-h' | 'layered-v' | 'grid' | 'by-type' | 'circle';

export interface LayoutOption {
	mode: LayoutMode;
	label: string;
	hint: string;
}

export const LAYOUTS: LayoutOption[] = [
	{ mode: 'layered-h', label: 'Layers →', hint: 'Dependency layers, flowing left to right' },
	{ mode: 'layered-v', label: 'Layers ↓', hint: 'Dependency layers, flowing top to bottom' },
	{ mode: 'by-type', label: 'By type', hint: 'One column per resource type' },
	{ mode: 'grid', label: 'Grid', hint: 'Compact grid, ignores dependencies' },
	{ mode: 'circle', label: 'Circle', hint: 'Arrange all nodes on a ring' }
];

const COL = 240; // horizontal spacing between columns
const ROW = 120; // vertical spacing between rows
const OX = 40; // origin x
const OY = 110; // origin y — clears the floating top toolbar

// computeLevels assigns each resource a dependency depth: roots are 0, and a
// node is one deeper than the deepest thing it depends on (longest-path).
export function computeLevels(resources: Resource[], edges: Edge[]): Map<string, number> {
	const level = new Map(resources.map((r) => [r.id, 0]));
	for (let i = 0; i < resources.length; i++) {
		let ch = false;
		for (const e of edges) {
			const nl = (level.get(e.from) ?? 0) + 1;
			if (nl > (level.get(e.to) ?? 0)) {
				level.set(e.to, nl);
				ch = true;
			}
		}
		if (!ch) break;
	}
	return level;
}

// layoutPositions computes a fresh position for every resource under the given
// mode. It replaces (rather than merges) positions, so callers use it for an
// explicit "re-arrange" action; new-node placement uses layoutMissing instead.
export function layoutPositions(
	resources: Resource[],
	edges: Edge[],
	mode: LayoutMode
): Record<string, { x: number; y: number }> {
	const pos: Record<string, { x: number; y: number }> = {};

	if (mode === 'grid') {
		const cols = Math.max(1, Math.ceil(Math.sqrt(resources.length)));
		resources.forEach((r, i) => {
			pos[r.id] = { x: OX + (i % cols) * COL, y: OY + Math.floor(i / cols) * ROW };
		});
		return pos;
	}

	if (mode === 'circle') {
		const n = Math.max(1, resources.length);
		const radius = Math.max(220, n * 34);
		const cx = OX + radius;
		const cy = OY + radius;
		resources.forEach((r, i) => {
			const a = (i / n) * Math.PI * 2 - Math.PI / 2;
			pos[r.id] = { x: Math.round(cx + radius * Math.cos(a)), y: Math.round(cy + radius * Math.sin(a)) };
		});
		return pos;
	}

	if (mode === 'by-type') {
		const types = [...new Set(resources.map((r) => r.type))].sort();
		const col = new Map(types.map((t, i) => [t, i]));
		const rows = new Map<string, number>();
		for (const r of resources) {
			const row = rows.get(r.type) ?? 0;
			pos[r.id] = { x: OX + (col.get(r.type) ?? 0) * COL, y: OY + row * ROW };
			rows.set(r.type, row + 1);
		}
		return pos;
	}

	// layered-h / layered-v: group by dependency level.
	const level = computeLevels(resources, edges);
	const byLevel = new Map<number, string[]>();
	for (const r of resources) {
		const l = level.get(r.id) ?? 0;
		if (!byLevel.has(l)) byLevel.set(l, []);
		byLevel.get(l)!.push(r.id);
	}
	for (const [l, ids] of byLevel) {
		ids.forEach((id, i) => {
			pos[id] =
				mode === 'layered-v'
					? { x: OX + i * COL, y: OY + l * ROW }
					: { x: OX + l * COL, y: OY + i * ROW };
		});
	}
	return pos;
}

// layoutMissing places only resources that don't already have a position,
// preserving user-dragged nodes. Used for incremental placement of new nodes.
export function layoutMissing(
	resources: Resource[],
	edges: Edge[],
	existing: Record<string, { x: number; y: number }>
): Record<string, { x: number; y: number }> {
	const missing = resources.filter((r) => !existing[r.id]);
	if (missing.length === 0) return existing;

	const level = computeLevels(resources, edges);
	const next = { ...existing };
	const byLevel = new Map<number, string[]>();
	for (const r of missing) {
		const l = level.get(r.id) ?? 0;
		if (!byLevel.has(l)) byLevel.set(l, []);
		byLevel.get(l)!.push(r.id);
	}
	for (const [l, ids] of byLevel) {
		let row = Object.values(next).filter((p) => Math.round((p.x - OX) / COL) === l).length;
		for (const id of ids) {
			next[id] = { x: OX + l * COL, y: OY + row * ROW };
			row++;
		}
	}
	return next;
}
