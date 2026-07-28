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
const DATA_RE = /\bdata\.([a-z][a-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_-]*)/g;

// DATA_COLOR is the fixed accent for data-source nodes (distinct from the
// per-type resource palette).
export const DATA_COLOR = '#56d4bc';

// baseAddr reduces a reference to its resource address: "a.b.attr" -> "a.b",
// "data.a.b.attr" -> "data.a.b".
export function baseAddr(ref: string): string {
	const parts = ref.split('.');
	return parts.slice(0, parts[0] === 'data' ? 3 : 2).join('.');
}

// modId is the canvas node id for a local module DEFINITION group box (keyed by
// folder name — there is exactly one box per definition, however many calls
// point at it).
export function modId(name: string): string {
	return `mod:${name}`;
}

// rmodId is the canvas node id for a module CALL card (keyed by the call's
// name, i.e. its address). Local and registry calls both use it.
export function rmodId(name: string): string {
	return `rmod:${name}`;
}

// LOCAL_SOURCE_PREFIX mirrors model.LocalSourcePrefix.
const LOCAL_SOURCE_PREFIX = './modules/';

// localFolder mirrors model.Module.LocalFolder: the definition a call targets,
// or "" for a registry/remote source. Use this — never the call's name — to go
// from a call back to its definition.
export function localFolder(m: { source?: string }): string {
	const src = m.source ?? '';
	if (!src.startsWith(LOCAL_SOURCE_PREFIX)) return '';
	const folder = src.slice(LOCAL_SOURCE_PREFIX.length);
	return folder && !folder.includes('/') ? folder : '';
}

// REGISTRY_MODULE_COLOR is the accent for registry module nodes (local module
// groups use the app accent).
export const REGISTRY_MODULE_COLOR = '#c297ff';

// attrText returns the reference/expression text of an attribute value (or "").
function attrText(v: AttrValue): string {
	if (v.mode === 'reference') return v.ref ?? '';
	if (v.mode === 'expression') return v.expr ?? '';
	return '';
}

// crossScopeEdges derives edges between the root scope, module call cards and
// local module definition boxes: a root resource that references
// `module.<call>.<out>` depends on that call; a call's argument that references
// a root `type.name` (or another call) makes the call depend on it; and a local
// call is joined to the definition it uses, which is what keeps "this is used
// here" visible once a definition can be called more than once.
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

	// `module.<name>` in an expression is a CALL's address, so only calls go in
	// this map — a definition's folder name is not addressable in HCL.
	const moduleIds = new Map<string, string>();
	for (const m of project.modules ?? []) moduleIds.set(m.name, rmodId(m.name));
	// Definition box <- call card, for every local call.
	const defs = new Set((project.localModules ?? []).map((lm) => lm.name));
	for (const m of project.modules ?? []) {
		const folder = localFolder(m);
		if (folder && defs.has(folder)) add(modId(folder), rmodId(m.name));
	}
	const rootAddr = new Map<string, string>(); // "type.name" / "data.type.name" -> node id
	for (const r of project.resources) rootAddr.set(`${r.type}.${r.name}`, r.id);
	for (const d of project.dataSources ?? []) rootAddr.set(`data.${d.type}.${d.name}`, d.id);

	// Root resource / data source -> module output reference: module is the dependency.
	for (const r of [...project.resources, ...(project.dataSources ?? [])]) {
		for (const v of Object.values(r.attrs ?? {})) {
			const text = attrText(v);
			if (!text) continue;
			let m: RegExpExecArray | null;
			MODULE_RE.lastIndex = 0;
			while ((m = MODULE_RE.exec(text))) {
				const id = moduleIds.get(m[1]);
				if (id) add(id, r.id);
			}
		}
	}

	// Module inputs -> references to root resources / data sources / other
	// modules. Local module inputs and registry module config are scanned alike.
	const scanInputs = (name: string, values: AttrValue[]) => {
		const selfId = moduleIds.get(name);
		if (!selfId) return;
		for (const v of values) {
			const text = attrText(v);
			if (!text) continue;
			let d: RegExpExecArray | null;
			DATA_RE.lastIndex = 0;
			while ((d = DATA_RE.exec(text))) {
				const id = rootAddr.get(`data.${d[1]}.${d[2]}`);
				if (id) add(id, selfId);
			}
			let a: RegExpExecArray | null;
			ADDR_RE.lastIndex = 0;
			while ((a = ADDR_RE.exec(text))) {
				const id = rootAddr.get(`${a[1]}.${a[2]}`);
				if (id) add(id, selfId);
			}
			let mm: RegExpExecArray | null;
			MODULE_RE.lastIndex = 0;
			while ((mm = MODULE_RE.exec(text))) {
				const id = moduleIds.get(mm[1]);
				if (id && mm[1] !== name) add(id, selfId);
			}
		}
	};
	// count / for_each on a module call are expressions too and may reference a
	// root resource (`for_each = toset(aws_subnet.main[*].id)`), so they are
	// scanned alongside the inputs — mirroring deriveEdges for resources.
	const metaValues = (meta: { count?: string; forEach?: string }): AttrValue[] =>
		[meta.count, meta.forEach]
			.filter((t): t is string => !!t)
			.map((expr) => ({ mode: 'expression', expr }) as AttrValue);
	for (const m of project.modules ?? [])
		scanInputs(m.name, [...Object.values(m.config ?? {}), ...metaValues(m)]);
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
// any attribute reference/expression that mentions another resource's or data
// source's address. Data sources participate as both producers and consumers.
export function deriveEdges(resources: Resource[], dataSources: Resource[] = []): Edge[] {
	const addr = new Map<string, string>();
	for (const r of resources) addr.set(`${r.type}.${r.name}`, r.id);
	for (const d of dataSources) addr.set(`data.${d.type}.${d.name}`, d.id);

	const edges: Edge[] = [];
	const seen = new Set<string>();
	const add = (fromId: string | undefined, toId: string, kind: 'ref' | 'depends') => {
		if (!fromId || fromId === toId) return;
		const k = `${fromId}->${toId}:${kind}`;
		if (seen.has(k)) return;
		seen.add(k);
		edges.push({ id: k, from: fromId, to: toId, kind });
	};

	for (const r of [...resources, ...dataSources]) {
		for (const dep of r.dependsOn ?? []) {
			add(addr.get(baseAddr(dep)), r.id, 'depends');
		}
		const texts = Object.values(r.attrs ?? {}).map((v) =>
			v.mode === 'reference' ? (v.ref ?? '') : v.mode === 'expression' ? (v.expr ?? '') : ''
		);
		// count / for_each are expressions too and may reference other resources.
		texts.push(r.count ?? '', r.forEach ?? '');
		for (const text of texts) {
			if (!text) continue;
			let m: RegExpExecArray | null;
			DATA_RE.lastIndex = 0;
			while ((m = DATA_RE.exec(text))) add(addr.get(`data.${m[1]}.${m[2]}`), r.id, 'ref');
			ADDR_RE.lastIndex = 0;
			// "data.x" itself matches ADDR_RE but never resolves in addr (resource
			// keys have no "data." prefix), so data refs don't double-count.
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
