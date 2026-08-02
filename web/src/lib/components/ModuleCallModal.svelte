<!--
  ModuleCallModal — add / edit ONE `module "<name>"` block: its name (the
  address), source, version, meta-args and arguments. It serves both kinds of
  source, because they are the same block: a registry address
  ("terraform-aws-modules/vpc/aws") or a local definition in this project
  ("./modules/team"). Editing a call never touches the definition — that is what
  lets one module be called under several names.

  Markup only. openCall() re-seeds the drafts UNCONDITIONALLY and is reachable
  from the palette, the canvas, jumpToAddress and the sidebar — and Ctrl+K is
  handled before the editable-target guard, so it can fire WHILE this modal is
  open. A seed-on-open $effect would be a no-op on that second open, leaving
  call A's drafts under call B's `editing`, and the save's rename path would
  then move B and write a copy of A. Hence the drafts stay in +page and are
  threaded with $bindable; `editing` is read-only here.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import ForEachPanel from '$lib/form/ForEachPanel.svelte';
	import ForEachField from '$lib/form/ForEachField.svelte';
	import type { Scope } from '$lib/model/expand';
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
		scope = {},
		onOpenDefinition,
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
		/** Root variables + locals, for the for_each panel (a call is root-scope). */
		scope?: Scope;
		onOpenDefinition?: () => void;
		saving?: boolean;
		onSave: () => void;
	} = $props();

	// A local source points at a definition in this project: it has no registry
	// version, and its declared variables are what seeded the input rows.
	const isLocal = $derived(/^\.\/modules\/[^/]+$/.test(source.trim()));
</script>

<Modal title={editing ? `Module call "${editing}"` : 'Add module call'} bind:open>
	<div class="space-y-3">
		<div class="flex gap-2">
			<div class="w-40">
				<label for="rm-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]" title="The block label — this call's address is module.<name>">Name</label>
				<input id="rm-name" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="vpc" bind:value={name} />
			</div>
			<div class="min-w-0 flex-1">
				<label for="rm-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Source</label>
				<input id="rm-source" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="terraform-aws-modules/vpc/aws · ./modules/team" bind:value={source} />
			</div>
			<div class="w-28">
				<label for="rm-version" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Version</label>
				<input id="rm-version" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="~> 5.0" bind:value={version} disabled={isLocal} title={isLocal ? 'A local module has no version — it is the code in this project' : ''} />
			</div>
		</div>
		<!-- Keyed like the resource form's: the mode is derived from the text once
		     per mount, and re-opening the modal over an already-open one (every
		     opener is re-entrant) re-seeds the drafts without remounting. -->
		{#key editing ?? ''}
			<ForEachField bind:count={count} bind:forEach={forEach} {scope} />
		{/key}
		{#if count.trim() !== '' || forEach.trim() !== ''}
			<p class="text-xs text-[var(--al-muted)]">
				Inputs below can use <span class="mono">{count.trim() !== '' ? 'count.index' : 'each.key / each.value'}</span> —
				they are raw HCL here, so write the address directly.
			</p>
		{/if}
		<!-- The members this call iterates: a module's arguments are written in
		     terms of each.value, so the set belongs next to them. -->
		<ForEachPanel {forEach} {count} {scope} {onOpenDefinition} />
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
		<button class={btnPrimary} onclick={onSave} disabled={saving || !name.trim() || !source.trim()}>{#if saving}<Loader2 size={14} class="animate-spin" />{/if} {editing ? 'Update' : 'Add'} call</button>
	{/snippet}
</Modal>
