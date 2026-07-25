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
	import { Plus, Trash2 } from 'lucide-svelte';

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

	type Kind = 'object' | 'list' | 'string' | 'number' | 'bool';

	// The kind is read back from the value itself — nothing else records it.
	const kind = $derived<Kind>(
		value.entries !== undefined
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
		required: false,
		optional: true,
		computed: false,
		sensitive: false
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

	const sel =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-1.5 py-1 text-[11px] text-[var(--al-muted)] outline-none focus:border-[var(--al-accent)]';
	const keyInp =
		'mono w-44 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const addBtn = 'text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]';
</script>

<div class="flex items-start gap-1.5">
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
	</select>

	{#if kind === 'object'}
		<div class="min-w-0 flex-1 rounded-md border border-[var(--al-border)] p-1.5">
			{#if (value.entries ?? []).length === 0}
				<p class="px-1 pb-1 text-[11px] text-[var(--al-muted)]">Empty object — add a key.</p>
			{/if}
			{#each value.entries ?? [] as entry, i (entry)}
				<div class="mb-1 flex items-start gap-1.5">
					<input
						class="{keyInp} {dupKeys.has(entry.key.trim()) ? 'border-[var(--al-danger)]' : ''}"
						placeholder="key"
						title={dupKeys.has(entry.key.trim()) ? `Duplicate key "${entry.key.trim()}" — keys must be unique in an object` : ''}
						bind:value={entry.key}
					/>
					<div class="min-w-0 flex-1">
						<Self bind:value={entry.value} {references} {secrets} {onCreateSecret} depth={depth + 1} />
					</div>
					<button class="mt-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => removeEntry(i)} aria-label="Remove key"><Trash2 size={12} /></button>
				</div>
			{/each}
			<button class={addBtn} onclick={addEntry}><Plus size={11} class="inline" /> add key</button>
			{#if dupKeys.size > 0}
				<p class="mt-1 text-[11px] text-[var(--al-danger)]">
					Duplicate key{dupKeys.size > 1 ? 's' : ''}: {[...dupKeys].join(', ')} — an object cannot repeat a key.
				</p>
			{/if}
		</div>
	{:else if kind === 'list'}
		<div class="min-w-0 flex-1 rounded-md border border-[var(--al-border)] p-1.5">
			{#if (value.items ?? []).length === 0}
				<p class="px-1 pb-1 text-[11px] text-[var(--al-muted)]">Empty list — add an item.</p>
			{/if}
			{#each value.items ?? [] as item, i (item)}
				<div class="mb-1 flex items-start gap-1.5">
					<span class="mono mt-1.5 w-6 flex-none text-right text-[10px] text-[var(--al-muted)]">{i}</span>
					<div class="min-w-0 flex-1">
						<Self bind:value={value.items![i]} {references} {secrets} {onCreateSecret} depth={depth + 1} />
					</div>
					<button class="mt-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => removeItem(i)} aria-label="Remove item"><Trash2 size={12} /></button>
				</div>
			{/each}
			<button class={addBtn} onclick={addItem}><Plus size={11} class="inline" /> add item</button>
		</div>
	{:else}
		<div class="min-w-0 flex-1">
			<FieldRenderer field={leafField} bind:value {references} {secrets} {onCreateSecret} compact />
		</div>
	{/if}
</div>
