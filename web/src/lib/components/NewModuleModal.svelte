<!--
  NewModuleModal — the "New module" dialog (name only).

  The draft (nmName) stays in +page: the sidebar's onNewModule clears it and
  createModule() reads it, so there is no seeding $effect here.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { Loader2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		name = $bindable(''),
		saving = false,
		onCreate
	}: {
		open?: boolean;
		name?: string;
		saving?: boolean;
		onCreate: () => void;
	} = $props();
</script>

<Modal title="New module" bind:open>
	<label for="nm-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Module name</label>
	<input id="nm-name" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="vpc" bind:value={name} onkeydown={(e) => { if (e.key === 'Enter') onCreate(); }} />
	<p class="mt-1.5 text-xs text-[var(--al-muted)]">Creates <span class="mono">modules/{name || 'name'}/</span> with its own resources, variables, and outputs.</p>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onCreate} disabled={saving || !name.trim()}>{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Create</button>
	{/snippet}
</Modal>
