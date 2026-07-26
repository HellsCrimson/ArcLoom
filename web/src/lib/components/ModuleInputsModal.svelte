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
		saving = false,
		onSave
	}: {
		open?: boolean;
		moduleName: string;
		variables: Variable[];
		draft?: Record<string, string>;
		saving?: boolean;
		onSave: () => void;
	} = $props();
</script>

<Modal title={`Module "${moduleName}" — inputs from root`} bind:open>
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
