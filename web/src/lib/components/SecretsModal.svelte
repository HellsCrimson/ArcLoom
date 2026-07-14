<script lang="ts">
	import type { SecretMeta } from '$lib/types';
	import Modal from './Modal.svelte';
	import { Plus, Trash2, Loader2, KeyRound, Lock } from 'lucide-svelte';

	let {
		open = $bindable(false),
		secrets = [],
		localEnabled = false,
		busy = false,
		onAdd,
		onDelete
	}: {
		open: boolean;
		secrets: SecretMeta[];
		localEnabled: boolean;
		busy?: boolean;
		onAdd: (name: string, value: string) => void;
		onDelete: (name: string) => void;
	} = $props();

	let newName = $state('');
	let newValue = $state('');

	function add() {
		if (!newName.trim() || !newValue) return;
		onAdd(newName.trim(), newValue);
		newName = '';
		newValue = '';
	}

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

<Modal title="Secrets" bind:open>
	<p class="mb-3 flex items-center gap-1.5 text-xs text-[var(--al-muted)]">
		<Lock size={12} /> Values are encrypted at rest and injected as <span class="mono">TF_VAR_*</span> at plan/apply — never written to HCL, state, or Git.
	</p>

	{#if !localEnabled}
		<div class="mb-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-xs text-[var(--al-muted)]">
			Local encrypted storage is disabled. Set <span class="mono">ARCLOOM_SECRET_KEY</span> to store secrets, or provide them via <span class="mono">ARCLOOM_SECRET_&lt;name&gt;</span> environment variables.
		</div>
	{/if}

	<!-- existing secrets -->
	<div class="mb-4 space-y-1">
		{#if secrets.length === 0}
			<p class="text-xs text-[var(--al-muted)]">No secrets yet.</p>
		{/if}
		{#each secrets as s (s.name + s.backend)}
			<div class="flex items-center gap-2 rounded-md border border-[var(--al-border)] px-2.5 py-1.5">
				<KeyRound size={14} class="text-[var(--al-accent)]" />
				<span class="mono flex-1 text-sm text-[var(--al-text)]">{s.name}</span>
				<span class="rounded bg-[var(--al-panel-2)] px-1.5 py-0.5 text-[10px] uppercase text-[var(--al-muted)]">{s.backend}</span>
				<span class="mono text-xs text-[var(--al-muted)]">••••••</span>
				{#if s.backend === 'local'}
					<button class="rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDelete(s.name)} aria-label="Delete secret"><Trash2 size={14} /></button>
				{/if}
			</div>
		{/each}
	</div>

	<!-- add secret -->
	{#if localEnabled}
		<div class="flex items-end gap-2 border-t border-[var(--al-border)] pt-3">
			<div class="flex-1">
				<label for="sec-name" class="mb-1 block text-[10px] uppercase tracking-wide text-[var(--al-muted)]">name</label>
				<input id="sec-name" class="mono w-full {inp}" placeholder="db_password" bind:value={newName} />
			</div>
			<div class="flex-1">
				<label for="sec-val" class="mb-1 block text-[10px] uppercase tracking-wide text-[var(--al-muted)]">value</label>
				<input id="sec-val" type="password" class="w-full {inp}" placeholder="secret value" bind:value={newValue} onkeydown={(e) => { if (e.key === 'Enter') add(); }} />
			</div>
			<button class="inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40" onclick={add} disabled={busy || !newName.trim() || !newValue}>
				{#if busy}<Loader2 size={14} class="animate-spin" />{:else}<Plus size={14} />{/if} Add
			</button>
		</div>
	{/if}
</Modal>
