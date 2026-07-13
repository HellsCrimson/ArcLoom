<script lang="ts">
	// NestedBlock renders an object / single nested block: a nested form of its
	// child fields. For repeatable blocks (widget 'block' with multiple items),
	// the parent uses ListEditor with this as the element.
	import type { Field } from '$lib/types';
	import PlainField from './PlainField.svelte';
	import { isReadOnly } from '$lib/types';

	let { field, value = $bindable() }: { field: Field; value: Record<string, unknown> } = $props();

	// Initialize before children render; the $effect runs after first paint, so
	// the template must also guard against an undefined/non-object value.
	$effect(() => {
		if (typeof value !== 'object' || value === null || Array.isArray(value)) value = {};
	});

	const children = $derived((field.fields ?? []).filter((c) => !isReadOnly(c)));
	const ready = $derived(typeof value === 'object' && value !== null && !Array.isArray(value));
</script>

<div class="space-y-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] p-3">
	{#if ready}
		{#each children as child (child.name)}
		<div>
			<div class="mb-1 flex items-center gap-2">
				<span class="text-xs font-medium text-[var(--al-text)]">{child.name}</span>
				{#if child.required}<span class="text-[var(--al-danger)]">*</span>{/if}
				<span class="mono text-[10px] text-[var(--al-muted)]">{child.type}</span>
			</div>
			<PlainField field={child} bind:value={value[child.name]} />
			</div>
		{/each}
		{#if children.length === 0}
			<p class="text-xs text-[var(--al-muted)]">No editable fields.</p>
		{/if}
	{/if}
</div>
