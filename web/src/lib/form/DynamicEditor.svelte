<script lang="ts">
	// Editor for a value whose type the provider does not declare (a framework
	// DynamicAttribute — e.g. arcane_notification.config). There is no child
	// schema to render, so the SHAPE is part of the editing: each node picks
	// whether it is an object, a list, or a scalar, and objects/lists nest
	// arbitrarily deep. The result is the same structural AttrValue (entries /
	// items / literal) the typed collection editors produce, so hclgen emits a
	// normal HCL object and references/secrets can sit at any leaf.
	import type { AttrValue, Field } from '$lib/types';
	import FieldRenderer from './FieldRenderer.svelte';
	import Self from './DynamicEditor.svelte'; // recursion: nested objects/lists
	import { ChevronDown, ChevronRight, Plus, Trash2 } from 'lucide-svelte';

	let {
		value = $bindable(),
		references = [],
		secrets = [],
		onCreateSecret,
		depth = 0
	}: {
		value: AttrValue;
		references?: string[];
		secrets?: string[];
		onCreateSecret?: (name: string, value: string) => Promise<void>;
		depth?: number;
	} = $props();

	type Kind = 'object' | 'list' | 'string' | 'number' | 'bool' | 'secret';

	// The kind is read back from the value itself — nothing else records it.
	// A secret is offered here rather than only behind the leaf's ƒx toggle: a
	// token inside a provider config object is one of the main reasons to reach
	// for one, and nobody finds it hidden two clicks deep.
	const kind = $derived<Kind>(
		value.mode === 'secret'
			? 'secret'
			: value.entries !== undefined
				? 'object'
				: value.items !== undefined
					? 'list'
					: typeof value.literal === 'boolean'
						? 'bool'
						: typeof value.literal === 'number'
							? 'number'
							: 'string'
	);

	// Switching kind rewrites the node: shapes have nothing meaningful in common,
	// and keeping a stale `literal` next to `entries` would confuse the wire form.
	// Scalars are seeded with a concrete zero value rather than left unset: the
	// kind is read back FROM the value, so an unset number would immediately read
	// as a string again — and an unset leaf is pruned on save, silently dropping
	// the key the user just created.
	function setKind(k: Kind) {
		if (k === 'secret') {
			value.mode = 'secret';
			value.entries = undefined;
			value.items = undefined;
			value.literal = undefined;
			return;
		}
		// Leaving a non-literal mode (secret / reference / expression, the last two
		// reachable through the leaf's ƒx toggle) drops what only that mode used.
		if (value.mode !== 'literal') {
			value.mode = 'literal';
			value.secretRef = undefined;
			value.ref = undefined;
			value.expr = undefined;
		}
		value.entries = k === 'object' ? (value.entries ?? []) : undefined;
		value.items = k === 'list' ? (value.items ?? []) : undefined;
		if (k === 'object' || k === 'list') {
			value.literal = undefined;
			return;
		}
		value.literal = k === 'bool' ? false : k === 'number' ? 0 : '';
	}

	// Scalars reuse the ordinary field editor (compact), so a leaf can still be a
	// reference, an expression or a secret — the usual reason to reach for one
	// here is a token inside an otherwise plain config object.
	const leafField = $derived<Field>({
		name: '',
		widget: kind === 'bool' ? 'checkbox' : kind === 'number' ? 'number' : 'text',
		type: kind === 'bool' ? 'bool' : kind === 'number' ? 'number' : 'string',
		sensitive: kind === 'secret',
		required: false,
		optional: true,
		computed: false
	});

	// Duplicate keys would collapse into one HCL attribute (the server rejects the
	// save), so they are flagged where they are typed.
	const dupKeys = $derived.by(() => {
		const seen = new Set<string>();
		const dup = new Set<string>();
		for (const e of value.entries ?? []) {
			const k = e.key.trim();
			if (k === '') continue;
			if (seen.has(k)) dup.add(k);
			seen.add(k);
		}
		return dup;
	});

	function addEntry() {
		value.entries = [...(value.entries ?? []), { key: '', value: { mode: 'literal', literal: '' } }];
	}
	function removeEntry(i: number) {
		value.entries = (value.entries ?? []).filter((_, idx) => idx !== i);
	}
	function addItem() {
		value.items = [...(value.items ?? []), { mode: 'literal', literal: '' }];
	}
	function removeItem(i: number) {
		value.items = (value.items ?? []).filter((_, idx) => idx !== i);
	}

	// Containers fold, like every other container editor. The kind selector moves
	// into the fold header rather than sitting to the LEFT of the box: as a
	// sibling it pushed each level ~96px further right, so a third level had
	// nothing left to draw in.
	let folded = $state(false);
	const childCount = $derived(kind === 'object' ? (value.entries ?? []).length : (value.items ?? []).length);
	const summary = $derived(
		kind === 'object'
			? `${childCount} ${childCount === 1 ? 'key' : 'keys'}`
			: `${childCount} ${childCount === 1 ? 'item' : 'items'}`
	);

	const sel =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-1.5 py-1 text-[11px] text-[var(--al-muted)] outline-none focus:border-[var(--al-accent)]';
	const keyInp =
		'mono min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const addBtn = 'text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]';
