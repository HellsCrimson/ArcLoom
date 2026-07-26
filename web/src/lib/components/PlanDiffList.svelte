<script lang="ts">
	// PlanDiffList renders the attribute-level diff of one planned change: which
	// property changes, from what to what, and which one forces a replacement.
	// Shared by the resource form (the primary surface — see PlanDiffPanel) and
	// the run pane's expandable change rows, so the raw CLI log is the last
	// resort rather than the only place this information exists.
	import type { AttrDiff } from '$lib/api/client';
	import { ACTION_COLOR } from '$lib/editor/graph';

	let {
		diffs,
		truncated = false,
		compact = false
	}: {
		diffs: AttrDiff[];
		/** The server hit its per-resource cap — point at the log for the rest. */
		truncated?: boolean;
		/** Tighter type scale for the run pane's code surface. */
		compact?: boolean;
	} = $props();

	// Attribute actions borrow the plan action colors: add reads like a create,
	// remove like a delete.
	const COLOR: Record<string, string> = {
		add: ACTION_COLOR.create,
		update: ACTION_COLOR.update,
		remove: ACTION_COLOR.delete
	};
	const SYMBOL: Record<string, string> = { add: '+', update: '~', remove: '−' };

	// Provider-computed values (hashes, ids, ARNs) are usually most of a plan's
	// attribute rows and none of its meaning — a `content` change drags seven
	// checksums along. The rows the user can act on come first; the computed ones
	// collapse behind a count so they stay available without burying the change.
	const primary = $derived(
		diffs
			.filter((d) => !d.unknown)
			.sort((a, b) => Number(!!b.forcesReplace) - Number(!!a.forcesReplace) || a.path.localeCompare(b.path))
	);
	const computed = $derived(diffs.filter((d) => d.unknown));
	let showComputed = $state(false);
</script>

{#snippet row(d: AttrDiff)}
	<!-- flex-wrap + a basis on the value: in a narrow panel the value drops to its
	     own full-width line instead of being squeezed to one character per row. -->
	<div class="flex flex-wrap items-baseline gap-x-2 py-[1px]">
		<span class="w-3 flex-none text-center font-bold" style="color:{COLOR[d.action]}">
			{SYMBOL[d.action]}
		</span>
		<span class="flex-none font-medium" style="color:var(--al-text);">{d.path}</span>
		{#if d.forcesReplace}
			<span
				class="flex-none rounded px-1 text-[9px] font-semibold uppercase tracking-wide"
				style="background:rgba(240,136,62,0.15);color:{ACTION_COLOR.replace};"
				title="Changing this attribute forces the resource to be replaced"
			>
				forces replace
			</span>
		{/if}
		<span class="min-w-0 flex-1 basis-48 break-all" style="color:var(--al-muted);">
			{#if d.action === 'add'}
				<span style="color:{COLOR.add};">{d.after}</span>
			{:else if d.action === 'remove'}
				<span class="line-through">{d.before}</span>
			{:else}
				<span class="line-through">{d.before}</span>
				<span class="px-1" style="color:var(--al-muted);">→</span>
				<span style="color:{d.unknown ? 'var(--al-muted)' : 'var(--al-text)'};">{d.after}</span>
			{/if}
			{#if d.sensitive}
				<span class="ml-1 text-[9px] uppercase tracking-wide" style="color:var(--al-accent);"
					>sensitive</span
				>
			{/if}
		</span>
	</div>
{/snippet}

<div class="mono {compact ? 'text-[11px]' : 'text-[11.5px]'} leading-relaxed">
	{#each primary as d (d.path)}
		{@render row(d)}
	{/each}

	{#if computed.length > 0}
		<button
			class="px-0.5 py-0.5 text-left text-[10px] hover:underline"
			style="color:var(--al-muted);"
			onclick={() => (showComputed = !showComputed)}
			aria-expanded={showComputed}
		>
			{showComputed ? '▾' : '▸'} {computed.length} computed value{computed.length === 1 ? '' : 's'}
			known after apply
		</button>
		{#if showComputed}
			{#each computed as d (d.path)}
				{@render row(d)}
			{/each}
		{/if}
	{/if}

	{#if truncated}
		<div class="px-0.5 pt-1 text-[10px]" style="color:var(--al-muted);">
			Attribute list truncated — the full diff is in the run log.
		</div>
	{/if}
	{#if diffs.length === 0}
		<div class="px-0.5 text-[11px]" style="color:var(--al-muted);">
			No attribute-level detail for this change.
		</div>
	{/if}
</div>
