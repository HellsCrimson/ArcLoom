<script lang="ts">
	import Modal from './Modal.svelte';
	import type { DriftInfo, StateEntry } from '$lib/api/client';
	import type { DriftConfig } from '$lib/types';
	import { Loader2, RefreshCw, PenLine, Trash2, Settings, AlertTriangle, CheckCircle2, Check } from 'lucide-svelte';

	let {
		open = $bindable(false),
		entries,
		loading = false,
		busy = false,
		canDeploy = false,
		backendType = '',
		drift = null,
		onSaveDrift,
		onConfigureBackend,
		onRefresh,
		onRm,
		onMv,
		onAcceptDrift
	}: {
		open: boolean;
		entries: StateEntry[];
		loading?: boolean;
		busy?: boolean;
		canDeploy?: boolean;
		backendType?: string; // '' = default local state
		drift?: DriftInfo | null;
		onSaveDrift?: (cfg: DriftConfig) => void;
		onConfigureBackend?: () => void;
		onRefresh: () => void;
		onRm: (address: string) => void;
		onMv: (from: string, to: string) => void;
		/** Absorb the refreshed values into state (apply -refresh-only). */
		onAcceptDrift?: () => void;
	} = $props();

	// Editable copy of the drift schedule, re-seeded whenever fresh info loads.
	let driftEnabled = $state(false);
	let driftInterval = $state(60);
	$effect(() => {
		driftEnabled = drift?.config.enabled ?? false;
		driftInterval = drift?.config.intervalMinutes || 60;
	});
	const driftDirty = $derived(
		drift != null &&
			(driftEnabled !== drift.config.enabled ||
				driftInterval !== (drift.config.intervalMinutes || 60))
	);

	function driftStatusText(): string {
		const s = drift?.status;
		if (!s) return 'not checked yet';
		const at = new Date(s.checkedAt).toLocaleString();
		if (s.error) return `check failed at ${at}`;
		if (!s.drifted) return `matches the configuration at ${at}`;
		const parts: string[] = [];
		if (s.add) parts.push(`+${s.add}`);
		if (s.change) parts.push(`~${s.change}`);
		if (s.replace) parts.push(`±${s.replace}`);
		if (s.destroy) parts.push(`−${s.destroy}`);
		const outOfBand = s.outOfBand ? `, ${s.outOfBand} changed outside ArcLoom` : '';
		return `drift detected at ${at}${parts.length ? ` (${parts.join(' ')}${outOfBand})` : ''}`;
	}

	// Accepting drift rewrites state (never infrastructure), so the confirm spells
	// out exactly that.
	function askAcceptDrift() {
		if (
			confirm(
				'Update this project\'s state to match what the providers report?\n\n' +
					'This runs apply -refresh-only: no infrastructure is created, changed or destroyed — ' +
					'only the recorded state catches up. Useful after importing a state from another install, ' +
					'or when the state holds values the providers no longer report.'
			)
		)
			onAcceptDrift?.();
	}

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
	{#if drift}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-2">
			<div class="flex flex-wrap items-center gap-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Drift detection</span>
				{#if drift.status?.error}
					<span class="flex items-center gap-1 text-xs" style="color:#f85149;" title={drift.status.error}><AlertTriangle size={12} /> {driftStatusText()}</span>
				{:else if drift.status?.drifted}
					<span class="flex items-center gap-1 text-xs" style="color:#e3b341;"><AlertTriangle size={12} /> {driftStatusText()}</span>
				{:else if drift.status}
					<span class="flex items-center gap-1 text-xs" style="color:#3fb950;"><CheckCircle2 size={12} /> {driftStatusText()}</span>
				{:else if drift.config.enabled}
					<span class="text-xs text-[var(--al-muted)]">{driftStatusText()}</span>
				{/if}
				{#if canDeploy && onSaveDrift}
					<span class="ml-auto flex items-center gap-2">
						<label class="flex items-center gap-1.5 text-xs text-[var(--al-text)]">
							<input type="checkbox" checked={driftEnabled} onchange={(e) => (driftEnabled = e.currentTarget.checked)} />
							enabled
						</label>
						<label class="flex items-center gap-1 text-xs text-[var(--al-muted)]">
							every
							<input
								type="number"
								min="5"
								class="w-16 rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] px-1.5 py-0.5 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]"
								value={driftInterval}
								onchange={(e) => (driftInterval = Math.max(5, Number(e.currentTarget.value) || 60))}
							/>
							min
						</label>
						<button
							class="rounded-md border border-[var(--al-border)] px-2 py-0.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel)] disabled:opacity-40"
							disabled={!driftDirty || busy}
							onclick={() => onSaveDrift?.({ enabled: driftEnabled, intervalMinutes: driftInterval })}
						>Save</button>
					</span>
				{:else if drift.config.enabled}
					<span class="ml-auto text-xs text-[var(--al-muted)]">every {drift.config.intervalMinutes || 60} min</span>
				{/if}
			</div>
			{#if drift.status?.addresses?.length}
				<ul class="mt-1.5 max-h-32 space-y-0.5 overflow-y-auto">
					{#each drift.status.addresses as addr (addr)}
						<li class="mono truncate text-[11px] text-[var(--al-muted)]" title={addr}>~ {addr}</li>
					{/each}
				</ul>
				{#if (drift.status.add + drift.status.change + drift.status.destroy + drift.status.replace) > drift.status.addresses.length}
					<p class="text-[11px] text-[var(--al-muted)]">
						… and {drift.status.add + drift.status.change + drift.status.destroy + drift.status.replace - drift.status.addresses.length} more (see the run history entry)
					</p>
				{/if}
			{/if}
			<p class="mt-1 text-[11px] text-[var(--al-muted)]">
				Periodically runs a plan on the server and reports what it would change — the same comparison as the
				Plan button (configuration vs reality), so values a provider recomputes on every read (timestamps,
				status, hashes) never raise a false alarm. Resources the refresh also saw changed outside ArcLoom are
				counted separately from configuration edits you have not applied. Detected drift is badged in the
				project picker and recorded in run history.
			</p>
		</div>
	{/if}
	<div class="mb-2 flex items-center justify-between">
		<p class="text-xs text-[var(--al-muted)]">
			Resources tracked in this project's persisted state. Output of state operations streams to the run pane.
		</p>
		<span class="ml-3 flex flex-none items-center gap-1.5">
			{#if canDeploy && onAcceptDrift}
				<button
					class="flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40"
					onclick={askAcceptDrift}
					disabled={busy}
					title="Run apply -refresh-only: the recorded state catches up to reality, no infrastructure changes"
				>
					<Check size={12} /> Accept refreshed state
				</button>
			{/if}
			<button
				class="flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40"
				onclick={onRefresh}
				disabled={loading || busy}
			>
				{#if loading}<Loader2 size={12} class="animate-spin" />{:else}<RefreshCw size={12} />{/if} Refresh
			</button>
		</span>
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
