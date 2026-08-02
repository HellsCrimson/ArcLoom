<!--
  BulkMoveModal — move many state addresses at once.

  The refactor that needs this: several hand-written resources become one
  `for_each`'d block, so every address changes, and unless the state follows the
  next apply DESTROYS and recreates the real objects. The State panel could only
  offer one `state mv` at a time through a window.prompt, and nothing derived
  the new addresses from the for_each that motivated the move.

  Two ways to perform it, the same pair the import flow offers:

    moved block  CONFIG the next apply performs — the plan shows every rename,
                 it is reviewable in Git, undoable, and costs one plan instead
                 of N locked CLI runs. The default.
    Run now      one `terraform state mv` per pair, sequentially. For state
                 that has no config to attach a block to.

  Markup + selection only: the rows live in +page (threaded with $bindable) and
  are seeded by its opener, never by an $effect here — every opener is
  re-entrant, so a seed-on-open effect would be a no-op the second time.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { moveTo, pairRows, type MoveRow } from '$lib/model/bulkmove';
	import type { Expansion } from '$lib/model/expand';
	import { ArrowRight, FileCode2, Trash2, Zap, Eye } from 'lucide-svelte';

	let {
		open = $bindable(false),
		rows = $bindable<MoveRow[]>([]),
		target = $bindable(''),
		/** Every address currently in the state, parents and instances alike. */
		sources = [],
		/** Blocks the model knows, offered as targets (address + multiplicity). */
		targetOptions = [],
		/** Instances of the chosen target, when the model can resolve them. */
		expansion = null,
		/** Which meta-arg the chosen target carries; null = no index at all. */
		kind = null,
		/** Addresses the configuration still declares (see stillDeclared). */
		declared = [],
		mode = $bindable<'block' | 'now'>('block'),
		busy = false,
		progress = '',
		canResources = false,
		canDeploy = false,
		onAddBlocks,
		onRunNow,
		onDryRun
	}: {
		open?: boolean;
		rows?: MoveRow[];
		target?: string;
		sources?: string[];
		targetOptions?: { address: string; badge?: string }[];
		expansion?: Expansion | null;
		kind?: 'count' | 'for_each' | null;
		declared?: string[];
		mode?: 'block' | 'now';
		busy?: boolean;
		progress?: string;
		canResources?: boolean;
		canDeploy?: boolean;
		onAddBlocks: () => void;
		onRunNow: () => void;
		onDryRun: () => void;
	} = $props();

	const keys = $derived(expansion?.keys ?? []);
	const selected = $derived(new Set(rows.map((r) => r.from)));

	function toggle(addr: string) {
		rows = selected.has(addr)
			? rows.filter((r) => r.from !== addr)
			: pairRows([...rows, { from: addr, key: '' }], keys);
	}

	// Fill EMPTY keys as soon as a key list exists — picking the target after the
	// sources is the natural order, and it used to leave every row keyless
	// (so every destination was the bare target address, and the duplicate
	// warning fired on a screen the user had not finished filling in).
	//
	// Only empty keys: pairRows keeps any key that is already a valid one, so
	// this cannot fight an edit. It also cannot loop — a second pass finds
	// nothing empty, and when there are fewer keys than rows pairRows returns
	// the same keys and the write is skipped.
	$effect(() => {
		if (keys.length === 0 || !rows.some((r) => !r.key)) return;
		const next = pairRows(rows, keys);
		if (next.some((r, i) => r.key !== rows[i].key)) rows = next;
	});

	// The explicit version: re-pair EVERYTHING, discarding manual keys. Offered
	// as a button rather than done automatically for exactly that reason.
	function autoPair() {
		rows = pairRows(rows.map((r) => ({ ...r, key: '' })), keys);
	}

	const pairs = $derived(
		rows.map((r) => ({ from: r.from, to: moveTo(target, kind, r.key), key: r.key }))
	);
	const ready = $derived(
		pairs.length > 0 && pairs.every((p) => p.to !== '' && p.to !== p.from && (!kind || p.key.trim() !== ''))
	);
	const duplicates = $derived(new Set(pairs.map((p) => p.to).filter((to, i, all) => all.indexOf(to) !== i)));
	// A `moved` block whose source is STILL a declared resource is rejected by
	// the engine — the refactor it records has not happened in the config yet.
	// The CLI path reads no config, so this only applies to the block mode.
	const stillDeclared = $derived(
		mode === 'block'
			? pairs.map((p) => p.from.replace(/\[[^\]]*\]/g, '')).filter((a) => declared.includes(a))
			: []
	);

	const inp =
		'mono w-full min-w-0 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]';
