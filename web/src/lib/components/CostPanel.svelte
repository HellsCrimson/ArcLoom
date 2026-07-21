<script lang="ts">
	import { api, type CostOverride } from '$lib/api/client';
	import Modal from './Modal.svelte';
	import { Loader2, Plus, Trash2, DollarSign } from 'lucide-svelte';

	let {
		open = $bindable(false),
		projectId
	}: { open: boolean; projectId: string } = $props();

	type Row = { target: string; monthly: string; note: string };

	let enabled = $state(false);
	let rows = $state<Row[]>([]);
	let costAvailable = $state(true);
	let busy = $state(false);
	let err = $state('');
	let info = $state('');

	$effect(() => {
		if (open && projectId) load();
	});

	async function load() {
		err = '';
		info = '';
		try {
			const [res, lic] = await Promise.all([api.getCost(projectId), api.getLicense()]);
			costAvailable = lic.features.includes('cost');
			enabled = res.config.enabled;
			rows = (res.config.overrides ?? []).map((o) => ({
				target: o.target,
				monthly: String(o.monthly),
				note: o.note ?? ''
			}));
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function addRow() {
		rows = [...rows, { target: '', monthly: '', note: '' }];
	}

	function removeRow(i: number) {
		rows = rows.filter((_, idx) => idx !== i);
	}

	// A non-empty override or enabling requires the enterprise feature; disabling
	// with no overrides is always allowed (so an expired license can turn it off).
	const dirtyNeedsLicense = $derived(enabled || rows.some((r) => r.target.trim() !== ''));
	const saveBlocked = $derived(busy || (!costAvailable && dirtyNeedsLicense));

	async function save() {
		busy = true;
		err = '';
		info = '';
		try {
			const overrides: CostOverride[] = rows
				.filter((r) => r.target.trim() !== '')
				.map((r) => ({ target: r.target.trim(), monthly: Number(r.monthly) || 0, note: r.note.trim() || undefined }));
			const res = await api.setCost(projectId, { enabled, overrides });
			enabled = res.config.enabled;
			rows = (res.config.overrides ?? []).map((o) => ({
				target: o.target,
				monthly: String(o.monthly),
				note: o.note ?? ''
			}));
			info = 'Cost settings saved';
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const btnPrimary =
		'inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40';
	const btnGhost =
		'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]';
</script>

<Modal title="Cost estimation" bind:open wide>
	{#if err}
		<div class="mb-2 rounded-md border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{err}</div>
	{/if}
	{#if info}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-xs text-[var(--al-muted)]">{info}</div>
	{/if}

	<p class="mb-3 text-xs text-[var(--al-muted)]">
		When enabled, a plan shows an estimated monthly cost. Assign a flat monthly price to a resource
		<span class="mono text-[var(--al-text)]">type</span> (e.g. <span class="mono text-[var(--al-text)]">proxmox_vm</span>)
		or a full <span class="mono text-[var(--al-text)]">address</span> (e.g. <span class="mono text-[var(--al-text)]">aws_instance.web</span>).
		Overrides win over Infracost, and an address match wins over a type match. Infracost (when installed
		on the server, with an API key) prices supported cloud resources automatically.
	</p>

	{#if !costAvailable}
		<div class="mb-3 rounded-md border border-[var(--al-warn,#d29922)] bg-[var(--al-warn,#d29922)]/10 px-3 py-1.5 text-xs text-[var(--al-warn,#d29922)]">
			Cost estimation requires ArcLoom Enterprise. You can still disable it and clear overrides below.
		</div>
	{/if}

	<label class="mb-3 flex items-center gap-2 text-sm text-[var(--al-text)]">
		<input type="checkbox" checked={enabled} onchange={() => (enabled = !enabled)} class="accent-[var(--al-accent)]" />
		<DollarSign size={14} /> Estimate cost on every plan
	</label>

	<div class="overflow-hidden rounded-md border border-[var(--al-border)]">
		<div class="grid grid-cols-[1fr_7rem_1fr_auto] items-center gap-2 border-b border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">
			<span>Type or address</span>
			<span>Monthly (USD)</span>
			<span>Note</span>
			<span></span>
		</div>
		{#if rows.length === 0}
			<p class="px-3 py-3 text-xs text-[var(--al-muted)]">No price overrides. Add one below.</p>
		{/if}
		{#each rows as row, i (i)}
			<div class="grid grid-cols-[1fr_7rem_1fr_auto] items-center gap-2 border-b border-[var(--al-border)] px-3 py-1.5 last:border-b-0">
				<input class={inp} bind:value={row.target} placeholder="proxmox_vm or aws_instance.web" />
				<input class={inp} type="number" step="0.01" min="0" bind:value={row.monthly} placeholder="0.00" />
				<input class={inp} bind:value={row.note} placeholder="optional" />
				<button class={btnGhost} title="Remove" onclick={() => removeRow(i)}><Trash2 size={13} /></button>
			</div>
		{/each}
	</div>
	<button class="{btnGhost} mt-2" onclick={addRow}><Plus size={13} /> Add override</button>

	<div class="mt-4 flex justify-end">
		<button class={btnPrimary} onclick={save} disabled={saveBlocked}>
			{#if busy}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	</div>
</Modal>
