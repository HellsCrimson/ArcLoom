<!--
  DeleteBlockedModal — shown when a project delete is refused because the
  project still tracks deployed resources.

  It must NOT self-close: forceRemoveProject in +page clears the flag itself
  before awaiting the force-delete.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btn, btnGhost } from '$lib/styles';
	import { Trash2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		name = $bindable(''),
		projectName,
		deployedCount,
		onDelete
	}: {
		open?: boolean;
		name?: string;
		projectName: string;
		deployedCount: number;
		onDelete: () => void;
	} = $props();
</script>

<Modal title="Project has deployed resources" bind:open>
	<p class="text-sm text-[var(--al-text)]">
		<span class="font-semibold">{projectName}</span> still tracks
		<span class="font-semibold text-[var(--al-danger)]">{deployedCount} deployed resource{deployedCount === 1 ? '' : 's'}</span>
		in its state. Deleting the project also deletes that state, and the resources would keep
		running with nothing left to manage or destroy them.
	</p>
	<p class="mt-2 text-xs text-[var(--al-muted)]">
		Recommended: cancel and run <span class="font-semibold">Destroy</span> first, then delete the project.
	</p>
	<label for="delblock-name" class="mb-1 mt-3 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
		Type the project name ({projectName}) to delete anyway
	</label>
	<input
		id="delblock-name"
		class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-danger)]"
		bind:value={name}
		placeholder={projectName}
	/>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button
			class="{btn} bg-[var(--al-danger)] text-white hover:brightness-110"
			disabled={name !== projectName}
			onclick={onDelete}
		>
			<Trash2 size={14} /> Delete anyway
		</button>
	{/snippet}
</Modal>
