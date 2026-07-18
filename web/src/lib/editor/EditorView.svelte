<script lang="ts">
	import type { Project, Resource } from '$lib/types';
	import type { PlanChange } from '$lib/api/client';
	import {
		deriveEdges, crossScopeEdges, interiorLayout, modId, typeColor, typeLetter,
		layoutPositions, layoutMissing, LAYOUTS,
		NODE_W, NODE_H, DOT_Y, MOD_HEADER_H, ACTION_COLOR, ACTION_SYMBOL,
		type Edge, type LayoutMode
	} from './graph';
	import HclView from './HclView.svelte';
	import { X, Plus, Minus, Search, Loader2, Network, Rows3, LayoutGrid, CircleDashed, Group, Folder, FolderOpen, ChevronRight, ChevronDown, Play } from 'lucide-svelte';

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
		onClose
	}: {
		project: Project;
		hcl: string;
		accent?: string;
		planChanges?: PlanChange[];
		planning?: boolean;
		onPlan?: () => void;
		loadTypes: (source: string) => Promise<string[]>;
		onAddType: (type: string, providerSource: string) => void;
		onEditResource: (r: Resource, module?: string) => void;
		onDeleteResource: (id: string, module?: string) => void;
		onRenameResource: (id: string, name: string, module?: string) => void;
		onSetDependsOn: (id: string, deps: string[], module?: string) => void;
		onClose: () => void;
	} = $props();

	let canvasEl = $state<HTMLDivElement>();
	const posKey = $derived(`arcloom.pos.${project.id}`);
	const expandKey = $derived(`arcloom.modexp.${project.id}`);

	let positions = $state<Record<string, { x: number; y: number }>>({});
	let expanded = $state<Record<string, boolean>>({});
	let dragging = $state<{ id: string; ox: number; oy: number } | null>(null);
	let dragMoved = $state(false); // distinguishes a drag from a click
	let connecting = $state<{ fromId: string; x: number; y: number } | null>(null);
	let selectedId = $state<string | null>(null);

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
	let typeFilter = $state('');
	let loadingTypes = $state(false);
	let showPalette = $state(false);

	const activeSource = $derived(
		project.providers.find((p) => p.key === activeProvider)?.source ?? project.providers[0]?.source ?? ''
	);
	const providerFooter = $derived((activeProvider || 'no') + ' provider');

	const localModules = $derived(project.localModules ?? []);

	// --- Plan overlay: map last plan's changes onto canvas nodes by address. ---
	const changeByAddr = $derived.by(() => {
		const m = new Map<string, string>();
		for (const c of planChanges) if (c.mode === 'managed') m.set(c.address, c.action);
		return m;
	});
	// Planned deletes at root scope have no model node left to color; render
	// them as non-interactive ghost cards so the disappearance is visible.
	const ghosts = $derived(
		planChanges.filter(
			(c) =>
				c.action === 'delete' && c.mode === 'managed' && !c.moduleAddress &&
				!project.resources.some((r) => `${r.type}.${r.name}` === c.address)
		)
	);
	// Aggregate pending actions per local module (covers nested addresses too).
	function moduleChangeCounts(name: string) {
		const pref = `module.${name}`;
		const agg = { create: 0, update: 0, replace: 0, delete: 0, total: 0 };
		for (const c of planChanges) {
			if (c.mode !== 'managed') continue;
			if (c.moduleAddress !== pref && !c.moduleAddress?.startsWith(pref + '.')) continue;
			if (c.action in agg) {
				agg[c.action as keyof typeof agg]++;
				agg.total++;
			}
		}
		return agg;
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
	function toggleExpand(name: string) {
		expanded = { ...expanded, [name]: !expanded[name] };
		localStorage.setItem(expandKey, JSON.stringify(expanded));
	}

	// Macro nodes are the things laid out at the top level: every root resource
	// plus one pseudo-node per local module, plus a ghost per planned root
	// delete (so it gets an auto position). Layout helpers only read id/type.
	const macroNodes = $derived<Resource[]>([
		...project.resources,
		...localModules.map((lm) => ({ id: modId(lm.name), type: 'module', name: lm.name, attrs: {} })),
		...ghosts.map((g) => ({ id: `ghost:${g.address}`, type: g.type, name: g.name, attrs: {} }))
	]);
	const macroEdges = $derived<Edge[]>([...deriveEdges(project.resources), ...crossScopeEdges(project)]);

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

	// Module boxes: geometry + expand state + interior child layout.
	const moduleBoxes = $derived(
		localModules.map((lm) => {
			const interior = interiorLayout(lm.resources);
			const open = !!expanded[lm.name];
			const pos = positions[modId(lm.name)] ?? { x: 40, y: 40 };
			return {
				lm,
				name: lm.name,
				id: modId(lm.name),
				count: lm.resources.length,
				open,
				interior,
				pos,
				w: open ? interior.w : NODE_W,
				h: open ? interior.h : NODE_H,
				selected: modId(lm.name) === selectedId
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
				selected: r.id === selectedId
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
		for (const mb of moduleBoxes) m.set(mb.id, { x: mb.pos.x, y: mb.pos.y, w: mb.w, h: mb.h, color: accent });
		return m;
	});

	function edgePath(from: { x: number; y: number; w: number; h: number }, to: { x: number; y: number; w: number; h: number }): string {
		const x1 = from.x + from.w,
			y1 = from.y + from.h / 2,
			x2 = to.x,
			y2 = to.y + to.h / 2;
		const mid = (x1 + x2) / 2;
		return `M ${x1} ${y1} C ${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`;
	}

	const edges = $derived.by(() => {
		const b = boxes;
		const out: Array<Edge & { color: string; d: string }> = [];
		const push = (e: Edge, color: string, fromBox?: { x: number; y: number; w: number; h: number }, toBox?: { x: number; y: number; w: number; h: number }) => {
			const f = fromBox ?? b.get(e.from);
			const t = toBox ?? b.get(e.to);
			if (!f || !t) return;
			out.push({ ...e, color, d: edgePath(f, t) });
		};
		// Root ↔ root.
		for (const e of deriveEdges(project.resources)) push(e, b.get(e.from)?.color ?? accent);
		// Cross-scope (root ↔ module, module ↔ module).
		for (const e of crossScopeEdges(project)) push(e, accent);
		// Interior edges for expanded modules.
		for (const mb of moduleBoxes) {
			if (!mb.open) continue;
			for (const e of deriveEdges(mb.lm.resources)) {
				const fa = childAbs(mb, e.from);
				const ta = childAbs(mb, e.to);
				push(e, mb.selected ? accent : '#5b6472', { ...fa, w: NODE_W, h: NODE_H }, { ...ta, w: NODE_W, h: NODE_H });
			}
		}
		return out;
	});

	const tempPath = $derived.by(() => {
		if (!connecting) return null;
		const from = boxes.get(connecting.fromId);
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

	// Start dragging a top-level box (root resource card or module group).
	function onBoxDown(e: MouseEvent, id: string) {
		if (e.button !== 0) return;
		e.stopPropagation();
		const box = boxes.get(id);
		if (!box) return;
		const { x, y } = toWorld(e);
		selectedId = id;
		dragMoved = false;
		dragging = { id, ox: x - box.x, oy: y - box.y };
	}

	function onCanvasDown(e: MouseEvent) {
		if (e.button !== 0) return;
		selectedId = null;
		panning = { sx: e.clientX, sy: e.clientY, ox: tx, oy: ty };
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
			positions = { ...positions, [dragging.id]: { x: Math.max(0, x - dragging.ox), y: Math.max(0, y - dragging.oy) } };
		} else if (connecting) {
			connecting = { ...connecting, x, y };
		}
	}

	function onUp() {
		if (dragging) {
			savePos();
			dragging = null;
		}
		if (connecting) connecting = null;
		panning = null;
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

	function onDotDown(e: MouseEvent, id: string, role: string) {
		e.stopPropagation();
		if (role !== 'out') return;
		const { x, y } = toWorld(e);
		connecting = { fromId: id, x, y };
	}

	function onDotUp(e: MouseEvent, id: string, role: string) {
		e.stopPropagation();
		if (role === 'in' && connecting && connecting.fromId !== id) {
			const from = project.resources.find((r) => r.id === connecting!.fromId);
			const to = project.resources.find((r) => r.id === id);
			if (from && to) {
				const addr = `${from.type}.${from.name}`;
				const deps = to.dependsOn ?? [];
				if (!deps.includes(addr)) onSetDependsOn(to.id, [...deps, addr]);
			}
		}
		connecting = null;
	}

	function onEdgeClick(e: Edge) {
		if (e.kind !== 'depends') return; // only root depends_on edges are editable
		const to = project.resources.find((r) => r.id === e.to);
		const from = project.resources.find((r) => r.id === e.from);
		if (!to || !from) return;
		const addr = `${from.type}.${from.name}`;
		onSetDependsOn(to.id, (to.dependsOn ?? []).filter((d) => d.split('.').slice(0, 2).join('.') !== addr));
	}

	function rename(id: string, name: string, scope?: string) {
		const next = window.prompt('Resource name', name);
		if (next && next.trim()) onRenameResource(id, next.trim().replace(/[^a-zA-Z0-9_]+/g, '_'), scope);
	}

	function switchProvider(key: string) {
		activeProvider = key;
		paletteTypes = [];
		showPalette = true;
	}

	async function loadPaletteTypes() {
		if (!activeSource) return;
		loadingTypes = true;
		try {
			paletteTypes = await loadTypes(activeSource);
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
		if (showPalette) loadPaletteTypes();
	});

	const filteredTypes = $derived(
		typeFilter ? paletteTypes.filter((t) => t.includes(typeFilter)) : paletteTypes
	);

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

{#snippet card(id: string, x: number, y: number, name: string, type: string, color: string, letter: string, selected: boolean, connectable: boolean, scope: string | undefined, change: string | undefined)}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<div
		role="button"
		tabindex="-1"
		style="position:absolute;left:{x}px;top:{y}px;width:{NODE_W}px;height:{NODE_H}px;border-radius:12px;padding:10px 12px;background:#10151f;border:1px solid {selected ? accent : change ? ACTION_COLOR[change] : 'rgba(255,255,255,0.10)'};box-shadow:{selected ? '0 0 0 2px ' + accent + '55, 0 10px 28px rgba(0,0,0,0.45)' : change ? '0 0 0 1.5px ' + ACTION_COLOR[change] + '55, 0 6px 16px rgba(0,0,0,0.35)' : '0 6px 16px rgba(0,0,0,0.35)'};cursor:{connectable ? 'grab' : 'pointer'};z-index:2;"
		onmousedown={(e) => { if (connectable) { onBoxDown(e, id); } else { e.stopPropagation(); } }}
		onclick={() => { if (dragMoved) { dragMoved = false; return; } onEditResource({ id, type, name, attrs: {} } as Resource, scope); }}
		ondblclick={(e) => { e.stopPropagation(); rename(id, name, scope); }}
	>
		<div class="flex items-center gap-2">
			<div style="width:22px;height:22px;border-radius:6px;background:{color};color:#0a0e15;font-size:11px;font-weight:700;display:flex;align-items:center;justify-content:center;flex:none;">{letter}</div>
			<div style="min-width:0;">
				<div style="font-size:13px;font-weight:600;color:#e6e9ef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{name}</div>
				<div class="font-mono" style="font-size:10px;color:#6b7480;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{type}</div>
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
				title="planned: {change}"
				style="position:absolute;left:-7px;top:-7px;width:16px;height:16px;border-radius:50%;background:{ACTION_COLOR[change]};color:#0a0e15;font-size:11px;font-weight:800;line-height:1;display:flex;align-items:center;justify-content:center;z-index:4;"
			>{ACTION_SYMBOL[change]}</div>
		{/if}
		{#if connectable}
			<div title="Drag to connect (depends_on)" role="presentation" style="position:absolute;right:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{color};border:2px solid #0a0e15;cursor:crosshair;z-index:3;" onmousedown={(e) => onDotDown(e, id, 'out')} onmouseup={(e) => onDotUp(e, id, 'out')}></div>
			<div title="Drop a connection here" role="presentation" style="position:absolute;left:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{color};border:2px solid #0a0e15;cursor:crosshair;z-index:3;" onmousedown={(e) => onDotDown(e, id, 'in')} onmouseup={(e) => onDotUp(e, id, 'in')}></div>
		{/if}
	</div>
{/snippet}

<svelte:window onmousemove={onMove} onmouseup={onUp} />

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
									onclick={() => onAddType(t, activeSource)}
									title="Add {t}"
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
				{#each project.providers as p (p.key)}
					<button
						class="rounded-md px-2.5 py-1.5 text-[11px] font-bold"
						style="background:{p.key === activeProvider ? accent : 'transparent'};color:{p.key === activeProvider ? '#0a0e15' : '#8b95a5'};"
						onclick={() => switchProvider(p.key)}
						title="Browse {p.key} resources to add"
					>{p.key}</button>
				{/each}
				{#if project.providers.length === 0}
					<span class="px-2 py-1.5 text-[11px] text-white/30">no providers</span>
				{/if}
			</div>
			<button class="rounded-lg p-2 text-white/50 hover:bg-white/10 hover:text-white/90" style="background:rgba(13,17,23,0.85);border:1px solid rgba(255,255,255,0.08);" onclick={onClose} title="Close editor" aria-label="Close editor">
				<X size={16} />
			</button>
		</div>

		<!-- hint -->
		<div class="pointer-events-none absolute bottom-3.5 left-4 z-[4] text-[11px]" style="color:rgba(255,255,255,0.32);">
			Drag empty space to pan · scroll to zoom · drag a node to move · drag the right dot to another node to add depends_on · click a module to expand · click a node to edit
		</div>

		<!-- world layer (pans / zooms together) -->
		<div style="position:absolute;left:0;top:0;transform:translate({tx}px,{ty}px) scale({scale});transform-origin:0 0;">
			<!-- edges -->
			<svg style="position:absolute;left:0;top:0;overflow:visible;pointer-events:none;" width={worldSize.w} height={worldSize.h}>
				{#each edges as e (e.id)}
					<path d={e.d} stroke="transparent" stroke-width="14" fill="none" style="pointer-events:{e.kind === 'depends' ? 'auto' : 'none'};cursor:{e.kind === 'depends' ? 'pointer' : 'default'};" onmousedown={(ev) => ev.stopPropagation()} onclick={() => onEdgeClick(e)} role="presentation" />
				{/each}
				{#each edges as e (e.id)}
					<path d={e.d} stroke={e.color} stroke-width="2" fill="none" stroke-dasharray={e.kind === 'depends' ? '5 4' : 'none'} opacity={e.kind === 'depends' ? '0.75' : '1'} style="pointer-events:none;" />
				{/each}
				{#if tempPath}
					<path d={tempPath} stroke={accent} stroke-width="2" stroke-dasharray="6 4" fill="none" style="pointer-events:none;" />
				{/if}
			</svg>

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
							onclick={() => { if (dragMoved) { dragMoved = false; return; } toggleExpand(mb.name); }}
							role="presentation"
						>
							<FolderOpen size={14} style="color:{accent};flex:none;" />
							<span style="font-size:12px;font-weight:700;color:#e6e9ef;">module.{mb.name}</span>
							<span style="font-size:10px;color:#6b7480;">{mb.count} resource{mb.count === 1 ? '' : 's'}</span>
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
						onclick={() => { if (dragMoved) { dragMoved = false; return; } toggleExpand(mb.name); }}
					>
						<div class="flex items-center gap-2">
							<div style="width:22px;height:22px;border-radius:6px;background:{accent}22;color:{accent};display:flex;align-items:center;justify-content:center;flex:none;"><Folder size={13} /></div>
							<div style="min-width:0;">
								<div style="font-size:13px;font-weight:700;color:#e6e9ef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{mb.name}</div>
								<div class="font-mono" style="font-size:10px;color:#6b7480;">module · {mb.count} resource{mb.count === 1 ? '' : 's'}</div>
							</div>
							<ChevronRight size={14} style="color:#6b7480;margin-left:auto;flex:none;" />
						</div>
						<div title="Drag to connect (depends_on)" role="presentation" style="position:absolute;right:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{accent};border:2px solid #0a0e15;z-index:3;"></div>
						<div role="presentation" style="position:absolute;left:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{accent};border:2px solid #0a0e15;z-index:3;"></div>
						{@render modBadge(moduleChangeCounts(mb.name))}
					</div>
				{/if}
			{/each}

			<!-- root resource nodes -->
			{#each rootNodes as node (node.id)}
				{@render card(node.id, node.x, node.y, node.name, node.type, node.color, node.letter, node.selected, true, undefined, changeByAddr.get(`${node.type}.${node.name}`))}
			{/each}

			<!-- expanded modules' interior resource nodes -->
			{#each moduleBoxes as mb (mb.id)}
				{#if mb.open}
					{#each mb.lm.resources as r (r.id)}
						{@const c = childAbs(mb, r.id)}
						{@render card(r.id, c.x, c.y, r.name, r.type, typeColor(r.type), typeLetter(r.type), r.id === selectedId, false, mb.name, changeByAddr.get(`module.${mb.name}.${r.type}.${r.name}`))}
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

	<!-- Live HCL panel -->
	<div class="w-[440px] flex-none" style="border-left:1px solid rgba(255,255,255,0.08);min-height:0;">
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
