<script lang="ts">
	import type { Field } from '$lib/types';
	import PlainField from './PlainField.svelte';
	import { defaultForField } from './defaults';
	import { Plus, Trash2 } from 'lucide-svelte';

	let { field, value = $bindable() }: { field: Field; value: Record<string, unknown> } = $props();

	const elem = $derived(field.elem ?? ({ ...field, widget: 'text' } as Field));

	// Rows are the source of truth so keys can be renamed without remounting the
	// value editors. They are synced into the bound value object reactively.
	let rows = $state<Array<{ k: string; v: unknown }>>(
		Object.entries(value ?? {}).map(([k, v]) => ({ k, v }))
	);

	$effect(() => {
		const out: Record<string, unknown> = {};
		for (const r of rows) if (r.k) out[r.k] = r.v;
		value = out;
	});

	function add() {
		rows = [...rows, { k: '', v: defaultForField(elem) }];
	}
	function remove(i: number) {
		rows = rows.filter((_, idx) => idx !== i);
	}
</script>

<div class="space-y-2">
	{#each rows as row, i (i)}
		<div class="flex items-start gap-2">
			<input
				class="w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
				placeholder="key"
				bind:value={row.k}
			/>
			<div class="flex-1">
				<PlainField field={elem} bind:value={row.v} />
			</div>
			<button
				type="button"
				class="mt-1 rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
				onclick={() => remove(i)}
				aria-label="Remove entry"
			>
				<Trash2 size={16} />
			</button>
		</div>
	{/each}
	<button
		type="button"
		class="inline-flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-muted)] hover:text-[var(--al-text)]"
		onclick={add}
	>
		<Plus size={14} /> Add entry
	</button>
</div>
