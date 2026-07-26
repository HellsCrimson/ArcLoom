<!--
  DestroyConfirmModal — the type-the-project-name gate in front of `destroy`.

  Deliberately NOT merged with ApplyConfirmModal/DeleteBlockedModal: the copy
  and the disabled predicates differ, and the copy is the safety mechanism.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btn, btnGhost } from '$lib/styles';
	import { Flame } from 'lucide-svelte';

	let {
		open = $bindable(false),
		name = $bindable(''),
		engine,
		projectName,
		onDestroy
	}: {
		open?: boolean;
		name?: string;
		engine: string;
		projectName: string;
		onDestroy: () => void;
	} = $props();
</script>

<Modal title="Destroy infrastructure" bind:open>
	<p class="text-sm text-[var(--al-text)]">
		This runs <span class="mono">{engine} destroy</span> and
		<span class="font-semibold text-[var(--al-danger)]">deletes every resource this project has deployed</span>.
		It cannot be undone.
	</p>
	<label for="destroy-name" class="mb-1 mt-3 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
		Type the project name ({projectName}) to confirm
	</label>
	<input
		id="destroy-name"
		class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-danger)]"
		bind:value={name}
		placeholder={projectName}
	/>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button
			class="{btn} bg-[var(--al-danger)] text-white hover:brightness-110"
			disabled={name !== projectName}
			onclick={onDestroy}
		>
			<Flame size={14} /> Destroy
		</button>
	{/snippet}
</Modal>
