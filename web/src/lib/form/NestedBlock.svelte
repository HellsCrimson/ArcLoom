<script lang="ts">
	// NestedBlock renders an object / single nested block: a sub-form of its child
	// schema fields. Each child is an AttrValue entry (keyed by field name) so it
	// carries its own value-mode toggle. For repeatable blocks (widget 'block'
	// with multiple items), the parent uses ListEditor with an object element.
	import type { AttrEntry, Field } from '$lib/types';
	import FieldRenderer from './FieldRenderer.svelte';
	import { defaultAttr, isContainer } from './defaults';
	import { isReadOnly } from '$lib/types';

	let {
		field,
		entries = $bindable(),
		references = [],
		secrets = [],
		onCreateSecret
	}: {
		field: Field;
		entries: AttrEntry[];
		references?: string[];
		secrets?: string[];
		onCreateSecret?: (name: string, value: string) => Promise<void>;
	} = $props();

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

<!--
  No border/padding of its own: the parent FieldRenderer draws the fold rail
  around every container, and a card inside that rail was double chrome — two
  nested objects used to eat ~56px of width before a single value was drawn.
-->
<div class="space-y-2">
	{#each children as child (child.name)}
		{@const entry = entryFor(child.name)}
		{#if entry}
			{#if isContainer(child)}
				<div>
					<div class="mb-1 flex items-center gap-2">
						<span class="text-xs font-medium text-[var(--al-text)]">{child.name}</span>
						{#if child.required}<span class="text-[var(--al-danger)]">*</span>{/if}
						<span class="mono truncate text-[10px] text-[var(--al-muted)]" title={child.type}>{child.type}</span>
					</div>
					<FieldRenderer field={child} bind:value={entry.value} {references} {secrets} {onCreateSecret} />
				</div>
			{:else}
				<!-- A scalar field sits on ONE line with its label, and takes the
				     compact editor (mode toggle behind ƒx). Inside a nested object the
				     old three-line stack — label, four mode buttons, input — made an
				     object of five strings taller than the dialog. -->
				<div class="flex items-start gap-2">
					<span class="flex w-32 flex-none items-center gap-1 pt-1.5 text-xs font-medium text-[var(--al-text)]" title={child.type}>
						<span class="truncate">{child.name}</span>
						{#if child.required}<span class="flex-none text-[var(--al-danger)]">*</span>{/if}
					</span>
					<div class="min-w-0 flex-1">
						<FieldRenderer field={child} bind:value={entry.value} {references} {secrets} {onCreateSecret} compact />
					</div>
				</div>
			{/if}
		{/if}
	{/each}
	{#if children.length === 0}
		<p class="text-xs text-[var(--al-muted)]">No editable fields.</p>
	{/if}
</div>
