<script lang="ts">
	import type { AttrEntry, Field } from '$lib/types';
	import FieldRenderer from './FieldRenderer.svelte';
	import { defaultAttr, elemField, isContainer } from './defaults';
	import { Plus, Trash2 } from 'lucide-svelte';

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

	// Always work with an array (the parent may bind an undefined slot).
	$effect(() => {
		if (!Array.isArray(entries)) entries = [];
	});

	const elem = $derived(elemField(field));
	// A primitive-valued map (map(string), map(number)…) reads best as clean
	// key→value pairs, so collapse each value's mode toggle behind "ƒx".
	const compactVal = $derived(!isContainer(elem));

	function add() {
		entries = [...(entries ?? []), { key: '', value: defaultAttr(elem) }];
	}
	function remove(i: number) {
		entries = entries.filter((_, idx) => idx !== i);
	}
</script>

<div class="space-y-2">
	{#each entries ?? [] as entry, i (entry)}
		{#if compactVal}
			<!-- map(string) and friends: the clean key→value pair, side by side. -->
			<div class="flex items-start gap-2">
				<input
					class="w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
					placeholder="key"
					bind:value={entry.key}
				/>
				<div class="min-w-0 flex-1">
					<FieldRenderer field={elem} bind:value={entry.value} {references} {secrets} {onCreateSecret} compact />
				</div>
				<button
					type="button"
					class="mt-1 flex-none rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
					onclick={() => remove(i)}
					aria-label="Remove entry"
				>
					<Trash2 size={16} />
				</button>
			</div>
		{:else}
			<!-- Container-valued entry (map(object({…}))): the key goes ABOVE its
			     value instead of beside it. Side by side, every level stole another
			     ~170px from the left, so by the third the value had no width left and
			     the dialog grew a horizontal scrollbar. -->
			<div class="rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)]/40 p-2">
				<div class="mb-1.5 flex items-center gap-2">
					<span class="mono flex-none text-[10px] uppercase tracking-wide text-[var(--al-muted)]">key</span>
					<input
						class="mono min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm outline-none focus:border-[var(--al-accent)]"
						placeholder="key"
						bind:value={entry.key}
					/>
					<button
						type="button"
						class="flex-none rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
						onclick={() => remove(i)}
						aria-label="Remove entry"
					>
						<Trash2 size={16} />
					</button>
				</div>
				<FieldRenderer field={elem} bind:value={entry.value} {references} {secrets} {onCreateSecret} />
			</div>
		{/if}
	{/each}
	<button
		type="button"
		class="inline-flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-muted)] hover:text-[var(--al-text)]"
		onclick={add}
	>
		<Plus size={14} /> Add entry
	</button>
</div>
