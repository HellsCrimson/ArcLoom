<script lang="ts">
	import type { LocalModule, Project, Resource, ResourceKind } from '$lib/types';
	import type { PlanChange } from '$lib/api/client';
	import {
		deriveEdges, crossScopeEdges, interiorLayout, modId, rmodId, localFolder, typeColor, typeLetter, baseAddr,
		layoutPositions, layoutMissing, LAYOUTS, edgeGeometry,
		NODE_W, NODE_H, DOT_Y, MOD_HEADER_H, ACTION_COLOR, ACTION_SYMBOL, DATA_COLOR, REGISTRY_MODULE_COLOR,
		type Edge, type LayoutMode
	} from './graph';
	import HclView from './HclView.svelte';
	import { multiBadge } from '$lib/model/multiplicity';
	import { X, Plus, Minus, Search, Loader2, Network, Rows3, LayoutGrid, CircleDashed, Group, Folder, FolderOpen, ChevronRight, ChevronDown, Play, Package, Magnet, MessageSquare, Trash2 } from 'lucide-svelte';

	const LAYOUT_ICONS: Record<LayoutMode, typeof Network> = {
		'layered-h': Network,
		'layered-v': Rows3,
		'by-type': Group,
		grid: LayoutGrid,
		circle: CircleDashed
	};

	let {
		project,
		hcl,
		accent = '#7aa2f7',
		planChanges = [],
		planning = false,
		onPlan,
		loadTypes,
		onAddType,
		onEditResource,
		onDeleteResource,
		onRenameResource,
		onSetDependsOn,
		onEditModuleCall,
		onClose
	}: {
		project: Project;
		hcl: string;
		accent?: string;
		planChanges?: PlanChange[];
		planning?: boolean;
		onPlan?: () => void;
		loadTypes: (source: string) => Promise<{ resourceTypes: string[]; dataSourceTypes: string[] }>;
		onAddType: (type: string, providerSource: string, kind?: ResourceKind) => void;
		onEditResource: (r: Resource, module?: string) => void;
		onDeleteResource: (id: string, module?: string) => void | Promise<void>;
		onRenameResource: (id: string, name: string, module?: string) => void;
		onSetDependsOn: (id: string, deps: string[], module?: string) => void;
		onEditModuleCall?: (name: string) => void;
		onClose: () => void;
	} = $props();

	let canvasEl = $state<HTMLDivElement>();
	const posKey = $derived(`arcloom.pos.${project.id}`);
	const expandKey = $derived(`arcloom.modexp.${project.id}`);

	let positions = $state<Record<string, { x: number; y: number }>>({});
	let expanded = $state<Record<string, boolean>>({});
	// Dragging moves every selected top-level box at once (each keeps its own
	// grab offset).
	let dragging = $state<{ items: { id: string; ox: number; oy: number }[] } | null>(null);
	let dragMoved = $state(false); // distinguishes a drag from a click
	// Connections are scoped: root dots connect root nodes, a module's interior
	// dots connect within that module (dependencies can't cross the boundary).
	let connecting = $state<{ fromId: string; scope?: string; x: number; y: number } | null>(null);
	let selected = $state<string[]>([]);
	// Marquee selection (Shift+drag on empty canvas), in world coordinates.
	let marquee = $state<{ x0: number; y0: number; x1: number; y1: number } | null>(null);
	// Inline rename overlay on a card (replaces window.prompt).
	let renaming = $state<{ id: string; scope?: string; value: string } | null>(null);
	// Snap-to-grid: shared preference across projects (grid matches the 22px dots).
	const GRID = 22;
	let snap = $state(localStorage.getItem('arcloom.snap') === '1');
	function toggleSnap() {
		snap = !snap;
		localStorage.setItem('arcloom.snap', snap ? '1' : '0');
	}

	// Viewport pan/zoom (world = (screen - t) / scale).
	let tx = $state(0);
	let ty = $state(0);
	let scale = $state(1);
	let panning = $state<{ sx: number; sy: number; ox: number; oy: number } | null>(null);

	// Provider palette
	let activeProvider = $state('');
	$effect(() => {
		if (!activeProvider && project.providers[0]) activeProvider = project.providers[0].key;
	});
	let paletteTypes = $state<string[]>([]);
	let paletteDataTypes = $state<string[]>([]);
	let paletteKind = $state<ResourceKind>('resource');
	let typeFilter = $state('');
	let loadingTypes = $state(false);
	let showPalette = $state(false);

	const activeSource = $derived(
		project.providers.find((p) => p.key === activeProvider)?.source ?? project.providers[0]?.source ?? ''
	);
	const providerFooter = $derived((activeProvider || 'no') + ' provider');

	// One pill per provider KEY, not per instance: instance identity is
	// (key, alias), so an aliased instance repeats its key and browses the very
	// same schema. Keying the pills by p.key over the raw list made Svelte throw
	// each_key_duplicate the moment a project had an alias, which aborted the
	// whole canvas render — the Editor button lit up and no graph appeared.
	const providerKeys = $derived([...new Set(project.providers.map((p) => p.key))]);

	// Definitions (modules/<name>/ folders) and calls (`module "<name>"` blocks)
	// are separate lists: one box per definition, one card per call, and a call
	// resolves to its definition through localFolder(source) — never by name.
	const localModules = $derived(project.localModules ?? []);
	const moduleCalls = $derived(project.modules ?? []);
	// Every call name that uses a given definition; the plan reports changes
	// under the CALL's address, so a definition box has to look at all of them.
	function callNamesFor(folder: string): string[] {
		return moduleCalls.filter((m) => localFolder(m) === folder).map((m) => m.name);
	}
	const rootDataSources = $derived(project.dataSources ?? []);
	// Scope for the multiplicity badge: a for_each naming a var/local this scope
	// declares can be counted here (×2), instead of the blanket ∀ that only the
	// state could resolve.
	const rootScope = $derived({ variables: project.variables ?? [], locals: project.locals ?? [] });
	const moduleScope = (lm: LocalModule) => ({ variables: lm.variables ?? [], locals: lm.locals ?? [] });

	// lookupNode resolves a node id to its resource and depends_on address in a
	// scope: root when scope is undefined, otherwise inside that local module
	// (module-interior addresses are local — "type.name", no module prefix).
	function lookupNode(id: string, scope?: string): { r: Resource; addr: string } | null {
		const lm = scope ? localModules.find((m) => m.name === scope) : undefined;
		const resources = scope ? (lm?.resources ?? []) : project.resources;
		const dataSources = scope ? (lm?.dataSources ?? []) : rootDataSources;
		const r = resources.find((x) => x.id === id);
		if (r) return { r, addr: `${r.type}.${r.name}` };
		const d = dataSources.find((x) => x.id === id);
		if (d) return { r: d, addr: `data.${d.type}.${d.name}` };
		return null;
	}

	// --- Plan overlay: map last plan's changes onto canvas nodes by address. ---
	// Counted / for_each resources plan as per-instance addresses ("x.y[0]",
	// 'x.y["a"]') but render as one node, so the instance index is stripped and
	// the most severe pending action wins for the node's color.
	const stripIndex = (addr: string) => addr.replace(/\[[^\]]*\]/g, '');
	const SEVERITY: Record<string, number> = { read: 0, update: 1, create: 2, delete: 3, replace: 4 };
	const changeByAddr = $derived.by(() => {
		const m = new Map<string, string>();
		for (const c of planChanges) {
			// Data-source reads are included too: their addresses already carry
			// the "data." prefix, so they can't collide with managed resources.
			const key = stripIndex(c.address);
			const prev = m.get(key);
			if (!prev || (SEVERITY[c.action] ?? 0) > (SEVERITY[prev] ?? 0)) m.set(key, c.action);
		}
		return m;
	});
	// Changed attribute paths per node address, for the plan badge's tooltip: the
	// card can say WHICH properties move, not just that something does. The full
	// diff lives in the resource form (PlanDiffPanel) and the run pane.
	const diffPathsByAddr = $derived.by(() => {
		const m = new Map<string, string[]>();
		for (const c of planChanges) {
			if (!c.diffs?.length) continue;
			const key = stripIndex(c.address);
			const paths = m.get(key) ?? [];
			for (const d of c.diffs) if (!paths.includes(d.path)) paths.push(d.path);
			m.set(key, paths);
		}
		return m;
	});
	const DIFF_HINT_MAX = 6;
	function planAttrHint(scope: string | undefined, kind: ResourceKind, type: string, name: string) {
		const addr = `${scope ? `module.${scope}.` : ''}${kind === 'data' ? 'data.' : ''}${type}.${name}`;
		const paths = diffPathsByAddr.get(addr);
		if (!paths?.length) return '';
		const shown = paths.slice(0, DIFF_HINT_MAX).join(', ');
		return `\n${paths.length} attribute${paths.length === 1 ? '' : 's'}: ${shown}${paths.length > DIFF_HINT_MAX ? ', …' : ''}\nOpen the resource for the full diff`;
	}

	// Planned deletes at root scope have no model node left to color; render
	// them as non-interactive ghost cards so the disappearance is visible.
	// Instance addresses of a counted resource collapse into ONE ghost.
	const ghosts = $derived.by(() => {
		const seen = new Set<string>();
		const out: typeof planChanges = [];
		for (const c of planChanges) {
			if (c.action !== 'delete' || c.mode !== 'managed' || c.moduleAddress) continue;
			const addr = stripIndex(c.address);
			if (seen.has(addr) || project.resources.some((r) => `${r.type}.${r.name}` === addr)) continue;
			seen.add(addr);
			out.push({ ...c, address: addr });
		}
		return out;
	});
	// Aggregate pending actions per local module (covers nested addresses too).
	// Plan counts for a definition box: the sum over every call that uses it,
	// since one box stands for a definition that may be called several times.
	function moduleChangeCounts(folder: string) {
		const prefixes = callNamesFor(folder).map((n) => `module.${n}`);
		const agg = { create: 0, update: 0, replace: 0, delete: 0, total: 0 };
		if (prefixes.length === 0) return agg;
		for (const c of planChanges) {
			if (c.mode !== 'managed') continue;
			// A for_each'd module call reports moduleAddress as 'module.team["alpha"]',
			// so the instance key must be stripped here too — comparing it raw matches
			// neither `module.team` nor `module.team.`, and the card read 0 changes.
			const ma = stripIndex(c.moduleAddress ?? '');
			if (!prefixes.some((p) => ma === p || ma.startsWith(p + '.'))) continue;
			if (c.action in agg) {
				agg[c.action as keyof typeof agg]++;
				agg.total++;
			}
		}
		return agg;
	}

	// Plan counts for ONE call card.
	function callChangeCounts(name: string) {
		const pref = `module.${name}`;
		const agg = { create: 0, update: 0, replace: 0, delete: 0, total: 0 };
		for (const c of planChanges) {
			if (c.mode !== 'managed') continue;
			const ma = stripIndex(c.moduleAddress ?? '');
			if (ma !== pref && !ma.startsWith(pref + '.')) continue;
			if (c.action in agg) {
				agg[c.action as keyof typeof agg]++;
				agg.total++;
			}
		}
		return agg;
	}

	// changeForChild finds a plan change for a module-interior card under ANY
	// call to that definition (first match wins — the box is shared).
	function changeForChild(folder: string, addr: string): string | undefined {
		for (const n of callNamesFor(folder)) {
			const c = changeByAddr.get(`module.${n}.${addr}`);
			if (c) return c;
		}
		return undefined;
	}
	const planCounts = $derived.by(() => {
		const agg = { create: 0, update: 0, replace: 0, delete: 0, total: 0 };
		for (const c of planChanges) {
			if (c.mode !== 'managed') continue;
			if (c.action in agg) {
				agg[c.action as keyof typeof agg]++;
				agg.total++;
			}
		}
		return agg;
	});

	// Persist / restore positions + expand state per project.
	$effect(() => {
		posKey; // track
		try {
			positions = JSON.parse(localStorage.getItem(posKey) || '{}');
		} catch {
			positions = {};
		}
	});
	$effect(() => {
		expandKey; // track
		try {
			expanded = JSON.parse(localStorage.getItem(expandKey) || '{}');
		} catch {
			expanded = {};
		}
	});
	function savePos() {
		localStorage.setItem(posKey, JSON.stringify(positions));
	}

	// Per-node annotations: a short free-text note attached to a node (resource /
	// data source / interior card), keyed by node id. Documentation only — kept
	// in localStorage per project (like positions), never written to HCL.
	const noteKey = $derived(`arcloom.notes.${project.id}`);
	let notes = $state<Record<string, string>>({});
	$effect(() => {
		noteKey; // track
		try {
			notes = JSON.parse(localStorage.getItem(noteKey) || '{}');
		} catch {
			notes = {};
		}
	});
	// The open note editor: anchored at the click's screen coordinates.
	let editingNote = $state<{ id: string; value: string; sx: number; sy: number } | null>(null);
	function openNote(e: MouseEvent, id: string) {
		e.stopPropagation();
		editingNote = { id, value: notes[id] ?? '', sx: e.clientX, sy: e.clientY };
	}
	function commitNote() {
		if (!editingNote) return;
		const v = editingNote.value.trim();
		const next = { ...notes };
		if (v) next[editingNote.id] = v;
		else delete next[editingNote.id];
		notes = next;
		localStorage.setItem(noteKey, JSON.stringify(notes));
		editingNote = null;
	}

	function toggleExpand(name: string) {
		expanded = { ...expanded, [name]: !expanded[name] };
		localStorage.setItem(expandKey, JSON.stringify(expanded));
	}

	// Macro nodes are the things laid out at the top level: every root resource
	// and data source, plus one pseudo-node per local module, plus a ghost per
	// planned root delete (so it gets an auto position). Layout helpers only
	// read id/type.
	const macroNodes = $derived<Resource[]>([
		...project.resources,
		...rootDataSources,
		...localModules.map((lm) => ({ id: modId(lm.name), type: 'module', name: lm.name, attrs: {} })),
		...moduleCalls.map((m) => ({ id: rmodId(m.name), type: 'module', name: m.name, attrs: {} })),
		...ghosts.map((g) => ({ id: `ghost:${g.address}`, type: g.type, name: g.name, attrs: {} }))
	]);
	const macroEdges = $derived<Edge[]>([...deriveEdges(project.resources, rootDataSources), ...crossScopeEdges(project)]);

	// Auto-place any macro node without a stored position, preserving dragged ones.
	$effect(() => {
		if (macroNodes.every((n) => positions[n.id])) return;
		positions = layoutMissing(macroNodes, macroEdges, positions);
		savePos();
	});

	function applyLayout(mode: LayoutMode) {
		positions = layoutPositions(macroNodes, macroEdges, mode);
		savePos();
		resetView();
	}

	// Module boxes: geometry + expand state + interior child layout (resources
	// and data sources share the interior grid).
	const moduleBoxes = $derived(
		localModules.map((lm) => {
			const children = [...lm.resources, ...(lm.dataSources ?? [])];
			const interior = interiorLayout(children);
			const open = !!expanded[lm.name];
			const pos = positions[modId(lm.name)] ?? { x: 40, y: 40 };
			return {
				lm,
				name: lm.name,
				id: modId(lm.name),
				count: children.length,
				uses: callNamesFor(lm.name).length,
				open,
				interior,
				pos,
				w: open ? interior.w : NODE_W,
				h: open ? interior.h : NODE_H,
				selected: selected.includes(modId(lm.name))
			};
		})
	);

	// Root resource cards.
	const rootNodes = $derived(
		project.resources.map((r) => {
			const pos = positions[r.id] ?? { x: 40, y: 40 };
			return {
				...r,
				x: pos.x,
				y: pos.y,
				color: typeColor(r.type),
				letter: typeLetter(r.type),
				selected: selected.includes(r.id)
			};
		})
	);

	// Module call cards (non-expandable). A local call is tinted with the app
	// accent and a registry one violet, so at a glance you can tell a use of an
	// in-project module from a use of someone else's.
	const callNodes = $derived(
		moduleCalls.map((m) => {
			const pos = positions[rmodId(m.name)] ?? { x: 40, y: 40 };
			const folder = localFolder(m);
			return {
				...m,
				folder,
				color: folder ? accent : REGISTRY_MODULE_COLOR,
				id: rmodId(m.name),
				x: pos.x,
				y: pos.y,
				selected: selected.includes(rmodId(m.name))
			};
		})
	);

	// Root data-source cards (distinct color; rendered with kind "data").
	const rootDataNodes = $derived(
		rootDataSources.map((d) => {
			const pos = positions[d.id] ?? { x: 40, y: 40 };
			return {
				...d,
				x: pos.x,
				y: pos.y,
				color: DATA_COLOR,
				letter: typeLetter(d.type),
				selected: selected.includes(d.id)
			};
		})
	);

	// Absolute position of a module's interior child card.
	function childAbs(mb: (typeof moduleBoxes)[number], id: string) {
		const rel = mb.interior.pos[id] ?? { x: 0, y: 0 };
		return { x: mb.pos.x + rel.x, y: mb.pos.y + rel.y };
	}

	// Bounding boxes for every top-level node id (root resource or module group),
	// used to anchor edges. Anchors sit at the vertical centre of each box.
	const boxes = $derived.by(() => {
		const m = new Map<string, { x: number; y: number; w: number; h: number; color: string }>();
		for (const n of rootNodes) m.set(n.id, { x: n.x, y: n.y, w: NODE_W, h: NODE_H, color: n.color });
		for (const n of rootDataNodes) m.set(n.id, { x: n.x, y: n.y, w: NODE_W, h: NODE_H, color: n.color });
		for (const n of callNodes) m.set(n.id, { x: n.x, y: n.y, w: NODE_W, h: NODE_H, color: n.color });
		for (const mb of moduleBoxes) m.set(mb.id, { x: mb.pos.x, y: mb.pos.y, w: mb.w, h: mb.h, color: accent });
		return m;
	});

	// The ports are chosen by geometry (edgeGeometry, graph.ts) rather than fixed
	// at right→left, and the tip carries the heading the edge arrives on so the
	// arrowhead can point into the target — direction is no longer implied by
	// "edges always flow rightwards".
	const edges = $derived.by(() => {
		const b = boxes;
		const out: Array<Edge & { color: string; d: string; scope?: string; tip: { x: number; y: number; angle: number } }> = [];
		const push = (e: Edge, color: string, scope?: string, fromBox?: { x: number; y: number; w: number; h: number }, toBox?: { x: number; y: number; w: number; h: number }) => {
			const f = fromBox ?? b.get(e.from);
			const t = toBox ?? b.get(e.to);
			if (!f || !t) return;
			out.push({ ...e, color, scope, ...edgeGeometry(f, t) });
		};
		// Root ↔ root.
		for (const e of deriveEdges(project.resources, rootDataSources)) push(e, b.get(e.from)?.color ?? accent);
		// Cross-scope (root ↔ module, module ↔ module).
		for (const e of crossScopeEdges(project)) push(e, accent);
		// Interior edges for expanded modules (scoped, so depends edges know
		// which module to edit on click).
		for (const mb of moduleBoxes) {
			if (!mb.open) continue;
			for (const e of deriveEdges(mb.lm.resources, mb.lm.dataSources ?? [])) {
				const fa = childAbs(mb, e.from);
				const ta = childAbs(mb, e.to);
				push(e, mb.selected ? accent : '#5b6472', mb.name, { ...fa, w: NODE_W, h: NODE_H }, { ...ta, w: NODE_W, h: NODE_H });
			}
		}
		return out;
	});

	// Absolute boxes for module-interior cards (for the in-flight connection line).
	const interiorBoxes = $derived.by(() => {
		const m = new Map<string, { x: number; y: number; w: number; h: number }>();
		for (const mb of moduleBoxes) {
			if (!mb.open) continue;
			for (const child of [...mb.lm.resources, ...(mb.lm.dataSources ?? [])]) {
				m.set(child.id, { ...childAbs(mb, child.id), w: NODE_W, h: NODE_H });
			}
		}
		return m;
	});

	const tempPath = $derived.by(() => {
		if (!connecting) return null;
		const from = boxes.get(connecting.fromId) ?? interiorBoxes.get(connecting.fromId);
		if (!from) return null;
		return `M ${from.x + from.w} ${from.y + from.h / 2} L ${connecting.x} ${connecting.y}`;
	});

	// World extent large enough to hold every box with slack for panning.
	const worldSize = $derived.by(() => {
		let w = 1600,
			h = 1000;
		for (const box of boxes.values()) {
			w = Math.max(w, box.x + box.w + 400);
			h = Math.max(h, box.y + box.h + 400);
		}
		return { w, h };
	});

	function toWorld(e: MouseEvent) {
		const rect = canvasEl!.getBoundingClientRect();
		return { x: (e.clientX - rect.left - tx) / scale, y: (e.clientY - rect.top - ty) / scale };
	}

	// Start dragging a top-level box (root resource card or module group). A
	// Shift/Ctrl-click toggles the box in the selection instead; dragging a box
	// that is part of the selection moves the whole selection.
	function onBoxDown(e: MouseEvent, id: string) {
		if (e.button !== 0) return;
		e.stopPropagation();
		const box = boxes.get(id);
		if (!box) return;
		if (e.shiftKey || e.ctrlKey || e.metaKey) {
			selected = selected.includes(id) ? selected.filter((s) => s !== id) : [...selected, id];
			return;
		}
		if (!selected.includes(id)) selected = [id];
		const { x, y } = toWorld(e);
		dragMoved = false;
		dragging = {
			items: selected
				.filter((s) => boxes.has(s))
				.map((s) => {
					const b = boxes.get(s)!;
					return { id: s, ox: x - b.x, oy: y - b.y };
				})
		};
	}

	function onCanvasDown(e: MouseEvent) {
		if (e.button !== 0) return;
		if (e.shiftKey) {
			const { x, y } = toWorld(e);
			marquee = { x0: x, y0: y, x1: x, y1: y };
			return;
		}
		selected = [];
		panning = { sx: e.clientX, sy: e.clientY, ox: tx, oy: ty };
	}

	function snapTo(v: number): number {
		return snap ? Math.round(v / GRID) * GRID : v;
	}

	function onMove(e: MouseEvent) {
		if (!canvasEl) return;
		if (panning) {
			tx = panning.ox + (e.clientX - panning.sx);
			ty = panning.oy + (e.clientY - panning.sy);
			return;
		}
		const { x, y } = toWorld(e);
		if (dragging) {
			dragMoved = true;
			const next = { ...positions };
			for (const it of dragging.items) {
				next[it.id] = { x: Math.max(0, snapTo(x - it.ox)), y: Math.max(0, snapTo(y - it.oy)) };
			}
			positions = next;
		} else if (marquee) {
			marquee = { ...marquee, x1: x, y1: y };
		} else if (connecting) {
			connecting = { ...connecting, x, y };
		}
	}

	function onUp() {
		if (dragging) {
			savePos();
			dragging = null;
		}
		if (marquee) {
			// Select every top-level box intersecting the marquee rectangle.
			const x0 = Math.min(marquee.x0, marquee.x1),
				x1 = Math.max(marquee.x0, marquee.x1),
				y0 = Math.min(marquee.y0, marquee.y1),
				y1 = Math.max(marquee.y0, marquee.y1);
			const hits: string[] = [];
			for (const [id, b] of boxes) {
				if (id.startsWith('ghost:')) continue;
				if (b.x < x1 && b.x + b.w > x0 && b.y < y1 && b.y + b.h > y0) hits.push(id);
			}
			selected = hits;
			marquee = null;
		}
		if (connecting) connecting = null;
		panning = null;
	}

	// Bulk delete for the current selection: resources and data sources only
	// (module boxes have their own richer delete flows and are skipped).
	// Sequential, because each delete triggers a project refresh in the page.
	const deletableSelection = $derived(selected.filter((id) => lookupNode(id) !== null));
	async function bulkDelete() {
		const ids = deletableSelection;
		if (ids.length === 0) return;
		const names = ids.map((id) => lookupNode(id)!.addr).join(', ');
		if (!confirm(`Delete ${ids.length} block${ids.length === 1 ? '' : 's'} from the model?\n\n${names}`)) return;
		for (const id of ids) await onDeleteResource(id, undefined);
		selected = [];
	}

	function onCanvasKey(e: KeyboardEvent) {
		const t = e.target as HTMLElement | null;
		if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
		if (e.key === 'Escape') {
			if (renaming) renaming = null;
			else if (connecting) connecting = null;
			else if (marquee) marquee = null;
			else if (selected.length) selected = [];
			else onClose();
		} else if ((e.key === 'Delete' || e.key === 'Backspace') && deletableSelection.length > 0) {
			e.preventDefault();
			bulkDelete();
		}
	}

	function onWheel(e: WheelEvent) {
		e.preventDefault();
		const rect = canvasEl!.getBoundingClientRect();
		const mx = e.clientX - rect.left;
		const my = e.clientY - rect.top;
		const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
		const ns = Math.min(2.5, Math.max(0.2, scale * factor));
		const wx = (mx - tx) / scale;
		const wy = (my - ty) / scale;
		tx = mx - wx * ns;
		ty = my - wy * ns;
		scale = ns;
	}

	function zoomBy(f: number) {
		const ns = Math.min(2.5, Math.max(0.2, scale * f));
		if (!canvasEl) {
			scale = ns;
			return;
		}
		const cx = canvasEl.clientWidth / 2;
		const cy = canvasEl.clientHeight / 2;
		const wx = (cx - tx) / scale;
		const wy = (cy - ty) / scale;
		tx = cx - wx * ns;
		ty = cy - wy * ns;
		scale = ns;
	}

	function resetView() {
		tx = 0;
		ty = 0;
		scale = 1;
	}

	function onDotDown(e: MouseEvent, id: string, role: string, scope?: string) {
		e.stopPropagation();
		if (role !== 'out') return;
		const { x, y } = toWorld(e);
		connecting = { fromId: id, scope, x, y };
	}

	// Completing a connection adds a depends_on — only within one scope, since a
	// depends_on can't cross the module boundary.
	function onDotUp(e: MouseEvent, id: string, role: string, scope?: string) {
		e.stopPropagation();
		if (role === 'in' && connecting && connecting.fromId !== id && connecting.scope === scope) {
			const from = lookupNode(connecting.fromId, scope);
			const to = lookupNode(id, scope);
			if (from && to) {
				const deps = to.r.dependsOn ?? [];
				if (!deps.includes(from.addr)) onSetDependsOn(to.r.id, [...deps, from.addr], scope);
			}
		}
		connecting = null;
	}

	function onEdgeClick(e: Edge & { scope?: string }) {
		if (e.kind !== 'depends') return; // ref edges derive from attrs, not editable here
		const to = lookupNode(e.to, e.scope);
		const from = lookupNode(e.from, e.scope);
		if (!to || !from) return;
		onSetDependsOn(to.r.id, (to.r.dependsOn ?? []).filter((d) => baseAddr(d) !== from.addr), e.scope);
	}

	// A card's single click (open form) is delayed briefly so a double-click
	// (inline rename) can cancel it — otherwise the first click closes the
	// editor before the second lands.
	let cardClickTimer: ReturnType<typeof setTimeout> | null = null;
	function cardClick(e: MouseEvent, id: string, type: string, name: string, scope?: string) {
		if (e.shiftKey || e.ctrlKey || e.metaKey) return;
		if (dragMoved) {
			dragMoved = false;
			return;
		}
		if (renaming?.id === id) return;
		if (cardClickTimer) clearTimeout(cardClickTimer);
		cardClickTimer = setTimeout(() => onEditResource({ id, type, name, attrs: {} } as Resource, scope), 250);
	}

	// Inline rename: double-click opens an input over the card's name.
	function startRename(id: string, name: string, scope?: string) {
		if (cardClickTimer) clearTimeout(cardClickTimer);
		cardClickTimer = null;
		renaming = { id, scope, value: name };
	}
	function commitRename() {
		if (!renaming) return;
		const { id, scope } = renaming;
		const name = renaming.value.trim().replace(/[^a-zA-Z0-9_]+/g, '_');
		const cur = lookupNode(id, scope);
		renaming = null;
		if (name && cur && name !== cur.r.name) onRenameResource(id, name, scope);
	}
	function focusSelect(node: HTMLInputElement) {
		node.focus();
		node.select();
	}

	function switchProvider(key: string) {
		activeProvider = key;
		paletteTypes = [];
		paletteDataTypes = [];
		showPalette = true;
	}

	async function loadPaletteTypes() {
		if (!activeSource) return;
		loadingTypes = true;
		try {
			const res = await loadTypes(activeSource);
			paletteTypes = res.resourceTypes;
			paletteDataTypes = res.dataSourceTypes;
		} finally {
			loadingTypes = false;
		}
	}

	function openPalette() {
		showPalette = !showPalette;
		if (showPalette && paletteTypes.length === 0) loadPaletteTypes();
	}

	$effect(() => {
		activeProvider; // track
		paletteTypes = [];
		paletteDataTypes = [];
		if (showPalette) loadPaletteTypes();
	});

	const filteredTypes = $derived.by(() => {
		const pool = paletteKind === 'data' ? paletteDataTypes : paletteTypes;
		return typeFilter ? pool.filter((t) => t.includes(typeFilter)) : pool;
	});

	const gridBg = 'radial-gradient(circle, rgba(255,255,255,0.06) 1px, transparent 1px)';
