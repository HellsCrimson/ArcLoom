<!--
  ModuleInputsModal — the values the root passes to a local `module "x"` block.

  Markup only: the `{#if activeMod}` narrowing wrapper and the inputDraft
  seeding $effect both stay in +page — that effect re-seeds whenever showInputs
  is true AND activeMod changes, and activeMod CAN change under an open modal
  (jumpToAddress writes activeModule directly).
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import type { Variable } from '$lib/types';
	import { Loader2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		moduleName,
		variables,
		draft = $bindable<Record<string, string>>({}),
		count = $bindable(''),
		forEach = $bindable(''),
		saving = false,
		onSave
	}: {
		open?: boolean;
		moduleName: string;
		variables: Variable[];
		draft?: Record<string, string>;
		count?: string;
		forEach?: string;
		saving?: boolean;
		onSave: () => void;
	} = $props();
</script>

<Modal title={`Module "${moduleName}" — inputs from root`} bind:open>
	<!--
	  Meta-args sit ABOVE the variables branch: a module can carry for_each while
	  declaring no variables of its own, and that must still be editable.
	-->
	<div class="mb-3 flex items-end gap-3 border-b border-[var(--al-border)] pb-3">
		<div>
			<label for="module-count" class="mb-1 block text-xs text-[var(--al-muted)]" title="count meta-argument on the module call — an HCL expression, e.g. 3 or var.replicas">count</label>
			<input id="module-count" class="mono w-28 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" bind:value={count} placeholder="3 · var.n" disabled={forEach.trim() !== ''} />
		</div>
		<div>
			<label for="module-foreach" class="mb-1 block text-xs text-[var(--al-muted)]" title="for_each meta-argument on the module call — a set or map expression, e.g. var.teams">for_each</label>
			<input id="module-foreach" class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" bind:value={forEach} placeholder="var.teams · toset([…])" disabled={count.trim() !== ''} />
		</div>
		{#if count.trim() !== '' || forEach.trim() !== ''}
			<p class="pb-1.5 text-xs text-[var(--al-muted)]">Inputs can use <span class="mono">{count.trim() !== '' ? 'count.index' : 'each.key'}</span>.</p>
		{/if}
	</div>
	{#if variables.length === 0}
		<p class="text-sm text-[var(--al-muted)]">This module has no variables yet. Add variables in the module's Variables panel, then set their values here.</p>
	{:else}
		<p class="mb-2 text-xs text-[var(--al-muted)]">Values the root passes to <span class="mono">module "{moduleName}"</span> (raw HCL).</p>
		<div class="space-y-2">
			{#each variables as v (v.name)}
				<div class="flex items-center gap-2">
					<span class="mono w-32 truncate text-xs text-[var(--al-text)]">{v.name}</span>
					<input class="mono flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder="value (HCL)" bind:value={draft[v.name]} />
				</div>
			{/each}
		</div>
	{/if}
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onSave} disabled={saving}>{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Save inputs</button>
	{/snippet}
</Modal>