</script>

{#snippet kindSelect()}
	<select
		class={sel}
		value={kind}
		onchange={(e) => setKind(e.currentTarget.value as Kind)}
		aria-label="Value kind"
		title="What this value is: an object, a list, or a scalar"
	>
		<option value="object">{'{ }'} object</option>
		<option value="list">[ ] list</option>
		<option value="string">abc string</option>
		<option value="number">123 number</option>
		<option value="bool">✓ bool</option>
		<option value="secret">🔒 secret</option>
	</select>
{/snippet}

{#if kind === 'object' || kind === 'list'}
	<div>
		<!-- Fold header: kind selector + child count. The body sits under a thin
		     rail, so one level of nesting costs ~10px of width instead of ~110. -->
		<div class="flex items-center gap-1.5">
			<button
				type="button"
				class="inline-flex items-center gap-1 rounded px-0.5 py-0.5 text-[11px] text-[var(--al-muted)] hover:text-[var(--al-text)]"
				onclick={() => (folded = !folded)}
				aria-expanded={!folded}
			>
				{#if folded}<ChevronRight size={12} />{:else}<ChevronDown size={12} />{/if}
			</button>
			{@render kindSelect()}
			<span class="mono text-[11px] text-[var(--al-muted)]">{summary}</span>
		</div>
		{#if !folded}
			<div class="mt-1 border-l border-[var(--al-border)] pl-2">
				{#if kind === 'object'}
					{#if (value.entries ?? []).length === 0}
						<p class="pb-1 text-[11px] text-[var(--al-muted)]">Empty object — add a key.</p>
					{/if}
					{#each value.entries ?? [] as entry, i (entry)}
						{@const nested = entry.value.entries !== undefined || entry.value.items !== undefined}
						{@const dup = dupKeys.has(entry.key.trim())}
						{@const dupTitle = dup ? `Duplicate key "${entry.key.trim()}" — keys must be unique in an object` : ''}
						{#if nested}
							<!-- A container child puts its key on its own line: beside the
							     value, the key column stacked up per level and squeezed the
							     value out (same rule as MapEditor). -->
							<div class="mb-1.5 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)]/40 p-1.5">
								<div class="mb-1 flex items-center gap-1.5">
									<span class="mono flex-none text-[10px] uppercase tracking-wide text-[var(--al-muted)]">key</span>
									<input
										class="{keyInp} max-w-xs {dup ? 'border-[var(--al-danger)]' : ''}"
										placeholder="key"
										title={dupTitle}
										bind:value={entry.key}
									/>
									<span class="flex-1"></span>
									<button class="flex-none text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => removeEntry(i)} aria-label="Remove key"><Trash2 size={12} /></button>
								</div>
								<Self bind:value={entry.value} {references} {secrets} {onCreateSecret} depth={depth + 1} />
							</div>
						{:else}
							<div class="mb-1 flex items-start gap-1.5">
								<input
									class="{keyInp} max-w-44 {dup ? 'border-[var(--al-danger)]' : ''}"
									placeholder="key"
									title={dupTitle}
									bind:value={entry.key}
								/>
								<div class="min-w-0 flex-1">
									<Self bind:value={entry.value} {references} {secrets} {onCreateSecret} depth={depth + 1} />
								</div>
								<button class="mt-1 flex-none text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => removeEntry(i)} aria-label="Remove key"><Trash2 size={12} /></button>
							</div>
						{/if}
					{/each}
					<button class={addBtn} onclick={addEntry}><Plus size={11} class="inline" /> add key</button>
					{#if dupKeys.size > 0}
						<p class="mt-1 text-[11px] text-[var(--al-danger)]">
							Duplicate key{dupKeys.size > 1 ? 's' : ''}: {[...dupKeys].join(', ')} — an object cannot repeat a key.
						</p>
					{/if}
				{:else}
					{#if (value.items ?? []).length === 0}
						<p class="pb-1 text-[11px] text-[var(--al-muted)]">Empty list — add an item.</p>
					{/if}
					{#each value.items ?? [] as item, i (item)}
						<div class="mb-1 flex items-start gap-1.5">
							<span class="mono mt-1.5 w-6 flex-none text-right text-[10px] text-[var(--al-muted)]">{i}</span>
							<div class="min-w-0 flex-1">
								<Self bind:value={value.items![i]} {references} {secrets} {onCreateSecret} depth={depth + 1} />
							</div>
							<button class="mt-1 flex-none text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => removeItem(i)} aria-label="Remove item"><Trash2 size={12} /></button>
						</div>
					{/each}
					<button class={addBtn} onclick={addItem}><Plus size={11} class="inline" /> add item</button>
				{/if}
			</div>
		{/if}
	</div>
{:else}
	<div class="flex items-start gap-1.5">
		{@render kindSelect()}
		<div class="min-w-0 flex-1">
			<FieldRenderer field={leafField} bind:value {references} {secrets} {onCreateSecret} compact />
		</div>
	</div>
{/if}