</script>

{#snippet modBadge(counts: { create: number; update: number; replace: number; delete: number; total: number })}
	{#if counts.total > 0}
		<div class="font-mono" style="position:absolute;left:-7px;top:-9px;display:flex;gap:4px;padding:2px 6px;border-radius:8px;background:#0d1117;border:1px solid rgba(255,255,255,0.15);font-size:10px;font-weight:800;z-index:4;pointer-events:none;">
			{#if counts.create}<span style="color:{ACTION_COLOR.create}">+{counts.create}</span>{/if}
			{#if counts.update}<span style="color:{ACTION_COLOR.update}">~{counts.update}</span>{/if}
			{#if counts.replace}<span style="color:{ACTION_COLOR.replace}">±{counts.replace}</span>{/if}
			{#if counts.delete}<span style="color:{ACTION_COLOR.delete}">−{counts.delete}</span>{/if}
		</div>
	{/if}
{/snippet}

<!--
  multiPill — the ×N / ∀ multiplicity badge. Shared by resource/data cards and
  by all three module node shapes (expanded box, collapsed box, registry card),
  so a module's count/for_each reads exactly like a resource's on the canvas.
  Positioned absolute, so the caller's node must be the positioned ancestor.
-->
{#snippet multiPill(multi: { label: string; title: string } | undefined, color: string)}
	{#if multi}
		<div
			class="font-mono"
			title={multi.title}
			style="position:absolute;right:-7px;bottom:-9px;padding:2px 6px;border-radius:8px;background:#0d1117;border:1px solid rgba(255,255,255,0.18);font-size:10px;font-weight:800;color:{color};z-index:4;pointer-events:none;"
		>{multi.label}</div>
	{/if}
{/snippet}

{#snippet card(id: string, x: number, y: number, name: string, type: string, color: string, letter: string, selected: boolean, draggable: boolean, scope: string | undefined, change: string | undefined, multi: { label: string; title: string } | undefined, kind: ResourceKind = 'resource')}
	{@const isData = kind === 'data'}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		role="button"
		tabindex="-1"
		class="group"
		style="position:absolute;left:{x}px;top:{y}px;width:{NODE_W}px;height:{NODE_H}px;border-radius:{isData ? '18px' : '12px'};padding:10px 12px;background:{isData ? '#0e1917' : '#10151f'};border:{selected ? '1px solid ' + accent : change ? '1px solid ' + ACTION_COLOR[change] : isData ? '1.5px dashed rgba(86,212,188,0.45)' : '1px solid rgba(255,255,255,0.10)'};box-shadow:{selected ? '0 0 0 2px ' + accent + '55, 0 10px 28px rgba(0,0,0,0.45)' : change ? '0 0 0 1.5px ' + ACTION_COLOR[change] + '55, 0 6px 16px rgba(0,0,0,0.35)' : '0 6px 16px rgba(0,0,0,0.35)'};cursor:{draggable ? 'grab' : 'pointer'};z-index:2;"
		onmousedown={(e) => { if (draggable) { onBoxDown(e, id); } else { e.stopPropagation(); } }}
		onclick={(e) => cardClick(e, id, type, name, scope)}
		ondblclick={(e) => { e.stopPropagation(); startRename(id, name, scope); }}
	>
		<div class="flex items-center gap-2">
			<div style="width:22px;height:22px;border-radius:{isData ? '50%' : '6px'};background:{color};color:#0a0e15;font-size:11px;font-weight:700;display:flex;align-items:center;justify-content:center;flex:none;">{letter}</div>
			<div style="min-width:0;flex:1;">
				{#if renaming?.id === id}
					<!-- svelte-ignore a11y_autofocus -->
					<input
						class="font-mono"
						style="width:100%;font-size:12px;font-weight:600;color:#e6e9ef;background:rgba(0,0,0,0.45);border:1px solid {accent};border-radius:5px;padding:1px 5px;outline:none;"
						bind:value={renaming.value}
						use:focusSelect
						onmousedown={(e) => e.stopPropagation()}
						onclick={(e) => e.stopPropagation()}
						ondblclick={(e) => e.stopPropagation()}
						onkeydown={(e) => { e.stopPropagation(); if (e.key === 'Enter') commitRename(); else if (e.key === 'Escape') renaming = null; }}
						onblur={commitRename}
					/>
				{:else}
					<div style="font-size:13px;font-weight:600;color:#e6e9ef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;" title="double-click to rename">{name}</div>
				{/if}
				<div class="font-mono" style="font-size:10px;color:{isData ? 'rgba(86,212,188,0.75)' : '#6b7480'};white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{isData ? `data.${type}` : type}</div>
			</div>
		</div>
		<button
			title="Delete resource"
			aria-label="Delete resource"
			class="absolute right-1.5 top-1.5 leading-none text-white/30 hover:text-white/80"
			style="font-size:12px;"
			onmousedown={(e) => e.stopPropagation()}
			onclick={(e) => { e.stopPropagation(); onDeleteResource(id, scope); }}
		>✕</button>
		{#if change}
			<div
				title="planned: {change}{planAttrHint(scope, kind, type, name)}"
				style="position:absolute;left:-7px;top:-7px;width:16px;height:16px;border-radius:50%;background:{ACTION_COLOR[change]};color:#0a0e15;font-size:11px;font-weight:800;line-height:1;display:flex;align-items:center;justify-content:center;z-index:4;"
			>{ACTION_SYMBOL[change]}</div>
		{/if}
		{@render multiPill(multi, color)}
		<div title="Drag to connect (depends_on{scope ? ', within this module' : ''})" role="presentation" style="position:absolute;right:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{color};border:2px solid #0a0e15;cursor:crosshair;z-index:3;" onmousedown={(e) => onDotDown(e, id, 'out', scope)} onmouseup={(e) => onDotUp(e, id, 'out', scope)}></div>
		<div title="Drop a connection here" role="presentation" style="position:absolute;left:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{color};border:2px solid #0a0e15;cursor:crosshair;z-index:3;" onmousedown={(e) => onDotDown(e, id, 'in', scope)} onmouseup={(e) => onDotUp(e, id, 'in', scope)}></div>
		<!-- annotation badge: solid when a note exists, otherwise revealed on card hover -->
		<button
			class="absolute -bottom-2 -left-2 flex h-[18px] w-[18px] items-center justify-center rounded-full transition-opacity {notes[id] ? 'opacity-100' : 'opacity-0 group-hover:opacity-70 hover:!opacity-100'}"
			style="background:#0d1117;border:1px solid {notes[id] ? accent : 'rgba(255,255,255,0.22)'};color:{notes[id] ? accent : '#8891a0'};z-index:4;"
			title={notes[id] ?? 'Add a note'}
			aria-label={notes[id] ? 'Edit note' : 'Add note'}
			onmousedown={(e) => e.stopPropagation()}
			onclick={(e) => openNote(e, id)}
		><MessageSquare size={10} /></button>
	</div>
{/snippet}

<svelte:window onmousemove={onMove} onmouseup={onUp} onkeydown={onCanvasKey} />

<div
	class="flex h-full w-full min-h-0"
	style="background:#0a0e15;color:#c9d3dc;font-family:'Inter',system-ui,sans-serif;overflow:hidden;user-select:none;"
>
	<!-- Canvas -->
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		bind:this={canvasEl}
		role="application"
		tabindex="-1"
		class="relative flex-1 overflow-hidden"
		class:cursor-grabbing={panning}
		style="background-image:{gridBg};background-size:{22 * scale}px {22 * scale}px;background-position:{tx}px {ty}px;"
		onmousedown={onCanvasDown}
		onwheel={onWheel}
	>
		<!-- layout presets -->
		{#if macroNodes.length > 1}
			<div class="absolute left-1/2 top-12 z-[5] flex -translate-x-1/2 items-center gap-0.5" style="background:rgba(13,17,23,0.85);backdrop-filter:blur(8px);padding:4px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);box-shadow:0 8px 24px rgba(0,0,0,0.4);">
				<span class="px-1.5 text-[10px] font-semibold uppercase tracking-wide text-white/25">Arrange</span>
				{#each LAYOUTS as l (l.mode)}
					{@const Icon = LAYOUT_ICONS[l.mode]}
					<button
						class="flex items-center gap-1 rounded-md px-2 py-1.5 text-[11px] font-semibold text-white/60 hover:bg-white/10 hover:text-white/90"
						onclick={() => applyLayout(l.mode)}
						title={l.hint}
					>
						<Icon size={13} />{l.label}
					</button>
				{/each}
			</div>
		{/if}

		<!-- palette (add resource) -->
		<div class="absolute left-4 top-3.5 z-[5]" style="background:rgba(13,17,23,0.85);backdrop-filter:blur(8px);border-radius:11px;border:1px solid rgba(255,255,255,0.08);box-shadow:0 8px 24px rgba(0,0,0,0.4);">
			<div class="flex items-center gap-1.5 p-1.5">
				<button
					class="flex h-[34px] items-center gap-1.5 rounded-lg px-2.5 text-[13px] font-semibold"
					style="background:{accent}22;border:1px solid {accent}55;color:{accent};"
					onclick={openPalette}
					disabled={!activeSource}
					title="Add a resource"
				>
					<Plus size={15} /> Add
				</button>
			</div>
			{#if showPalette}
				<div class="border-t border-white/10 p-2" style="width:220px;">
					<div class="mb-1.5 flex gap-1">
						<button
							class="flex-1 rounded-md px-2 py-1 text-[11px] font-semibold"
							style="background:{paletteKind === 'resource' ? accent + '22' : 'transparent'};color:{paletteKind === 'resource' ? accent : '#8b95a5'};border:1px solid {paletteKind === 'resource' ? accent + '55' : 'transparent'};"
							onclick={() => (paletteKind = 'resource')}
						>resources</button>
						<button
							class="flex-1 rounded-md px-2 py-1 text-[11px] font-semibold"
							style="background:{paletteKind === 'data' ? DATA_COLOR + '22' : 'transparent'};color:{paletteKind === 'data' ? DATA_COLOR : '#8b95a5'};border:1px solid {paletteKind === 'data' ? DATA_COLOR + '55' : 'transparent'};"
							onclick={() => (paletteKind = 'data')}
						>data</button>
					</div>
					<div class="mb-1.5 flex items-center gap-1.5 rounded-md bg-black/30 px-2 py-1">
						<Search size={12} class="text-white/40" />
						<input class="w-full bg-transparent text-[11px] text-white/80 outline-none" placeholder="filter types…" bind:value={typeFilter} />
					</div>
					{#if loadingTypes}
						<div class="flex items-center gap-1.5 px-1 py-2 text-[11px] text-white/40"><Loader2 size={12} class="animate-spin" /> loading…</div>
					{:else}
						<div class="max-h-56 overflow-y-auto">
							{#each filteredTypes as t (t)}
								<button
									class="block w-full truncate rounded px-2 py-1 text-left font-mono text-[11px] text-white/70 hover:bg-white/10"
									onclick={() => onAddType(t, activeSource, paletteKind)}
									title="Add {paletteKind === 'data' ? 'data source' : ''} {t}"
								>{t}</button>
							{/each}
							{#if filteredTypes.length === 0}
								<p class="px-2 py-1 text-[11px] text-white/30">no matching types</p>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- provider pills + close -->
		<div class="absolute right-4 top-3.5 z-[5] flex items-center gap-2">
			<div class="flex gap-0.5" style="background:rgba(13,17,23,0.85);backdrop-filter:blur(8px);padding:4px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);">
				{#each providerKeys as key (key)}
					<button
						class="rounded-md px-2.5 py-1.5 text-[11px] font-bold"
						style="background:{key === activeProvider ? accent : 'transparent'};color:{key === activeProvider ? '#0a0e15' : '#8b95a5'};"
						onclick={() => switchProvider(key)}
						title="Browse {key} resources to add"
					>{key}</button>
				{/each}
				{#if providerKeys.length === 0}
					<span class="px-2 py-1.5 text-[11px] text-white/30">no providers</span>
				{/if}
			</div>
			<button class="rounded-lg p-2 text-white/50 hover:bg-white/10 hover:text-white/90" style="background:rgba(13,17,23,0.85);border:1px solid rgba(255,255,255,0.08);" onclick={onClose} title="Close editor" aria-label="Close editor">
				<X size={16} />
			</button>
		</div>

		<!-- hint -->
		<div class="pointer-events-none absolute bottom-3.5 left-4 z-[4] text-[11px]" style="color:rgba(255,255,255,0.32);">
			Drag empty space to pan · scroll to zoom · Shift+drag for marquee select (Shift+click adds) · Del deletes selection · double-click a name to rename · drag the right dot to another node for depends_on · Esc clears / closes
		</div>

		<!-- world layer (pans / zooms together) -->
		<div style="position:absolute;left:0;top:0;transform:translate({tx}px,{ty}px) scale({scale});transform-origin:0 0;">
			<!-- module groups -->
			{#each moduleBoxes as mb (mb.id)}
				{#if mb.open}
					<!-- expanded container background -->
					<!-- svelte-ignore a11y_click_events_have_key_events -->
					<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
					<div
						role="button"
						tabindex="-1"
						style="position:absolute;left:{mb.pos.x}px;top:{mb.pos.y}px;width:{mb.w}px;height:{mb.h}px;border-radius:14px;background:rgba(124,162,247,0.05);border:1.5px dashed {mb.selected ? accent : 'rgba(124,162,247,0.35)'};z-index:1;"
						onmousedown={(e) => onBoxDown(e, mb.id)}
					>
						<div
							class="flex items-center gap-1.5"
							style="height:{MOD_HEADER_H}px;padding:0 10px;cursor:grab;"
							onclick={(e) => { if (e.shiftKey || e.ctrlKey || e.metaKey) return; if (dragMoved) { dragMoved = false; return; } toggleExpand(mb.name); }}
							role="presentation"
						>
							<FolderOpen size={14} style="color:{accent};flex:none;" />
							<span class="font-mono" style="font-size:12px;font-weight:700;color:#e6e9ef;">modules/{mb.name}</span>
							<span style="font-size:10px;color:#6b7480;">{mb.count} block{mb.count === 1 ? '' : 's'} · {mb.uses} use{mb.uses === 1 ? '' : 's'}</span>
							<ChevronDown size={13} style="color:#6b7480;margin-left:auto;" />
						</div>
						{#if mb.count === 0}
							<div class="pointer-events-none absolute inset-x-0 flex justify-center text-[11px] text-white/25" style="top:{MOD_HEADER_H + 12}px;">empty module</div>
						{/if}
						{@render modBadge(moduleChangeCounts(mb.name))}
					</div>
				{:else}
					<!-- collapsed module node -->
					<!-- svelte-ignore a11y_click_events_have_key_events -->
					<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
					<div
						role="button"
						tabindex="-1"
						style="position:absolute;left:{mb.pos.x}px;top:{mb.pos.y}px;width:{NODE_W}px;height:{NODE_H}px;border-radius:12px;padding:10px 12px;background:#121826;border:1px solid {mb.selected ? accent : 'rgba(124,162,247,0.30)'};box-shadow:{mb.selected ? '0 0 0 2px ' + accent + '55, 0 10px 28px rgba(0,0,0,0.45)' : '0 6px 16px rgba(0,0,0,0.35)'};cursor:grab;z-index:2;"
						onmousedown={(e) => onBoxDown(e, mb.id)}
						onclick={(e) => { if (e.shiftKey || e.ctrlKey || e.metaKey) return; if (dragMoved) { dragMoved = false; return; } toggleExpand(mb.name); }}
					>
						<div class="flex items-center gap-2">
							<div style="width:22px;height:22px;border-radius:6px;background:{accent}22;color:{accent};display:flex;align-items:center;justify-content:center;flex:none;"><Folder size={13} /></div>
							<div style="min-width:0;">
								<div style="font-size:13px;font-weight:700;color:#e6e9ef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{mb.name}</div>
								<div class="font-mono" style="font-size:10px;color:#6b7480;">modules/ · {mb.count} block{mb.count === 1 ? '' : 's'}</div>
							</div>
							<ChevronRight size={14} style="color:#6b7480;margin-left:auto;flex:none;" />
						</div>
						<div title="Drag to connect (depends_on)" role="presentation" style="position:absolute;right:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{accent};border:2px solid #0a0e15;z-index:3;"></div>
						<div role="presentation" style="position:absolute;left:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{accent};border:2px solid #0a0e15;z-index:3;"></div>
						{@render modBadge(moduleChangeCounts(mb.name))}
						{#if mb.uses === 0}
							<div
								class="font-mono"
								title="Defined but never called — add a module call to use it"
								style="position:absolute;right:-7px;bottom:-9px;padding:2px 6px;border-radius:8px;background:#0d1117;border:1px dashed rgba(255,255,255,0.22);font-size:10px;font-weight:700;color:#6b7480;z-index:4;pointer-events:none;"
							>unused</div>
						{/if}
					</div>
				{/if}
			{/each}

			<!-- edges: rendered after (and at the same z as) the module containers
			     so interior depends edges stay clickable above a module's
			     background; cards sit higher (z2) and are unaffected. -->
			<svg style="position:absolute;left:0;top:0;overflow:visible;pointer-events:none;z-index:1;" width={worldSize.w} height={worldSize.h}>
				{#each edges as e (e.id)}
					<path d={e.d} stroke="transparent" stroke-width="14" fill="none" style="pointer-events:{e.kind === 'depends' ? 'auto' : 'none'};cursor:{e.kind === 'depends' ? 'pointer' : 'default'};" onmousedown={(ev) => ev.stopPropagation()} onclick={() => onEdgeClick(e)} role="presentation" />
				{/each}
				{#each edges as e (e.id)}
					<path d={e.d} stroke={e.color} stroke-width="2" fill="none" stroke-dasharray={e.kind === 'depends' ? '5 4' : 'none'} opacity={e.kind === 'depends' ? '0.75' : '1'} style="pointer-events:none;" />
					<!-- Arrowhead drawn per edge rather than with a <marker>: each edge has
					     its own colour, and marker `context-stroke` is not old enough to
					     rely on. The triangle points +x and is rotated onto the heading. -->
					<polygon
						points="0,0 -9,-4.5 -9,4.5"
						fill={e.color}
						opacity={e.kind === 'depends' ? '0.75' : '1'}
						transform="translate({e.tip.x} {e.tip.y}) rotate({e.tip.angle})"
						style="pointer-events:none;"
					/>
				{/each}
				{#if tempPath}
					<path d={tempPath} stroke={accent} stroke-width="2" stroke-dasharray="6 4" fill="none" style="pointer-events:none;" />
				{/if}
				{#if marquee}
					<rect
						x={Math.min(marquee.x0, marquee.x1)}
						y={Math.min(marquee.y0, marquee.y1)}
						width={Math.abs(marquee.x1 - marquee.x0)}
						height={Math.abs(marquee.y1 - marquee.y0)}
						fill="{accent}18"
						stroke={accent}
						stroke-width="1.5"
						stroke-dasharray="5 4"
						style="pointer-events:none;"
					/>
				{/if}
			</svg>

			<!--
			  Module CALL cards — one per `module "<name>"` block, local and
			  registry alike. The multiplicity pill lives here (count/for_each is
			  a property of the call), and a local call is joined by an edge to
			  the definition box it uses.
			-->
			{#each callNodes as node (node.id)}
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
				<div
					role="button"
					tabindex="-1"
					style="position:absolute;left:{node.x}px;top:{node.y}px;width:{NODE_W}px;height:{NODE_H}px;border-radius:12px;padding:10px 12px;background:{node.folder ? '#121826' : '#171226'};border:1px solid {node.selected ? accent : node.color + '59'};box-shadow:{node.selected ? '0 0 0 2px ' + accent + '55, 0 10px 28px rgba(0,0,0,0.45)' : '0 6px 16px rgba(0,0,0,0.35)'};cursor:grab;z-index:2;"
					onmousedown={(e) => onBoxDown(e, node.id)}
					onclick={(e) => { if (e.shiftKey || e.ctrlKey || e.metaKey) return; if (dragMoved) { dragMoved = false; return; } onEditModuleCall?.(node.name); }}
					title="module.{node.name} — {node.source}{node.version ? ' @ ' + node.version : ''}"
				>
					<div class="flex items-center gap-2">
						<div style="width:22px;height:22px;border-radius:6px;background:{node.color}22;color:{node.color};display:flex;align-items:center;justify-content:center;flex:none;"><Package size={13} /></div>
						<div style="min-width:0;">
							<div style="font-size:13px;font-weight:700;color:#e6e9ef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{node.name}</div>
							<div class="font-mono" style="font-size:10px;color:{node.color}a6;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{node.source}</div>
						</div>
					</div>
					<div role="presentation" style="position:absolute;right:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{node.color};border:2px solid #0a0e15;z-index:3;"></div>
					<div role="presentation" style="position:absolute;left:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{node.color};border:2px solid #0a0e15;z-index:3;"></div>
					{@render modBadge(callChangeCounts(node.name))}
					{@render multiPill(multiBadge(node, rootScope), node.color)}
				</div>
			{/each}

			<!-- root resource nodes -->
			{#each rootNodes as node (node.id)}
				{@render card(node.id, node.x, node.y, node.name, node.type, node.color, node.letter, node.selected, true, undefined, changeByAddr.get(`${node.type}.${node.name}`), multiBadge(node, rootScope))}
			{/each}

			<!-- root data-source nodes -->
			{#each rootDataNodes as node (node.id)}
				{@render card(node.id, node.x, node.y, node.name, node.type, node.color, node.letter, node.selected, true, undefined, changeByAddr.get(`data.${node.type}.${node.name}`), multiBadge(node, rootScope), 'data')}
			{/each}

			<!-- expanded modules' interior resource + data-source nodes -->
			{#each moduleBoxes as mb (mb.id)}
				{#if mb.open}
					{#each mb.lm.resources as r (r.id)}
						{@const c = childAbs(mb, r.id)}
						{@render card(r.id, c.x, c.y, r.name, r.type, typeColor(r.type), typeLetter(r.type), false, false, mb.name, changeForChild(mb.name, `${r.type}.${r.name}`), multiBadge(r, moduleScope(mb.lm)))}
					{/each}
					{#each mb.lm.dataSources ?? [] as d (d.id)}
						{@const c = childAbs(mb, d.id)}
						{@render card(d.id, c.x, c.y, d.name, d.type, DATA_COLOR, typeLetter(d.type), false, false, mb.name, changeForChild(mb.name, `data.${d.type}.${d.name}`), multiBadge(d, moduleScope(mb.lm)), 'data')}
					{/each}
				{/if}
			{/each}

			<!-- ghost cards for planned root deletes (resource already gone from the model) -->
			{#each ghosts as g (g.address)}
				{@const pos = positions[`ghost:${g.address}`] ?? { x: 40, y: 40 }}
				<div
					title="planned: delete {g.address}"
					style="position:absolute;left:{pos.x}px;top:{pos.y}px;width:{NODE_W}px;height:{NODE_H}px;border-radius:12px;padding:10px 12px;background:rgba(248,81,73,0.06);border:1.5px dashed {ACTION_COLOR.delete};opacity:0.9;z-index:2;pointer-events:none;"
				>
					<div class="flex items-center gap-2">
						<div style="width:22px;height:22px;border-radius:6px;background:{ACTION_COLOR.delete}22;color:{ACTION_COLOR.delete};font-size:13px;font-weight:800;display:flex;align-items:center;justify-content:center;flex:none;">−</div>
						<div style="min-width:0;">
							<div style="font-size:13px;font-weight:600;color:#e6e9ef;text-decoration:line-through;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{g.name}</div>
							<div class="font-mono" style="font-size:10px;color:#6b7480;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{g.type}</div>
						</div>
					</div>
					<div style="position:absolute;left:-7px;top:-7px;width:16px;height:16px;border-radius:50%;background:{ACTION_COLOR.delete};color:#0a0e15;font-size:11px;font-weight:800;line-height:1;display:flex;align-items:center;justify-content:center;">−</div>
				</div>
			{/each}
		</div>

		<!-- plan legend -->
		{#if planCounts.total > 0}
			<div class="absolute bottom-12 left-1/2 z-[5] flex -translate-x-1/2 items-center gap-2.5" style="background:rgba(13,17,23,0.85);backdrop-filter:blur(8px);padding:5px 12px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);box-shadow:0 8px 24px rgba(0,0,0,0.4);">
				<span class="text-[10px] font-semibold uppercase tracking-wide text-white/25">last plan</span>
				<span class="font-mono text-[11px] font-bold" style="color:{ACTION_COLOR.create}">+{planCounts.create} create</span>
				<span class="font-mono text-[11px] font-bold" style="color:{ACTION_COLOR.update}">~{planCounts.update} update</span>
				<span class="font-mono text-[11px] font-bold" style="color:{ACTION_COLOR.replace}">±{planCounts.replace} replace</span>
				<span class="font-mono text-[11px] font-bold" style="color:{ACTION_COLOR.delete}">−{planCounts.delete} delete</span>
			</div>
		{/if}

		<!-- zoom controls -->
		<div class="absolute bottom-3.5 right-4 z-[5] flex items-center gap-1" style="background:rgba(13,17,23,0.85);backdrop-filter:blur(8px);padding:4px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);">
			<button
				class="rounded-md px-2 py-1 hover:bg-white/10"
				style="color:{snap ? accent : 'rgba(255,255,255,0.6)'};"
				onclick={toggleSnap}
				title="Snap dragged nodes to the grid ({snap ? 'on' : 'off'})"
				aria-label="Toggle snap to grid"
				aria-pressed={snap}
			><Magnet size={14} /></button>
			<button class="rounded-md px-2 py-1 text-white/60 hover:bg-white/10 hover:text-white/90" onclick={() => zoomBy(1 / 1.2)} title="Zoom out" aria-label="Zoom out"><Minus size={14} /></button>
			<button class="min-w-[42px] rounded-md px-1 py-1 text-[11px] text-white/60 hover:bg-white/10 hover:text-white/90" onclick={resetView} title="Reset view">{Math.round(scale * 100)}%</button>
			<button class="rounded-md px-2 py-1 text-white/60 hover:bg-white/10 hover:text-white/90" onclick={() => zoomBy(1.2)} title="Zoom in" aria-label="Zoom in"><Plus size={14} /></button>
		</div>

		{#if macroNodes.length === 0}
			<div class="pointer-events-none absolute inset-0 grid place-items-center text-sm text-white/30">
				No resources yet — click <span class="mx-1 font-semibold" style="color:{accent}">Add</span> to create one.
			</div>
		{/if}
	</div>

	<!-- Per-node annotation editor (fixed overlay, anchored at the click) -->
	{#if editingNote}
		<!-- backdrop: click outside saves & closes -->
		<button
			class="fixed inset-0 z-[60] cursor-default"
			aria-label="Close note editor"
			onclick={commitNote}
		></button>
		<div
			class="fixed z-[61] w-[260px] rounded-lg border border-white/10 p-2.5 shadow-2xl"
			style="left:{Math.min(editingNote.sx, (typeof window !== 'undefined' ? window.innerWidth : 1200) - 276)}px;top:{Math.min(editingNote.sy, (typeof window !== 'undefined' ? window.innerHeight : 800) - 170)}px;background:#0d1117;"
		>
			<div class="mb-1.5 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-white/40">
				<MessageSquare size={12} /> Note
			</div>
			<!-- svelte-ignore a11y_autofocus -->
			<textarea
				autofocus
				class="min-h-[68px] w-full resize-y rounded-md border border-white/10 bg-black/40 px-2 py-1.5 text-[13px] text-[#e6e9ef] outline-none focus:border-[color:var(--al-accent,#7aa2f7)]"
				placeholder="e.g. legacy, migrate soon"
				bind:value={editingNote.value}
				onkeydown={(e) => { e.stopPropagation(); if (e.key === 'Escape') editingNote = null; else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) commitNote(); }}
			></textarea>
			<div class="mt-2 flex items-center justify-between">
				{#if notes[editingNote.id]}
					<button
						class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-white/40 hover:text-[#f85149]"
						onclick={() => { if (editingNote) { editingNote.value = ''; commitNote(); } }}
					><Trash2 size={12} /> Delete</button>
				{:else}
					<span></span>
				{/if}
				<div class="flex items-center gap-1.5">
					<button class="rounded-md px-2 py-1 text-[11px] text-white/50 hover:text-white/90" onclick={() => (editingNote = null)}>Cancel</button>
					<button class="rounded-md px-2.5 py-1 text-[11px] font-semibold" style="background:{accent}22;border:1px solid {accent}55;color:{accent};" onclick={commitNote}>Save</button>
				</div>
			</div>
			<p class="mt-1.5 text-[10px] text-white/25">Notes are local documentation — they never appear in the generated HCL.</p>
		</div>
	{/if}

	<!-- Live HCL panel. max-w caps it on a narrow window: at a fixed 440px it ate
	     the whole row and the canvas (flex-1) collapsed to zero width, so opening
	     the editor showed the HCL pane and no graph at all. -->
	<div class="w-[440px] max-w-[45%] flex-none" style="border-left:1px solid rgba(255,255,255,0.08);min-height:0;">
		<HclView {hcl}>
			{#snippet right()}
				{#if onPlan}
					<button
						class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] font-semibold disabled:opacity-40"
						style="background:{accent}22;border:1px solid {accent}55;color:{accent};"
						onclick={onPlan}
						disabled={planning}
						title="Plan and color nodes by pending change"
					>
						{#if planning}<Loader2 size={12} class="animate-spin" />{:else}<Play size={12} />{/if} Plan
					</button>
				{/if}
				<div class="font-mono" style="font-size:11px;color:#5b6472;">{providerFooter}</div>
			{/snippet}
		</HclView>
	</div>
</div>
