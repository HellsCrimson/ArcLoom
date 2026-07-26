<!--
  ApplyConfirmModal — the "Apply changes" confirmation.

  The copy IS the safety mechanism, so it is copied verbatim; the modal closes
  before calling back, exactly as it did inline.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { Rocket } from 'lucide-svelte';

	let {
		open = $bindable(false),
		engine,
		projectName,
		planResult,
		onApply
	}: {
		open?: boolean;
		engine: string;
		projectName: string;
		planResult: string;
		onApply: () => void;
	} = $props();
</script>

<Modal title="Apply changes" bind:open>
	<p class="text-sm text-[var(--al-text)]">
		This runs <span class="mono">{engine} apply</span> on
		<span class="font-semibold">{projectName}</span> and makes real changes to your infrastructure.
	</p>
	<p class="mt-2 text-xs text-[var(--al-muted)]">
		{#if planResult === 'No changes'}
			The last plan reported no changes.
		{:else if planResult === 'Changes pending'}
			The last plan reported pending changes — review the run output before applying.
		{:else}
			Tip: run a plan first to review exactly what will change.
		{/if}
	</p>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onApply}>
			<Rocket size={14} /> Apply
		</button>
	{/snippet}
</Modal>
