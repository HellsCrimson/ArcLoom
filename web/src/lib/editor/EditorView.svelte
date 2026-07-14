<script lang="ts">
	import type { Project, Resource } from '$lib/types';
	import { highlightHcl } from './highlight';
	import { deriveEdges, typeColor, typeLetter, layoutPositions, layoutMissing, LAYOUTS, type Edge, type LayoutMode } from './graph';
	import { X, Plus, Minus, Search, Loader2, Network, Rows3, LayoutGrid, CircleDashed, Group } from 'lucide-svelte';

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
		loadTypes: (source: string) => Promise<string[]>;
		onAddType: (type: string, providerSource: string) => void;
		onEditResource: (r: Resource) => void;
		onDeleteResource: (id: string) => void;
		onRenameResource: (id: string, name: string) => void;
		onSetDependsOn: (id: string, deps: string[]) => void;
		onClose: () => void;
	} = $props();

	const NODE_W = 180;
	const NODE_H = 84;
	const DOT_Y = 42;

	let canvasEl = $state<HTMLDivElement>();
	const posKey = $derived(`arcloom.pos.${project.id}`);

	let positions = $state<Record<string, { x: number; y: number }>>({});
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

	// Load stored positions when the project changes.
	$effect(() => {
		posKey; // track
		try {
			positions = JSON.parse(localStorage.getItem(posKey) || '{}');
		} catch {
			positions = {};
		}
	});

	function savePos() {
		localStorage.setItem(posKey, JSON.stringify(positions));
	}

	// Auto-layout any resource without a stored position (dependency-layered),
	// preserving nodes the user has already dragged.
	$effect(() => {
		const res = project.resources;
		if (res.every((r) => positions[r.id])) return;
		positions = layoutMissing(res, deriveEdges(res), positions);
		savePos();
	});

	// applyLayout re-arranges every node under a chosen preset (an explicit
	// user action — it discards manual positions in favour of a clean layout).
	function applyLayout(mode: LayoutMode) {
		positions = layoutPositions(project.resources, deriveEdges(project.resources), mode);
		savePos();
		resetView();
	}

	const nodes = $derived(
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

	const edges = $derived.by(() => {
		const byId = new Map(nodes.map((n) => [n.id, n]));
		return deriveEdges(project.resources)
			.map((e: Edge) => {
				const from = byId.get(e.from);
				const to = byId.get(e.to);
				if (!from || !to) return null;
				const x1 = from.x + NODE_W,
					y1 = from.y + DOT_Y,
					x2 = to.x,
					y2 = to.y + DOT_Y;
				const mid = (x1 + x2) / 2;
				return {
					...e,
					color: from.color,
					d: `M ${x1} ${y1} C ${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`
				};
			})
			.filter(Boolean) as Array<Edge & { color: string; d: string }>;
	});

	const tempPath = $derived.by(() => {
		if (!connecting) return null;
		const from = nodes.find((n) => n.id === connecting!.fromId);
		if (!from) return null;
		return `M ${from.x + NODE_W} ${from.y + DOT_Y} L ${connecting.x} ${connecting.y}`;
	});

	const hclLines = $derived(highlightHcl(hcl || ''));

	// World extent large enough to hold all nodes with slack for panning.
	const worldSize = $derived.by(() => {
		let w = 1600,
			h = 1000;
		for (const n of nodes) {
			w = Math.max(w, n.x + NODE_W + 400);
			h = Math.max(h, n.y + NODE_H + 400);
		}
		return { w, h };
	});

	// Screen -> world coordinates, accounting for pan and zoom.
	function toWorld(e: MouseEvent) {
		const rect = canvasEl!.getBoundingClientRect();
		return { x: (e.clientX - rect.left - tx) / scale, y: (e.clientY - rect.top - ty) / scale };
	}

	function onNodeDown(e: MouseEvent, id: string) {
		if (e.button !== 0) return;
		e.stopPropagation(); // don't start a canvas pan
		const n = nodes.find((x) => x.id === id);
		if (!n) return;
		const { x, y } = toWorld(e);
		selectedId = id;
		dragMoved = false;
		dragging = { id, ox: x - n.x, oy: y - n.y };
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
		// Keep the world point under the cursor fixed.
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
		if (e.kind !== 'depends') return; // reference edges are read-only here
		const to = project.resources.find((r) => r.id === e.to);
		const from = project.resources.find((r) => r.id === e.from);
		if (!to || !from) return;
		const addr = `${from.type}.${from.name}`;
		onSetDependsOn(to.id, (to.dependsOn ?? []).filter((d) => d.split('.').slice(0, 2).join('.') !== addr));
	}

	function rename(id: string) {
		const r = project.resources.find((x) => x.id === id);
		if (!r) return;
		const next = window.prompt('Resource name', r.name);
		if (next && next.trim()) onRenameResource(id, next.trim().replace(/[^a-zA-Z0-9_]+/g, '_'));
	}

	async function switchProvider(key: string) {
		activeProvider = key;
		paletteTypes = [];
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

	// When the active provider changes, refresh the palette's type list (reload if
	// it's open, otherwise just clear the stale list). Must NOT toggle showPalette
	// here — reading it and calling openPalette() would close the palette the same
	// tick it opens.
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

<svelte:window onmousemove={onMove} onmouseup={onUp} />

<div
	class="fixed inset-0 z-50 flex"
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
		<!-- top-center brand -->
		<div class="pointer-events-none absolute left-1/2 top-3.5 z-[4] flex -translate-x-1/2 items-center gap-2">
			<div style="width:12px;height:12px;border-radius:3px;background:{accent};transform:rotate(45deg);box-shadow:0 0 14px {accent}80;"></div>
			<div style="font-size:12.5px;font-weight:700;letter-spacing:0.02em;color:rgba(255,255,255,0.55);">ArcLoom · {project.name}</div>
		</div>

		<!-- layout presets -->
		{#if project.resources.length > 1}
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
			Drag empty space to pan · scroll to zoom · drag a node to move · drag the right dot to another node to add depends_on · click a node to edit · double-click to rename
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

			<!-- nodes -->
			{#each nodes as node (node.id)}
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
				<div
					role="button"
					tabindex="-1"
					style="position:absolute;left:{node.x}px;top:{node.y}px;width:{NODE_W}px;height:{NODE_H}px;border-radius:12px;padding:10px 12px;background:#10151f;border:1px solid {node.selected ? accent : 'rgba(255,255,255,0.10)'};box-shadow:{node.selected ? '0 0 0 2px ' + accent + '55, 0 10px 28px rgba(0,0,0,0.45)' : '0 6px 16px rgba(0,0,0,0.35)'};cursor:grab;z-index:2;"
					onmousedown={(e) => onNodeDown(e, node.id)}
					onclick={() => { if (dragMoved) { dragMoved = false; return; } onEditResource(node); }}
					ondblclick={(e) => { e.stopPropagation(); rename(node.id); }}
				>
					<div class="flex items-center gap-2">
						<div style="width:22px;height:22px;border-radius:6px;background:{node.color};color:#0a0e15;font-size:11px;font-weight:700;display:flex;align-items:center;justify-content:center;flex:none;">{node.letter}</div>
						<div style="min-width:0;">
							<div style="font-size:13px;font-weight:600;color:#e6e9ef;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{node.name}</div>
							<div class="font-mono" style="font-size:10px;color:#6b7480;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">{node.type}</div>
						</div>
					</div>
					<button
						title="Delete resource"
						aria-label="Delete resource"
						class="absolute right-1.5 top-1.5 leading-none text-white/30 hover:text-white/80"
						style="font-size:12px;"
						onmousedown={(e) => e.stopPropagation()}
						onclick={(e) => { e.stopPropagation(); onDeleteResource(node.id); }}
					>✕</button>
					<div title="Drag to connect (depends_on)" role="presentation" style="position:absolute;right:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{node.color};border:2px solid #0a0e15;cursor:crosshair;z-index:3;" onmousedown={(e) => onDotDown(e, node.id, 'out')} onmouseup={(e) => onDotUp(e, node.id, 'out')}></div>
					<div title="Drop a connection here" role="presentation" style="position:absolute;left:-6px;top:{DOT_Y - 6}px;width:12px;height:12px;border-radius:50%;background:{node.color};border:2px solid #0a0e15;cursor:crosshair;z-index:3;" onmousedown={(e) => onDotDown(e, node.id, 'in')} onmouseup={(e) => onDotUp(e, node.id, 'in')}></div>
				</div>
			{/each}
		</div>

		<!-- zoom controls -->
		<div class="absolute bottom-3.5 right-4 z-[5] flex items-center gap-1" style="background:rgba(13,17,23,0.85);backdrop-filter:blur(8px);padding:4px;border-radius:10px;border:1px solid rgba(255,255,255,0.08);">
			<button class="rounded-md px-2 py-1 text-white/60 hover:bg-white/10 hover:text-white/90" onclick={() => zoomBy(1 / 1.2)} title="Zoom out" aria-label="Zoom out"><Minus size={14} /></button>
			<button class="min-w-[42px] rounded-md px-1 py-1 text-[11px] text-white/60 hover:bg-white/10 hover:text-white/90" onclick={resetView} title="Reset view">{Math.round(scale * 100)}%</button>
			<button class="rounded-md px-2 py-1 text-white/60 hover:bg-white/10 hover:text-white/90" onclick={() => zoomBy(1.2)} title="Zoom in" aria-label="Zoom in"><Plus size={14} /></button>
		</div>

		{#if project.resources.length === 0}
			<div class="pointer-events-none absolute inset-0 grid place-items-center text-sm text-white/30">
				No resources yet — click <span class="mx-1 font-semibold" style="color:{accent}">Add</span> to create one.
			</div>
		{/if}
	</div>

	<!-- Live HCL panel -->
	<div class="flex w-[440px] flex-none flex-col" style="background:#0b0f18;border-left:1px solid rgba(255,255,255,0.08);min-height:0;">
		<div class="flex h-11 flex-none items-center justify-between border-b px-4" style="border-color:rgba(255,255,255,0.07);">
			<div class="flex items-center gap-2">
				<div style="width:6px;height:6px;border-radius:50%;background:#3fb950;"></div>
				<div class="font-mono" style="font-size:12.5px;font-weight:600;color:#c9d3dc;">main.tf</div>
			</div>
			<div class="font-mono" style="font-size:11px;color:#5b6472;">{providerFooter}</div>
		</div>
		<div class="flex-1 overflow-auto px-[18px] py-4 font-mono" style="font-size:12.5px;line-height:1.7;">
			{#each hclLines as line, i (i)}
				<div>{#each line as tok, j (j)}<span style="color:{tok.c};white-space:pre;">{tok.t}</span>{/each}</div>
			{/each}
		</div>
	</div>
</div>
