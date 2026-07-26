<script lang="ts">
	// The measured action row of the top bar, plus the fit algorithm that decides
	// how much of each action is shown. It lives next to the markup it measures
	// (and next to hdrBtn, against which the width constants were calibrated):
	// clientWidth of the root div IS the space the fixed-size header groups left
	// over, so the fit needs no estimate of their widths.
	import ToolbarMenu, { type ToolItem } from '$lib/components/ToolbarMenu.svelte';
	import { hdrBtn } from '$lib/styles';
	import { FileDown, ChevronDown } from 'lucide-svelte';

	let {
		items,
		importRows,
		measuredWidth = $bindable(0),
		menuOpen = $bindable(false),
		importMenuOpen = $bindable(false),
		onImportOpen
	}: {
		items: ToolItem[];
		importRows: ToolItem[];
		measuredWidth: number;
		menuOpen: boolean;
		importMenuOpen: boolean;
		onImportOpen?: () => void;
	} = $props();

	// Width estimates (measured against the rendered buttons, then rounded up:
	// over-estimating collapses one action early, which is invisible, while
	// under-estimating would clip the bar). Each includes the 6px flex gap.
	const TOOL_ICON_W = 42; // icon-only button
	const TOOL_MORE_W = 86; // the "More" button
	const toolWidth = (it: ToolItem) =>
		44 + it.label.length * 7 + (it.badge ? 18 : 0) + (it.id === 'import' ? 14 : 0);

	// Fitting the actions into the measured width, degrading in three steps so the
	// space available is always spent on the most useful thing:
	//   1. everything labelled, if it fits;
	//   2. otherwise every action stays visible as an icon, and the leftover width
	//      buys labels back for the leading (highest-priority) ones — a full row of
	//      icons would otherwise waste a few hundred px on a 1600px laptop;
	//   3. and only when even the icons do not fit does the tail move to "More".
	const toolLayout = $derived.by(() => {
		const labelAll = () => new Set(items.map((it) => it.id));
		// Before the first measurement, assume everything fits: a flash of the full
		// bar is better than a flash of an empty one.
		if (items.length === 0 || measuredWidth === 0) {
			return { labelled: labelAll(), visible: items, overflow: [] as ToolItem[] };
		}

		const iconTotal = items.length * TOOL_ICON_W;
		if (iconTotal > measuredWidth) {
			const budget = measuredWidth - TOOL_MORE_W;
			const visible = items.slice(0, Math.max(0, Math.floor(budget / TOOL_ICON_W)));
			const overflow = items.slice(visible.length).flatMap((it) => (it.id === 'import' ? importRows : [it]));
			return { labelled: new Set<string>(), visible, overflow };
		}

		if (items.reduce((n, it) => n + toolWidth(it), 0) <= measuredWidth) {
			return { labelled: labelAll(), visible: items, overflow: [] as ToolItem[] };
		}

		// Buy labels from the left, stopping at the first that does not fit so the
		// labelled ones stay contiguous instead of scattering across the row.
		let spare = measuredWidth - iconTotal;
		const labelled = new Set<string>();
		for (const it of items) {
			const extra = toolWidth(it) - TOOL_ICON_W;
			if (extra > spare) break;
			spare -= extra;
			labelled.add(it.id);
		}
		return { labelled, visible: items, overflow: [] as ToolItem[] };
	});
</script>

<!-- min-w (not the flex default of min-content) keeps clientWidth equal to
     the width flex assigned rather than to the content, and keeps room for
     the "More" button so the actions stay reachable at any size. -->
<div class="flex min-w-[46px] flex-1 items-center gap-1.5" bind:clientWidth={measuredWidth}>
	{#each toolLayout.visible as item (item.id)}
		{#if item.id === 'import'}
			<div class="relative flex-none">
				<button class={hdrBtn} onclick={(e) => { e.stopPropagation(); onImportOpen?.(); importMenuOpen = !importMenuOpen; }}>
					<FileDown size={14} />
					{#if toolLayout.labelled.has('import')}Import <ChevronDown size={13} />{/if}
				</button>
				{#if importMenuOpen}
					<div class="absolute left-0 top-full z-20 mt-1 w-40 overflow-hidden rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] shadow-xl">
						{#each importRows as row (row.id)}
							{@const RowIcon = row.icon}
							<button class="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]" onclick={() => { importMenuOpen = false; row.run(); }}><RowIcon size={13} /> {row.label}</button>
						{/each}
					</div>
				{/if}
			</div>
		{:else}
			{@const Icon = item.icon}
			<button
				class="{hdrBtn} flex-none {item.active ? 'border-[var(--al-accent)] bg-[var(--al-accent-weak)] text-[var(--al-accent)]' : ''}"
				title={item.title ?? item.label}
				aria-label={item.label}
				onclick={item.run}
			>
				<Icon size={14} />
				{#if toolLayout.labelled.has(item.id)}
					{item.label}{#if item.badge}<span class="text-[10px] text-[var(--al-muted)]">{item.badge}</span>{/if}
				{/if}
			</button>
		{/if}
	{/each}
	{#if toolLayout.overflow.length > 0}
		<ToolbarMenu bind:open={menuOpen} items={toolLayout.overflow} />
	{/if}
</div>
