<!--
  ImportHclModal — paste or upload .tf files to reconstruct them into the model.

  Markup only: readFiles() and doImportHcl() stay in +page. The drafts stay
  there too (threaded with $bindable) which preserves the deliberate asymmetry
  that they SURVIVE Cancel — no opener clears them, and doImportHcl clears them
  only inside its try. A seed-on-open $effect would erase a half-pasted config.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost, ta } from '$lib/styles';
	import { Loader2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		text = $bindable(''),
		files = $bindable<Record<string, string>>({}),
		scopeLabel,
		saving = false,
		onFiles,
		onImport
	}: {
		open?: boolean;
		text?: string;
		files?: Record<string, string>;
		scopeLabel: string;
		saving?: boolean;
		onFiles: (list: FileList | null) => void;
		onImport: () => void;
	} = $props();
</script>

<Modal title="Import existing HCL" bind:open>
	<div class="mb-2 flex items-center gap-1.5 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-xs">
		<span class="text-[var(--al-muted)]">Importing into</span>
		<span class="mono font-medium text-[var(--al-text)]">{scopeLabel}</span>
	</div>
	<p class="mb-2 text-xs text-[var(--al-muted)]">Select one or more <span class="mono">.tf</span> files, or paste HCL below. Resources, variables, providers and required_providers are reconstructed into the model.</p>
	<input type="file" multiple accept=".tf,.hcl" class="mb-2 text-xs" onchange={(e) => onFiles(e.currentTarget.files)} />
	{#if Object.keys(files).length > 0}
		<p class="mb-2 text-xs text-[var(--al-muted)]">Files: {Object.keys(files).join(', ')}</p>
	{/if}
	<textarea class={ta} rows="12" placeholder="resource &quot;...&quot; ..." bind:value={text}></textarea>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onImport} disabled={saving}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Import
		</button>
	{/snippet}
</Modal>
