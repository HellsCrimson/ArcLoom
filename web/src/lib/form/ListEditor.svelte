<script lang="ts">
	import type { AttrValue, Field } from '$lib/types';
	import FieldRenderer from './FieldRenderer.svelte';
	import { defaultAttr, elemField, isContainer } from './defaults';
	import { Plus, Trash2 } from 'lucide-svelte';

	let {
		field,
		items = $bindable(),
		references = [],
		secrets = [],
		onCreateSecret
	}: {
		field: Field;
		items: AttrValue[];
		references?: string[];
		secrets?: string[];
		onCreateSecret?: (name: string, value: string) => Promise<void>;
	} = $props();

	// Always work with an array (the parent may bind an undefined slot).
	$effect(() => {
		if (!Array.isArray(items)) items = [];
	});

	const elem = $derived(elemField(field));
	// list(string) etc. read best as plain rows; collapse the per-item mode toggle.
	const compactVal = $derived(!isContainer(elem));

	function add() {
		items = [...(items ?? []), defaultAttr(elem)];
	}
	function remove(i: number) {
		items = items.filter((_, idx) => idx !== i);
	}
</script>

<div class="space-y-2">
	{#each items ?? [] as item, i (item)}
		<div class="flex items-start gap-2">
			<div class="flex-1">
				<FieldRenderer field={elem} bind:value={items[i]} {references} {secrets} {onCreateSecret} compact={compactVal} />
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