</script>

<Modal title="Move state addresses" bind:open wide>
	<div class="mb-3 inline-flex overflow-hidden rounded-md border border-[var(--al-border)] text-xs">
		<button
			class="inline-flex items-center gap-1.5 px-2.5 py-1 {mode === 'block' ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}"
			onclick={() => (mode = 'block')}
		><FileCode2 size={12} /> moved block</button>
		<button
			class="inline-flex items-center gap-1.5 px-2.5 py-1 {mode === 'now' ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}"
			onclick={() => (mode = 'now')}
		><Zap size={12} /> Run now</button>
	</div>

	{#if mode === 'block'}
		<p class="mb-3 text-sm text-[var(--al-text)]">
			Writes one <span class="mono">moved</span> block per pair. The next
			<strong>plan lists every rename</strong> and the apply performs them — one plan instead of one locked
			CLI run per address, reviewable in Git and undoable until you apply.
		</p>
	{:else}
		<p class="mb-3 text-sm text-[var(--al-text)]">
			Runs <span class="mono">state mv</span> once per pair, in order, against the recorded state. Each takes
			the workspace lock, so they cannot overlap; a failure stops the run and reports how far it got — the
			moves that succeeded have already happened.
		</p>
	{/if}

	<div class="grid gap-3 md:grid-cols-2">
		<!-- 1. what to move -->
		<div>
			<div class="mb-1 flex items-center gap-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Move from</span>
				<span class="text-[11px] text-[var(--al-muted)]">{rows.length} selected</span>
			</div>
			{#if sources.length === 0}
				<p class="text-xs text-[var(--al-muted)]">State is empty — there is nothing to move.</p>
			{:else}
				<ul class="max-h-56 space-y-0.5 overflow-y-auto rounded-md border border-[var(--al-border)] p-1">
					{#each sources as addr (addr)}
						<li>
							<label class="flex cursor-pointer items-center gap-2 rounded px-1.5 py-0.5 hover:bg-[var(--al-panel-2)]">
								<input type="checkbox" checked={selected.has(addr)} onchange={() => toggle(addr)} />
								<span class="mono min-w-0 flex-1 truncate text-[11px] text-[var(--al-text)]" title={addr}>{addr}</span>
							</label>
						</li>
					{/each}
				</ul>
			{/if}
		</div>

		<!-- 2. where to -->
		<div>
			<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Move to</div>
			<select
				class="{inp} mb-1"
				value={targetOptions.some((o) => o.address === target) ? target : ''}
				onchange={(e) => (target = (e.currentTarget as HTMLSelectElement).value)}
			>
				<option value="">Pick a block…</option>
				{#each targetOptions as o (o.address)}
					<option value={o.address}>{o.address}{o.badge ? ` · ${o.badge}` : ''}</option>
				{/each}
			</select>
			<input class={inp} bind:value={target} placeholder="null_resource.sub · module.team.null_resource.sub" />
			{#if target.trim() && !kind}
				<!-- The gotcha worth saying out loud: you cannot move INTO an index
				     that the configuration does not describe yet. -->
				<p class="mt-1 text-[11px] text-[var(--al-warn)]">
					This block has no <span class="mono">count</span>/<span class="mono">for_each</span>, so the
					addresses carry no index — that is a plain rename. To fold several resources into one block, give
					it a <span class="mono">for_each</span> first: the engine refuses an indexed address the config
					does not declare.
				</p>
			{:else if kind}
				<p class="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-[var(--al-muted)]">
					<span>
						{kind === 'count' ? 'count' : 'for_each'} ·
						{#if keys.length}
							{keys.length} instance{keys.length === 1 ? '' : 's'}: <span class="mono">{keys.slice(0, 8).join(', ')}</span>{keys.length > 8 ? ' …' : ''}
						{:else}
							the engine resolves the keys — type them below
						{/if}
					</span>
					{#if keys.length}
						<button class="underline hover:text-[var(--al-accent)]" onclick={autoPair}>match automatically</button>
					{/if}
				</p>
			{/if}
		</div>
	</div>

	<!-- 3. the preview: old -> new, which is the whole point of the screen -->
	<div class="mt-3">
		<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Preview</div>
		{#if rows.length === 0}
			<p class="text-xs text-[var(--al-muted)]">Select the addresses to move.</p>
		{:else}
			<div class="max-h-56 space-y-1 overflow-y-auto">
				{#each rows as row, i (row.from)}
					{@const to = moveTo(target, kind, row.key)}
					<div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,6rem)_minmax(0,1fr)_auto] items-center gap-2">
						<span class="mono truncate text-[11px] text-[var(--al-muted)]" title={row.from}>{row.from}</span>
						<ArrowRight size={12} class="flex-none text-[var(--al-muted)]" />
						{#if kind}
							{#if keys.length}
								<select class={inp} bind:value={row.key}>
									<option value="">key…</option>
									{#each keys as k (k)}
										<option value={k}>{k}</option>
									{/each}
								</select>
							{:else}
								<input class={inp} bind:value={row.key} placeholder={kind === 'count' ? '0' : 'www'} />
							{/if}
						{:else}
							<span></span>
						{/if}
						<span
							class="mono truncate text-[11px]"
							style="color:{to && to !== row.from ? 'var(--al-accent)' : 'var(--al-muted)'};"
							title={to}
						>{to || '—'}{duplicates.has(to) ? ' ⚠' : ''}</span>
						<button
							class="rounded p-0.5 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
							onclick={() => (rows = rows.filter((_, j) => j !== i))}
							aria-label="Drop this pair"
						><Trash2 size={13} /></button>
					</div>
				{/each}
			</div>
			{#if stillDeclared.length > 0}
				<p class="mt-1 text-[11px] text-[var(--al-warn)]">
					Still declared in the configuration: <span class="mono">{[...new Set(stillDeclared)].join(', ')}</span>.
					A <span class="mono">moved</span> block is rejected while its source block still exists — delete the
					old resources first (folding them into the target is the refactor this records), or use
					<span class="mono">Run now</span>.
				</p>
			{/if}
			{#if duplicates.size > 0}
				<p class="mt-1 text-[11px] text-[var(--al-warn)]">
					Two sources point at the same destination — each instance can hold only one object.
				</p>
			{/if}
		{/if}
	</div>

	{#if progress}
		<p class="mt-2 text-xs text-[var(--al-accent)]">{progress}</p>
	{/if}

	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)} disabled={busy}>Cancel</button>
		{#if mode === 'now'}
			<!-- The dry run is the engine's own verdict on the pairs: it reports what
			     it WOULD move (and refuses a source that is not in the state) without
			     writing anything, which a locally-computed preview cannot do. -->
			<button class={btnGhost} onclick={onDryRun} disabled={!ready || busy || !canDeploy} title="Ask the engine what it would move — nothing is written">
				<Eye size={14} /> Dry run
			</button>
			<button class={btnPrimary} onclick={onRunNow} disabled={!ready || busy || duplicates.size > 0 || !canDeploy}>
				<Zap size={14} /> Move {pairs.length} address{pairs.length === 1 ? '' : 'es'}
			</button>
		{:else}
			<button class={btnPrimary} onclick={onAddBlocks} disabled={!ready || busy || duplicates.size > 0 || !canResources}>
				<FileCode2 size={14} /> Add {pairs.length} moved block{pairs.length === 1 ? '' : 's'}
			</button>
		{/if}
	{/snippet}
</Modal>
