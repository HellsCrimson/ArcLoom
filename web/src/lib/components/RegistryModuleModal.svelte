<!--
  RegistryModuleModal — add / edit a registry module (name, source, version,
  raw-HCL inputs).

  Markup only. openRegModule() re-seeds the drafts UNCONDITIONALLY and is
  reachable from the palette, the canvas, jumpToAddress and the sidebar — and
  Ctrl+K is handled before the editable-target guard, so it can fire WHILE this
  modal is open. A seed-on-open $effect would be a no-op on that second open,
  leaving module A's drafts under module B's `editing`, and saveRegModule's
  rename path would then delete B and upsert a copy of A. Hence the drafts stay
  in +page and are threaded with $bindable; `editing` is read-only here.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { Loader2, Trash2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		editing,
		name = $bindable(''),
		source = $bindable(''),
		version = $bindable(''),
		rows = $bindable<{ key: string; expr: string }[]>([]),
		count = $bindable(''),
		forEach = $bindable(''),
		saving = false,
		onSave
	}: {
		open?: boolean;
		editing: string | null;
		name?: string;
		source?: string;
		version?: string;
		rows?: { key: string; expr: string }[];
		count?: string;
		forEach?: string;
		saving?: boolean;
		onSave: () => void;
	} = $props();
</script>

<Modal title={editing ? `Module "${editing}"` : 'Add registry module'} bind:open>
	<div class="space-y-3">
		<div class="flex gap-2">
			<div class="w-40">
				<label for="rm-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Name</label>
				<input id="rm-name" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="vpc" bind:value={name} />
			</div>
			<div class="min-w-0 flex-1">
				<label for="rm-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Source</label>
				<input id="rm-source" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="terraform-aws-modules/vpc/aws" bind:value={source} />
			</div>
			<div class="w-28">
				<label for="rm-version" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Version</label>
				<input id="rm-version" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="~> 5.0" bind:value={version} />
			</div>
		</div>
		<div class="flex items-end gap-3">
			<div>
				<label for="rm-count" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]" title="count meta-argument — an HCL expression, e.g. 3 or var.replicas">count</label>
				<input id="rm-count" class="mono w-28 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" placeholder="3 · var.n" bind:value={count} disabled={forEach.trim() !== ''} />
			</div>
			<div>
				<label for="rm-foreach" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]" title="for_each meta-argument — a set or map expression, e.g. var.teams">for_each</label>
				<input id="rm-foreach" class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" placeholder="var.teams · toset([…])" bind:value={forEach} disabled={count.trim() !== ''} />
			</div>
			{#if count.trim() !== '' || forEach.trim() !== ''}
				<p class="pb-1.5 text-xs text-[var(--al-muted)]">Inputs can use <span class="mono">{count.trim() !== '' ? 'count.index' : 'each.key'}</span>.</p>
			{/if}
		</div>
		<div>
			<div class="mb-1 flex items-center justify-between">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Inputs (raw HCL)</span>
				<button class="text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => (rows = [...rows, { key: '', expr: '' }])}>+ add input</button>
			</div>
			{#if rows.length === 0}
				<p class="text-xs text-[var(--al-muted)]">No inputs yet — the module is called with defaults.</p>
			{/if}
			<div class="space-y-1.5">
				{#each rows as row, i (i)}
					<div class="flex items-center gap-2">
						<input class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder="name" bind:value={row.key} />
						<input class="mono min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder={'value (HCL) — e.g. "10.0.0.0/16" or var.cidr'} bind:value={row.expr} />
						<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => (rows = rows.filter((_, j) => j !== i))} aria-label="Remove input"><Trash2 size={12} /></button>
					</div>
				{/each}
			</div>
		</div>
	</div>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onSave} disabled={saving || !name.trim() || !source.trim()}>{#if saving}<Loader2 size={14} class="animate-spin" />{/if} {editing ? 'Update' : 'Add'} module</button>
	{/snippet}
</Modal>
