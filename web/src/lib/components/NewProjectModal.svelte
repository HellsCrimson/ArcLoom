<!--
  NewProjectModal — the "New project" dialog (name + engine).

  The drafts stay in +page (newProject() seeds them, createProjectConfirmed()
  reads them) and are threaded down with $bindable, so there is no seeding
  $effect here and the modal closes at exactly the same point in the create
  sequence as before.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import type { Engine } from '$lib/types';
	import { Loader2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		name = $bindable(''),
		engine = $bindable(''),
		engines,
		saving = false,
		onCreate
	}: {
		open?: boolean;
		name?: string;
		engine?: string;
		engines: Engine[];
		saving?: boolean;
		onCreate: () => void;
	} = $props();
</script>

<Modal title="New project" bind:open>
	<div class="space-y-3">
		<div>
			<label for="np-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Name</label>
			<input
				id="np-name"
				class="w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
				bind:value={name}
				placeholder="my-infra"
				onkeydown={(e) => { if (e.key === 'Enter') onCreate(); }}
			/>
		</div>
		<div>
			<label for="np-engine" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Engine</label>
			<select id="np-engine" class="w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm" bind:value={engine}>
				{#each engines as e (e.engine)}
					<option value={e.engine}>{e.engine} {e.version}</option>
				{/each}
			</select>
		</div>
	</div>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onCreate} disabled={saving || !name.trim()}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Create
		</button>
	{/snippet}
</Modal>
