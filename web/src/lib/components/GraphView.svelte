<script lang="ts">
	import type { GraphData } from '$lib/api/client';

	let { graph }: { graph: GraphData } = $props();

	const NODE_W = 180;
	const NODE_H = 38;
	const COL = 240;
	const ROW = 70;
	const PAD = 30;

	type Placed = { id: string; type: string; x: number; y: number };

	// Longest-path layering: edge from -> to (dependent -> dependency) pushes the
	// dependency into a deeper level, so arrows read left-to-right.
	const layout = $derived.by(() => {
		const nodes = graph.nodes;
		const ids = nodes.map((n) => n.id);
		const level = new Map<string, number>(ids.map((id) => [id, 0]));
		const edges = graph.edges.filter((e) => level.has(e.from) && level.has(e.to));

		// Relax levels (DAG; bounded iterations guard against surprises).
		for (let iter = 0; iter < ids.length; iter++) {
			let changed = false;
			for (const e of edges) {
				const nl = (level.get(e.from) ?? 0) + 1;
				if (nl > (level.get(e.to) ?? 0)) {
					level.set(e.to, nl);
					changed = true;
				}
			}
			if (!changed) break;
		}

		const byLevel = new Map<number, string[]>();
		for (const id of ids) {
			const l = level.get(id) ?? 0;
			if (!byLevel.has(l)) byLevel.set(l, []);
			byLevel.get(l)!.push(id);
		}

		const placed = new Map<string, Placed>();
		for (const [l, group] of [...byLevel.entries()].sort((a, b) => a[0] - b[0])) {
			group.sort();
			group.forEach((id, i) => {
				const n = nodes.find((x) => x.id === id)!;
				placed.set(id, { id, type: n.type, x: PAD + l * COL, y: PAD + i * ROW });
			});
		}

		const maxLevel = Math.max(0, ...[...byLevel.keys()]);
		const maxRows = Math.max(1, ...[...byLevel.values()].map((g) => g.length));
		const width = PAD * 2 + maxLevel * COL + NODE_W;
		const height = PAD * 2 + (maxRows - 1) * ROW + NODE_H;

		const lines = edges
			.map((e) => ({ a: placed.get(e.from)!, b: placed.get(e.to)! }))
			.filter((l) => l.a && l.b);

		return { placed: [...placed.values()], lines, width, height };
	});
</script>

{#if graph.nodes.length === 0}
	<p class="p-6 text-sm text-[var(--al-muted)]">
		No resources to graph yet. Save a resource, then open the graph.
	</p>
{:else}
	<div class="h-full w-full overflow-auto">
		<svg
			width={layout.width}
			height={layout.height}
			viewBox={`0 0 ${layout.width} ${layout.height}`}
			class="min-h-full min-w-full"
		>
			<defs>
				<marker
					id="arrow"
					viewBox="0 0 10 10"
					refX="9"
					refY="5"
					markerWidth="7"
					markerHeight="7"
					orient="auto-start-reverse"
				>
					<path d="M 0 0 L 10 5 L 0 10 z" fill="var(--al-muted)" />
				</marker>
			</defs>

			{#each layout.lines as l (l.a.id + '->' + l.b.id)}
				<line
					x1={l.a.x + NODE_W}
					y1={l.a.y + NODE_H / 2}
					x2={l.b.x}
					y2={l.b.y + NODE_H / 2}
					stroke="var(--al-muted)"
					stroke-width="1.5"
					marker-end="url(#arrow)"
					opacity="0.7"
				/>
			{/each}

			{#each layout.placed as n (n.id)}
				<g transform={`translate(${n.x},${n.y})`}>
					<rect
						width={NODE_W}
						height={NODE_H}
						rx="6"
						fill="var(--al-panel-2)"
						stroke={n.type === 'data' ? 'var(--al-muted)' : 'var(--al-accent)'}
						stroke-width="1.5"
					/>
					<text
						x={NODE_W / 2}
						y={NODE_H / 2 + 4}
						text-anchor="middle"
						class="mono"
						font-size="11"
						fill="var(--al-text)"
					>
						{n.id.length > 24 ? n.id.slice(0, 23) + '…' : n.id}
					</text>
				</g>
			{/each}
		</svg>
	</div>
{/if}
