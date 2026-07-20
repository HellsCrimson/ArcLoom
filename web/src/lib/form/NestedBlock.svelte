<script lang="ts">
	// NestedBlock renders an object / single nested block: a sub-form of its child
	// schema fields. Each child is an AttrValue entry (keyed by field name) so it
	// carries its own value-mode toggle. For repeatable blocks (widget 'block'
	// with multiple items), the parent uses ListEditor with an object element.
	import type { AttrEntry, Field } from '$lib/types';
	import FieldRenderer from './FieldRenderer.svelte';
	import { defaultAttr } from './defaults';
	import { isReadOnly } from '$lib/types';

	let {
		field,
		entries = $bindable(),
		references = [],
		secrets = []
	}: { field: Field; entries: AttrEntry[]; references?: string[]; secrets?: string[] } = $props();

	const children = $derived((field.fields ?? []).filter((c) => !isReadOnly(c)));

	// Ensure an entry exists for every editable child, so each FieldRenderer has a
	// stable slot to bind. Runs after the parent seeds `entries` (may be undefined
	// on the first paint).
	$effect(() => {
		if (!Array.isArray(entries)) {
			entries = [];
			return;
		}
		for (const c of children) {
			if (!entries.some((e) => e.key === c.name)) {
				entries = [...entries, { key: c.name, value: defaultAttr(c) }];
			}
		}
	});

	function entryFor(name: string): AttrEntry | undefined {
		return entries?.find((e) => e.key === name);
	}
</script>

<div class="space-y-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] p-3">
	{#each children as child (child.name)}
		{@const entry = entryFor(child.name)}
		{#if entry}
			<div>
				<div class="mb-1 flex items-center gap-2">
					<span class="text-xs font-medium text-[var(--al-text)]">{child.name}</span>
					{#if child.required}<span class="text-[var(--al-danger)]">*</span>{/if}
					<span class="mono text-[10px] text-[var(--al-muted)]">{child.type}</span>
				</div>
				<FieldRenderer field={child} bind:value={entry.value} {references} {secrets} />
			</div>
		{/if}
	{/each}
	{#if children.length === 0}
		<p class="text-xs text-[var(--al-muted)]">No editable fields.</p>
	{/if}
</div>
