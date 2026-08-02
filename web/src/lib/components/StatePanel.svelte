<script lang="ts">
	import Modal from './Modal.svelte';
	import type { DriftInfo, StateEntry } from '$lib/api/client';
	import type { DriftConfig, ImportBlock, MovedBlock } from '$lib/types';
	import { Loader2, RefreshCw, PenLine, Trash2, Settings, AlertTriangle, CheckCircle2, Check, Biohazard, HeartPulse, LockOpen, ChevronRight, ChevronDown, Folder, ArrowRight, Shuffle } from 'lucide-svelte';

	let {
		open = $bindable(false),
		entries,
		loading = false,
		busy = false,
		canDeploy = false,
		backendType = '',
		imports = [],
		moved = [],
		drift = null,
		onSaveDrift,
		onRemoveImport,
		onRemoveMoved,
		onBulkMove,
		onConfigureBackend,
		onRefresh,
		onRm,
		onMv,
		onAcceptDrift,
		onTaint,
		onUntaint,
		onForceUnlock
	}: {
		open: boolean;
		entries: StateEntry[];
		loading?: boolean;
		busy?: boolean;
		canDeploy?: boolean;
		backendType?: string; // '' = default local state
		/** Config-driven `import` blocks waiting to be applied. */
		imports?: ImportBlock[];
		onRemoveImport?: (id: string) => void;
		/** Config-driven `moved` blocks waiting to be applied. */
		moved?: MovedBlock[];
		onRemoveMoved?: (id: string) => void;
		/** Open the bulk move screen (several addresses at once). */
		onBulkMove?: () => void;
		drift?: DriftInfo | null;
		onSaveDrift?: (cfg: DriftConfig) => void;
		onConfigureBackend?: () => void;
		onRefresh: () => void;
		onRm: (address: string) => void;
		onMv: (from: string, to: string) => void;
		/** Absorb the refreshed values into state (apply -refresh-only). */
		onAcceptDrift?: () => void;
		/** Mark an instance damaged so the next apply replaces it. */
		onTaint?: (address: string) => void;
		/** Clear that mark (the remedy for a failed create-time provisioner). */
		onUntaint?: (address: string) => void;
		/** Release a lock a crashed run left behind. */
		onForceUnlock?: (lockId: string) => void;
	} = $props();

	// ── What a count / for_each actually expanded to ─────────────────────────
	// The state is the only authoritative answer: the config says `for_each =
	// var.teams`, the plan only lists resources that CHANGE, but the state lists
	// every instance that exists. Two levels are collapsed here:
	//   * a MODULE call with for_each puts each instance in the address as
	//     `module.team["alpha"]`, so entries are grouped by that;
	//   * a RESOURCE with count/for_each carries its index keys in `instances`,
	//     which used to be a hover-only tooltip behind a "×N" badge.

	/** Group key -> label. "" is the root module. */
	const groups = $derived.by(() => {
		const m = new Map<string, StateEntry[]>();
		for (const e of entries) {
			const k = e.module ?? '';
			const list = m.get(k);
			if (list) list.push(e);
			else m.set(k, [e]);
		}
		// Root first, then module instances alphabetically, so the tree is stable.
		return [...m.entries()].sort(([a], [b]) => (a === '' ? -1 : b === '' ? 1 : a.localeCompare(b)));
	});

	// Collapsed module groups and expanded instance lists, both keyed by address.
	let collapsed = $state<Record<string, boolean>>({});
	let openInstances = $state<Record<string, boolean>>({});
	const toggleGroup = (k: string) => (collapsed[k] = !collapsed[k]);
	const toggleInstances = (addr: string) => (openInstances[addr] = !openInstances[addr]);

	/** The engine address of one instance: `x.y[0]` / `x.y["a"]`. */
	const instanceAddr = (e: StateEntry, key: string) => `${e.address}[${key}]`;

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

	// Taint acts on ONE instance. It used to open a window.prompt listing the
	// keys, because there was no instance picker anywhere in the product; now the
	// expanded instance rows ARE the picker, so this just takes a full address.
	function askTaint(address: string) {
		if (
			confirm(
				`Mark ${address} as damaged?\n\nNothing changes now — the next apply destroys and recreates it.`
			)
		)
			onTaint?.(address);
	}

	function askUntaint(address: string) {
		if (
			confirm(
				`Clear the tainted mark on ${address}?\n\nThe engine stops treating it as damaged, so the next apply keeps it as-is.`
			)
		)
			onUntaint?.(address);
	}

	// One prompt does both jobs: it carries the warning and takes the ID. The ID
	// has to be typed because the lock is not visible from here — a remote backend
	// keeps it in the remote store, and the local backend's lock is an OS file
	// lock that dies with the process (so it never gets stuck). The engine prints
	// the ID in the error that refused the operation.
	function askForceUnlock() {
		const id = window.prompt(
			'Force-unlock this state?\n\n' +
				'Do this only when the run holding the lock is really gone (killed process, crashed container, ' +
				'connection lost mid-apply). Releasing a lock a LIVE run still holds lets two operations write ' +
				'the state at once, which can corrupt it.\n\n' +
				'The recorded state is not modified — only the lock is released.\n\n' +
				'Lock ID (the failed operation prints it as "Lock Info: ID:"):',
			''
		);
		if (id && id.trim()) onForceUnlock?.(id.trim());
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

	<!--
	  Config-driven imports. They are not state yet: they sit in imports.tf until
	  a plan/apply performs them, which is exactly why they need a home outside
	  the resource form — otherwise a half-finished adoption is invisible.
	-->
	{#if imports.length > 0}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5">
			<div class="mb-1 flex items-center gap-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Import blocks</span>
				<span class="text-[11px] text-[var(--al-muted)]">
					{imports.length} pending — the next apply adopts them; delete once they are in state.
				</span>
			</div>
			<div class="space-y-1">
				{#each imports as b (b.id)}
					<div class="flex items-start gap-2">
						<div class="mono min-w-0 flex-1 text-[11px] text-[var(--al-text)]">
							<div class="truncate" title={b.to}>to = {b.to}</div>
							<div class="truncate text-[var(--al-muted)]" title={b.importId}>id = {b.importId}</div>
							{#if b.forEach}
								<div class="truncate text-[var(--al-muted)]" title={b.forEach}>for_each = {b.forEach}</div>
							{/if}
						</div>
						{#if onRemoveImport}
							<button
								class="flex-none rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
								onclick={() => onRemoveImport?.(b.id)}
								aria-label="Remove import block"
							><Trash2 size={13} /></button>
						{/if}
					</div>
				{/each}
			</div>
		</div>
	{/if}
	<!--
	  Pending renames. Like the import blocks above they are not state yet: they
	  sit in moved.tf until an apply performs them, and a half-finished refactor
	  is otherwise invisible. Deliberately never auto-removed — a `moved` whose
	  source is gone is a no-op, and dropping one silently would erase the record
	  of the rename.
	-->
	{#if moved.length > 0}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5">
			<div class="mb-1 flex items-center gap-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Moved blocks</span>
				<span class="text-[11px] text-[var(--al-muted)]">
					{moved.length} pending — the next plan lists them and the apply performs them; delete once applied.
				</span>
			</div>
			<div class="space-y-0.5">
				{#each moved as b (b.id)}
					<div class="flex items-center gap-2">
						<span class="mono min-w-0 flex-1 truncate text-[11px] text-[var(--al-muted)]" title={b.from}>{b.from}</span>
						<ArrowRight size={11} class="flex-none text-[var(--al-muted)]" />
						<span class="mono min-w-0 flex-1 truncate text-[11px] text-[var(--al-text)]" title={b.to}>{b.to}</span>
						{#if onRemoveMoved}
							<button
								class="flex-none rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
								onclick={() => onRemoveMoved?.(b.id)}
								aria-label="Remove moved block"
							><Trash2 size={13} /></button>
						{/if}
					</div>
				{/each}
			</div>
		</div>
	{/if}
	{#if canDeploy && onForceUnlock}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-2">
			<div class="flex flex-wrap items-center gap-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">State lock</span>
				<span class="text-xs text-[var(--al-muted)]">
					{backendType
						? `held in the ${backendType} backend — not visible from here`
						: 'held only while an operation runs (local state)'}
				</span>
				<button
					class="ml-auto flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel)] disabled:opacity-40"
					onclick={askForceUnlock}
					disabled={busy}
					title="Release a lock a crashed run left behind (the recorded state is untouched)"
				>
					<LockOpen size={12} /> Force unlock…
				</button>
			</div>
			<p class="mt-1 text-[11px] text-[var(--al-muted)]">
				An operation the engine never got to finish — killed process, crashed container, connection lost
				mid-apply — can leave a remote backend's lock held by nobody, and every later operation then fails to
				acquire it. Force-unlock releases it; the recorded state is not modified. Take the ID from the
				“Lock Info: ID:” line of the operation that was refused.
			</p>
		</div>
	{/if}
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
			{#if onBulkMove}
				<!-- Renaming several addresses at once — the per-row PenLine below is
				     one prompt at a time, which is unusable past two. -->
				<button
					class="flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40"
					onclick={onBulkMove}
					disabled={busy}
					title="Move several addresses at once — e.g. after folding resources into one for_each'd block"
				>
					<Shuffle size={12} /> Bulk move…
				</button>
			{/if}
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
			{#each groups as [key, groupEntries] (key)}
				{#if key !== ''}
					<!-- One module INSTANCE: a for_each'd module call appears once per
					     key, as module.team["alpha"] — this is where those items become
					     visible instead of being a prefix buried in each address. -->
					<li>
						<button
							class="flex w-full items-center gap-1.5 rounded px-2 py-1 text-left hover:bg-[var(--al-panel-2)]"
							onclick={() => toggleGroup(key)}
							title="{groupEntries.length} resource{groupEntries.length === 1 ? '' : 's'} in {key}"
						>
							{#if collapsed[key]}<ChevronRight size={12} class="flex-none text-[var(--al-muted)]" />{:else}<ChevronDown size={12} class="flex-none text-[var(--al-muted)]" />{/if}
							<Folder size={12} style="color:#7aa2f7" class="flex-none" />
							<span class="mono min-w-0 flex-1 truncate text-xs text-[var(--al-text)]">{key}</span>
							<span class="mono flex-none text-[10px] text-[var(--al-muted)]">{groupEntries.length}</span>
						</button>
					</li>
				{/if}
				{#if key === '' || !collapsed[key]}
					{#each groupEntries as e (e.address)}
						{@const expandable = (e.instances?.length ?? 0) > 0}
						{@const shown = !!openInstances[e.address]}
						<li class="group flex items-center gap-2 rounded px-2 py-1 hover:bg-[var(--al-panel-2)]" class:pl-5={key !== ''}>
							{#if expandable}
								<button
									class="flex-none text-[var(--al-muted)] hover:text-[var(--al-text)]"
									onclick={() => toggleInstances(e.address)}
									title={shown ? 'Hide instances' : `Show the ${e.instances!.length} instances this expanded to`}
									aria-label={shown ? 'Hide instances' : 'Show instances'}
								>
									{#if shown}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
								</button>
							{/if}
							<span
								class="w-14 flex-none rounded px-1 py-0.5 text-center font-mono text-[10px] font-bold uppercase"
								style="background:{e.mode === 'data' ? '#56d4bc' : '#4c8dff'}22;color:{e.mode === 'data' ? '#56d4bc' : '#4c8dff'};"
							>{e.mode === 'data' ? 'data' : 'res'}</span>
							<span class="mono min-w-0 flex-1 truncate text-xs text-[var(--al-text)]" title={e.address}>{e.address}</span>
							{#if expandable}
								<button
									class="mono flex-none rounded px-1 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]"
									onclick={() => toggleInstances(e.address)}
									title="Expanded to {e.instances!.length} instances — click to list them"
								>×{e.instances!.length}</button>
							{/if}
							{#if e.tainted?.length}
								<span
									class="flex-none rounded px-1 py-0.5 text-[10px] font-semibold"
									style="background:#e3b34122;color:#e3b341;"
									title={`Marked damaged — the next apply replaces ${e.tainted.join(', ')}`}
								>tainted{e.tainted.length > 1 ? ` ×${e.tainted.length}` : ''}</span>
							{/if}
							{#if canDeploy}
								<span class="flex flex-none items-center gap-1 opacity-0 group-hover:opacity-100">
									<!-- Taint is per-instance, so an expandable resource offers it on
									     its instance rows instead of here. mv / rm act on the whole
									     resource (state rm removes every instance), which is still
									     the right granularity at this level. -->
									{#if e.mode !== 'data' && onTaint && !expandable}
										<button class="text-[var(--al-muted)] hover:text-[#e3b341] disabled:opacity-40" title="Mark damaged — the next apply replaces it" aria-label="Taint" onclick={() => askTaint(e.address)} disabled={busy}><Biohazard size={13} /></button>
									{/if}
									<button class="text-[var(--al-muted)] hover:text-[var(--al-accent)] disabled:opacity-40" title="Move / rename in state" aria-label="Move in state" onclick={() => askMv(e.address)} disabled={busy}><PenLine size={13} /></button>
									<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)] disabled:opacity-40" title={expandable ? 'Remove all instances from state (does not destroy)' : 'Remove from state (does not destroy)'} aria-label="Remove from state" onclick={() => askRm(e.address)} disabled={busy}><Trash2 size={13} /></button>
								</span>
							{/if}
						</li>

						{#if expandable && shown}
							{#each e.instances ?? [] as ikey (ikey)}
								{@const addr = instanceAddr(e, ikey)}
								{@const isTainted = (e.tainted ?? []).includes(addr)}
								<li
									class="group flex items-center gap-2 rounded py-0.5 pr-2 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]"
									style="padding-left:{key !== '' ? 44 : 32}px;"
								>
									<span class="mono min-w-0 flex-1 truncate text-[11px]" title={addr}>{addr}</span>
									{#if isTainted}
										<span class="flex-none rounded px-1 text-[10px] font-semibold" style="background:#e3b34122;color:#e3b341;">tainted</span>
									{/if}
									{#if canDeploy}
										<span class="flex flex-none items-center gap-1">
											{#if isTainted}
												<button class="text-[#e3b341] hover:brightness-125 disabled:opacity-40" title={`Clear the tainted mark on ${addr} (keeps the resource)`} aria-label="Untaint" onclick={() => askUntaint(addr)} disabled={busy}><HeartPulse size={13} /></button>
											{/if}
										</span>
										<span class="flex flex-none items-center gap-1 opacity-0 group-hover:opacity-100">
											{#if e.mode !== 'data' && onTaint && !isTainted}
												<button class="text-[var(--al-muted)] hover:text-[#e3b341] disabled:opacity-40" title={`Mark ${addr} damaged — the next apply replaces just this instance`} aria-label="Taint instance" onclick={() => askTaint(addr)} disabled={busy}><Biohazard size={13} /></button>
											{/if}
											<button class="text-[var(--al-muted)] hover:text-[var(--al-accent)] disabled:opacity-40" title={`Move / rename ${addr} in state`} aria-label="Move instance in state" onclick={() => askMv(addr)} disabled={busy}><PenLine size={13} /></button>
											<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)] disabled:opacity-40" title={`Remove just ${addr} from state (does not destroy)`} aria-label="Remove instance from state" onclick={() => askRm(addr)} disabled={busy}><Trash2 size={13} /></button>
										</span>
									{/if}
								</li>
							{/each}
						{/if}
					{/each}
				{/if}
			{/each}
		</ul>
	{/if}
	{#if busy}
		<p class="mt-2 flex items-center gap-1.5 text-xs text-[var(--al-muted)]"><Loader2 size={12} class="animate-spin" /> state operation running — see run output</p>
	{/if}
</Modal>
