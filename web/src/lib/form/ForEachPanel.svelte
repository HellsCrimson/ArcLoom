<!--
  ForEachPanel — what the for_each expression actually iterates.

  The form used to show only the expression (`var.clusters`) while every field
  below referred to `each.key` / `each.value.x`, so the one thing you needed —
  the members and their shape — lived in another modal. This renders the set
  itself: where it is declared, the instance keys it produces, what
  `each.value` offers, and a one-line preview of each member.

  Best-effort by the same rule as expand.ts: when the expression needs the
  engine (a function call, a resource attribute, a second hop through another
  local) it says so instead of guessing.
-->
<script lang="ts">
	import type { Scope } from '$lib/model/expand';
	import { describeForEach } from '$lib/model/foreach';
	import { ChevronDown, ChevronRight, ExternalLink } from 'lucide-svelte';

	let {
		forEach = '',
		count = '',
		scope,
		/** Opens the declaration (the Variables/locals modal); omitted = no link. */
		onOpenDefinition
	}: {
		forEach?: string;
		count?: string;
		scope: Scope;
		onOpenDefinition?: () => void;
	} = $props();

	const info = $derived(describeForEach(forEach, scope));
	// count is expanded by the sidebar already; here it only earns a line when it
	// is the meta-arg in use, so the panel is never empty next to a filled field.
	const countN = $derived(/^\d+$/.test(count.trim()) ? parseInt(count.trim(), 10) : null);
	let open = $state(true);

	const chip = 'mono rounded border border-[var(--al-border)] px-1.5 py-0.5 text-[11px]';
</script>

{#if count.trim()}
	<div class="mb-4 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-xs text-[var(--al-muted)]">
		<span class="mono text-[var(--al-text)]">count = {count.trim()}</span>
		{#if countN !== null}
			<span> → {countN} instance{countN === 1 ? '' : 's'}, addressed <span class="mono">[0]…[{Math.max(countN - 1, 0)}]</span>. <span class="mono">count.index</span> is the only key.</span>
		{:else}
			<span> → resolved by the engine; the real instances are in the State panel after an apply.</span>
		{/if}
	</div>
{:else if info}
	<div class="mb-4 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2">
		<div class="flex flex-wrap items-center gap-2">
			<button
				class="inline-flex items-center gap-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)] hover:text-[var(--al-text)]"
				onclick={() => (open = !open)}
				aria-expanded={open}
			>
				{#if open}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
				for_each
			</button>
			<span class="mono text-xs text-[var(--al-text)]">{forEach.trim()}</span>
			{#if info.ref}
				<span class="text-[11px] text-[var(--al-muted)]">
					{#if !info.found}
						no {info.ref.kind} "{info.ref.name}" in this scope
					{:else if info.derived}
						over <span class="mono text-[var(--al-text)]">{info.ref.kind === 'variable' ? 'var' : 'local'}.{info.ref.name}</span>
					{:else}
						declared as a {info.ref.kind}
					{/if}
				</span>
			{/if}
			{#if info.typeText}
				<span class="mono truncate text-[11px] text-[var(--al-muted)]" title={info.typeText}>{info.typeText}</span>
			{/if}
			{#if info.ref && info.found && onOpenDefinition}
				<button
					class="ml-auto inline-flex items-center gap-1 text-[11px] text-[var(--al-muted)] underline hover:text-[var(--al-accent)]"
					onclick={onOpenDefinition}
					title="Open the declaration of {info.ref.kind === 'variable' ? 'var' : 'local'}.{info.ref.name}"
				>
					<ExternalLink size={11} /> edit definition
				</button>
			{/if}
		</div>

		{#if open}
			{#if !info.expansion}
				<p class="mt-1.5 text-[11px] text-[var(--al-muted)]">
					{#if info.derived && info.found}
						A comprehension filters or transforms the collection, so the engine decides the final keys — and
						<span class="mono">each.value</span> too, if it maps members to something else. What it starts from:
					{:else}
						The engine resolves this one — a function, a resource attribute or another indirection.
						The real instances appear in the State panel after an apply, and a plan lists the ones it changes.
					{/if}
				</p>
				{#if info.derived && info.found}
					<div class="mt-1 text-[11px] text-[var(--al-muted)]">
						{#if info.valueFields.length}
							members of <span class="mono text-[var(--al-text)]">{info.ref?.kind === 'variable' ? 'var' : 'local'}.{info.ref?.name}</span> carry
							{#each info.valueFields as f (f)}<span class="mono text-[var(--al-text)]">.{f}</span>{' '}{/each}
						{:else}
							<span class="mono text-[var(--al-text)]">{info.ref?.kind === 'variable' ? 'var' : 'local'}.{info.ref?.name}</span> — open the definition to see its members.
						{/if}
					</div>
				{/if}
			{:else if info.expansion.keys.length === 0}
				<p class="mt-1.5 text-[11px] text-[var(--al-muted)]">Empty collection — this block creates no instances.</p>
			{:else}
				<div class="mt-1.5 text-[11px] text-[var(--al-muted)]">
					{info.expansion.keys.length}{info.expansion.truncated ? `+${info.expansion.truncated}` : ''}
					instance{info.expansion.keys.length === 1 && !info.expansion.truncated ? '' : 's'} ·
					<span class="mono">each.key</span>
					{#if info.valueIsKey}
						and <span class="mono">each.value</span> are the same string
					{:else if info.valueFields.length}
						· <span class="mono">each.value</span>:
						{#each info.valueFields as f (f)}<span class="mono text-[var(--al-text)]">.{f}</span>{' '}{/each}
					{/if}
				</div>
				<div class="mt-1 space-y-0.5">
					{#each info.elements as el (el.key)}
						<div class="flex items-start gap-2">
							<span class={chip + ' flex-none text-[var(--al-accent)]'}>{el.key}</span>
							{#if el.preview}
								<span class="mono min-w-0 flex-1 truncate pt-0.5 text-[11px] text-[var(--al-muted)]" title={el.preview}>{el.preview}</span>
							{/if}
						</div>
					{/each}
					{#if info.expansion.truncated}
						<p class="text-[11px] text-[var(--al-muted)]">+{info.expansion.truncated} more not listed.</p>
					{/if}
				</div>
			{/if}
		{/if}
	</div>
{/if}
