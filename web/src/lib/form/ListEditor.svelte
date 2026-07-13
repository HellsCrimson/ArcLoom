<script lang="ts">
	import type { Field } from '$lib/types';
	import PlainField from './PlainField.svelte';
	import { defaultForField } from './defaults';
	import { Plus, Trash2 } from 'lucide-svelte';

	let { field, value = $bindable() }: { field: Field; value: unknown[] } = $props();

	// Ensure we always work with an array.
	$effect(() => {
		if (!Array.isArray(value)) value = [];
	});

	const elem = $derived(field.elem ?? ({ ...field, widget: 'text' } as Field));

	function add() {
		value = [...(value ?? []), defaultForField(elem)];
	}
	function remove(i: number) {
		value = value.filter((_, idx) => idx !== i);
	}
</script>

<div class="space-y-2">
	{#each value ?? [] as _, i (i)}
		<div class="flex items-start gap-2">
			<div class="flex-1">
				<PlainField field={elem} bind:value={value[i]} />
			</div>
			<button
				type="button"
				class="mt-1 rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
				onclick={() => remove(i)}
				aria-label="Remove item"
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
		<Plus size={14} /> Add item
	</button>
</div>
