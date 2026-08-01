<!--
  ImportResourceModal — adopt live resources into state at the form's address.

  `terraform import` takes ONE literal address and ONE literal ID: no
  expressions, so `each.value` cannot appear in it, and a resource with a
  count/for_each has to be imported one INSTANCE at a time
  (`null_resource.sub["www"]`). This modal is therefore a table when the block
  is multiplied — the keys arrive pre-filled whenever the model can resolve them
  (the same expansion the structure tree and the for_each panel use), and the
  caller runs one import per filled row, in order.

  Markup only: the rows live in +page (threaded with $bindable) and are seeded
  by its opener, never by an $effect here — every opener is re-entrant, so a
  seed-on-open effect would be a no-op the second time and leave the previous
  resource's rows under this one's address.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { HardDriveDownload, Plus, Trash2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		rows = $bindable<{ key: string; id: string }[]>([]),
		address,
		/** The block is multiplied (count/for_each), so imports are per instance. */
		multi = false,
		/** count -> [0]; for_each -> ["key"]. */
		kind = 'for_each',
		/** true when the keys were resolved from the model rather than typed. */
		keysKnown = false,
		busy = false,
		progress = '',
		onImport
	}: {
		open?: boolean;
		rows?: { key: string; id: string }[];
		address: string;
		multi?: boolean;
		kind?: 'count' | 'for_each';
		keysKnown?: boolean;
		busy?: boolean;
		progress?: string;
		onImport: () => void;
	} = $props();

	const inp =
		'mono w-full min-w-0 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]';
	const grid = 'grid grid-cols-[minmax(0,12rem)_minmax(0,1fr)_auto] items-center gap-2';

	function addr(key: string): string {
		const k = key.trim();
		if (!k) return address;
		return kind === 'count' ? `${address}[${k}]` : `${address}["${k}"]`;
	}

	const fillable = $derived(rows.filter((r) => r.id.trim() !== '' && (!multi || r.key.trim() !== '')));
</script>

<Modal title="Import into state" bind:open wide={multi}>
	{#if !multi}
		<p class="mb-3 text-sm text-[var(--al-text)]">
			Adopt an existing real-world resource as
			<span class="mono text-[var(--al-accent)]">{address}</span>.
			The provider reads it by ID and records it in this project's state — nothing is created or changed.
		</p>
		<label for="import-id" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Resource ID</label>
		<input
			id="import-id"
			class={inp}
			placeholder="provider-specific ID (e.g. i-0abc123, vpc-…, a value)"
			bind:value={rows[0].id}
			onkeydown={(e) => { if (e.key === 'Enter' && fillable.length) onImport(); }}
		/>
	{:else}
		<p class="mb-3 text-sm text-[var(--al-text)]">
			This block is multiplied, and the engine imports one <em>instance</em> at a time — an address like
			<span class="mono text-[var(--al-accent)]">{addr(rows[0]?.key || (kind === 'count' ? '0' : 'key'))}</span>.
			Give each instance the ID its provider knows it by; they are imported in order, and blank rows are skipped.
		</p>
		{#if !keysKnown}
			<p class="mb-3 text-[11px] text-[var(--al-muted)]">
				The keys are not resolvable from the config (the engine works them out), so type them.
			</p>
		{/if}
		<div class="{grid} mb-1 text-[10px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">
			<span>{kind === 'count' ? 'Index' : 'Key'}</span><span>Resource ID</span><span></span>
		</div>
		<div class="space-y-1.5">
			{#each rows as row, i (i)}
				<div class={grid}>
					<input class={inp} placeholder={kind === 'count' ? '0' : 'www'} bind:value={row.key} />
					<input
						class={inp}
						placeholder="ID for {addr(row.key)}"
						bind:value={row.id}
						onkeydown={(e) => { if (e.key === 'Enter' && fillable.length) onImport(); }}
					/>
					<button
						class="rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
						onclick={() => (rows = rows.filter((_, j) => j !== i))}
						aria-label="Remove instance"
					><Trash2 size={14} /></button>
				</div>
			{/each}
		</div>
		<button
			class="mt-2 inline-flex items-center gap-1 text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]"
			onclick={() => (rows = [...rows, { key: '', id: '' }])}
		><Plus size={12} /> add instance</button>
	{/if}
	<p class="mt-2 text-xs text-[var(--al-muted)]">Attributes that differ from the form will show as changes on the next plan.</p>
	{#if progress}
		<p class="mt-1.5 text-xs text-[var(--al-accent)]">{progress}</p>
	{/if}
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)} disabled={busy}>Cancel</button>
		<button class={btnPrimary} onclick={onImport} disabled={fillable.length === 0 || busy}>
			<HardDriveDownload size={14} />
			Import{multi ? ` ${fillable.length} instance${fillable.length === 1 ? '' : 's'}` : ''}
		</button>
	{/snippet}
</Modal>
