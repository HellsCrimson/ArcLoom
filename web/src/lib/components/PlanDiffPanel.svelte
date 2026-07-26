<script lang="ts">
	// PlanDiffPanel is the resource form's plan banner: when the last plan has a
	// pending action for the resource being edited, it says what the action is and
	// which attributes it touches. Clicking a plan row on the canvas or in the run
	// pane opens the form, and this is what makes that landing useful — before it,
	// the per-attribute diff existed only in the streamed CLI log.
	import type { PlanChange } from '$lib/api/client';
	import { ACTION_COLOR, ACTION_SYMBOL } from '$lib/editor/graph';
	import { ChevronDown, ChevronRight } from 'lucide-svelte';
	import PlanDiffList from './PlanDiffList.svelte';

	let {
		changes,
		onDismiss
	}: {
		/** Every planned change for the open resource — more than one when the
		 *  resource is counted / for_each'd (one per instance). */
		changes: PlanChange[];
		onDismiss?: () => void;
	} = $props();

	let open = $state(true);

	// The most severe action across instances labels the panel, matching how the
	// canvas colors a counted resource's single node.
	const SEVERITY: Record<string, number> = { read: 0, update: 1, create: 2, delete: 3, replace: 4 };
	const action = $derived(
		changes.reduce(
			(worst, c) => ((SEVERITY[c.action] ?? 0) > (SEVERITY[worst] ?? 0) ? c.action : worst),
			changes[0]?.action ?? 'update'
		)
	);
	const attrCount = $derived(changes.reduce((n, c) => n + (c.diffs?.length ?? 0), 0));
	// Headline the attributes the user set, not the provider-computed values that
	// ride along (those are collapsed inside the list).
	const setCount = $derived(
		changes.reduce((n, c) => n + (c.diffs ?? []).filter((d) => !d.unknown).length, 0)
	);
	const replaceCount = $derived(
		changes.reduce((n, c) => n + (c.diffs ?? []).filter((d) => d.forcesReplace).length, 0)
	);

	// create/delete/read carry no attribute list by design: there is no "before"
	// to compare a create against (the form below already shows every value being
	// set) and a delete removes the resource whole.
	const explanation: Record<string, string> = {
		create: 'This resource does not exist yet — apply will create it with the values below.',
		delete: 'Apply will destroy this resource.',
		read: 'This data source will be read during apply.'
	};
	const color = $derived(ACTION_COLOR[action] ?? ACTION_COLOR.update);
</script>

<div
	class="mb-4 overflow-hidden rounded-md border"
	style="border-color:{color}55;background:{color}0f;"
>
	<div class="flex items-center gap-2 px-2.5 py-1.5">
		<button
			class="flex min-w-0 flex-1 items-center gap-2 text-left"
			onclick={() => (open = !open)}
			aria-expanded={open}
			title="What the last plan will do to this resource — {open
				? 'click to hide'
				: 'click to show'}"
		>
			{#if open}<ChevronDown size={13} style="color:var(--al-muted)" />{:else}<ChevronRight
					size={13}
					style="color:var(--al-muted)"
				/>{/if}
			<span class="mono flex-none font-bold" style="color:{color};">{ACTION_SYMBOL[action]}</span>
			<span class="flex-none text-xs font-semibold uppercase tracking-wide" style="color:{color};">
				{action}
			</span>
			<!-- Counts lead: in a narrow form column the prose is what should be cut. -->
			<span class="truncate text-xs" style="color:var(--al-muted);">
				{#if setCount > 0}{setCount} attribute{setCount === 1 ? '' : 's'}{:else if attrCount > 0}{attrCount}
					computed value{attrCount === 1 ? '' : 's'}{:else}by the last plan{/if}{#if replaceCount > 0}
					· {replaceCount} forcing replacement{/if}
			</span>
		</button>
		{#if onDismiss}
			<button
				class="flex-none text-[10px] uppercase tracking-wide"
				style="color:var(--al-muted);"
				title="Hide until the next plan"
				onclick={onDismiss}>dismiss</button
			>
		{/if}
	</div>

	{#if open}
		<!-- Capped + scrollable: a wide diff must not push the form it annotates
		     off the screen. -->
		<div
			class="max-h-64 overflow-auto border-t px-2.5 py-1.5"
			style="border-color:{color}33;"
		>
			{#if explanation[action] && attrCount === 0}
				<p class="text-xs" style="color:var(--al-muted);">{explanation[action]}</p>
			{:else}
				{#each changes as c (c.address)}
					{#if changes.length > 1}
						<div class="mono mt-1 text-[10px] first:mt-0" style="color:var(--al-muted);">
							{c.address}
						</div>
					{/if}
					<PlanDiffList diffs={c.diffs ?? []} truncated={c.diffsTruncated} />
				{/each}
			{/if}
		</div>
	{/if}
</div>
