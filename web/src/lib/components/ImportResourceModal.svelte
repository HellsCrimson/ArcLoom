<!--
  ImportResourceModal — adopt a live resource into state at the form's address.

  Markup only: importId stays in +page (the form header's Import button clears
  it) and runImport() reads it alongside importAddress.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { HardDriveDownload } from 'lucide-svelte';

	let {
		open = $bindable(false),
		id = $bindable(''),
		address,
		onImport
	}: {
		open?: boolean;
		id?: string;
		address: string;
		onImport: () => void;
	} = $props();
</script>

<Modal title="Import into state" bind:open>
	<p class="mb-3 text-sm text-[var(--al-text)]">
		Adopt an existing real-world resource as
		<span class="mono text-[var(--al-accent)]">{address}</span>.
		The provider reads it by ID and records it in this project's state — nothing is created or changed.
	</p>
	<label for="import-id" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Resource ID</label>
	<input
		id="import-id"
		class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
		placeholder="provider-specific ID (e.g. i-0abc123, vpc-…, a value)"
		bind:value={id}
		onkeydown={(e) => { if (e.key === 'Enter' && id.trim()) onImport(); }}
	/>
	<p class="mt-1.5 text-xs text-[var(--al-muted)]">Attributes that differ from the form will show as changes on the next plan.</p>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onImport} disabled={!id.trim()}>
			<HardDriveDownload size={14} /> Import
		</button>
	{/snippet}
</Modal>
