<script lang="ts">
	import Modal from './Modal.svelte';
	import type { StateEntry } from '$lib/api/client';
	import { Loader2, RefreshCw, PenLine, Trash2, Settings } from 'lucide-svelte';

	let {
		open = $bindable(false),
		entries,
		loading = false,
		busy = false,
		canDeploy = false,
		backendType = '',
		onConfigureBackend,
		onRefresh,
		onRm,
		onMv
	}: {
		open: boolean;
		entries: StateEntry[];
		loading?: boolean;
		busy?: boolean;
		canDeploy?: boolean;
		backendType?: string; // '' = default local state
		onConfigureBackend?: () => void;
		onRefresh: () => void;
		onRm: (address: string) => void;
		onMv: (from: string, to: string) => void;
	} = $props();

	function askMv(from: string) {
		const to = window.prompt(`Move (rename) state address\n\nfrom: ${from}\nto:`, from);
		if (to && to.trim() && to.trim() !== from) onMv(from, to.trim());
	}

	function askRm(address: string) {
		if (
			confirm(
				`Remove ${address} from state?\n\nThe real resource is NOT destroyed — it is simply forgotten; the next plan will want to create it again.`
			)
		)
			onRm(address);
	}
</script>

<Modal title="State" bind:open>
	<div class="mb-2 flex items-center gap-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5">
		<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Backend</span>
		<span class="mono text-xs" style="color:{backendType ? '#c297ff' : 'var(--al-text)'};">{backendType || 'local (project workspace)'}</span>
		{#if onConfigureBackend}
			<button class="ml-auto flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-0.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel)]" onclick={onConfigureBackend} disabled={busy}>
				<Settings size={12} /> Configure
			</button>
		{/if}
	</div>
	<div class="mb-2 flex items-center justify-between">
		<p class="text-xs text-[var(--al-muted)]">
			Resources tracked in this project's persisted state. Output of state operations streams to the run pane.
		</p>
		<button
			class="ml-3 flex flex-none items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40"
			onclick={onRefresh}
			disabled={loading || busy}
		>
			{#if loading}<Loader2 size={12} class="animate-spin" />{:else}<RefreshCw size={12} />{/if} Refresh
		</button>
	</div>

	{#if entries.length === 0}
		<p class="py-4 text-center text-sm text-[var(--al-muted)]">
			{loading ? 'Loading…' : 'State is empty — nothing has been applied or imported yet.'}
		</p>
	{:else}
		<ul class="max-h-80 space-y-0.5 overflow-y-auto">
			{#each entries as e (e.address)}
				<li class="group flex items-center gap-2 rounded px-2 py-1 hover:bg-[var(--al-panel-2)]">
					<span
						class="w-14 flex-none rounded px-1 py-0.5 text-center font-mono text-[10px] font-bold uppercase"
						style="background:{e.mode === 'data' ? '#56d4bc' : '#4c8dff'}22;color:{e.mode === 'data' ? '#56d4bc' : '#4c8dff'};"
					>{e.mode === 'data' ? 'data' : 'res'}</span>
					<span class="mono min-w-0 flex-1 truncate text-xs text-[var(--al-text)]" title={e.address}>{e.address}</span>
					{#if e.instances?.length}
						<span class="mono flex-none text-[10px] text-[var(--al-muted)]" title={e.instances.map((i) => `[${i}]`).join(' ')}>×{e.instances.length}</span>
					{/if}
					{#if canDeploy}
						<span class="flex flex-none items-center gap-1 opacity-0 group-hover:opacity-100">
							<button class="text-[var(--al-muted)] hover:text-[var(--al-accent)] disabled:opacity-40" title="Move / rename in state" aria-label="Move in state" onclick={() => askMv(e.address)} disabled={busy}><PenLine size={13} /></button>
							<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)] disabled:opacity-40" title="Remove from state (does not destroy)" aria-label="Remove from state" onclick={() => askRm(e.address)} disabled={busy}><Trash2 size={13} /></button>
						</span>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
	{#if busy}
		<p class="mt-2 flex items-center gap-1.5 text-xs text-[var(--al-muted)]"><Loader2 size={12} class="animate-spin" /> state operation running — see run output</p>
	{/if}
</Modal>
