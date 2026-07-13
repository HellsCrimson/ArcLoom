<script lang="ts">
	// PlainField edits a plain JS value (no value-mode wrapper). It is used for
	// the interiors of containers (list/map/object), where slice-1 supports
	// literal values only. It recurses on itself for nested structures.
	import type { Field } from '$lib/types';
	import ListEditor from './ListEditor.svelte';
	import MapEditor from './MapEditor.svelte';
	import NestedBlock from './NestedBlock.svelte';
	import { objectElem } from './defaults';

	let { field, value = $bindable() }: { field: Field; value: unknown } = $props();

	// A `block` (list/set of objects) is edited as a list whose rows are nested
	// object forms. Synthesize the element field once.
	const blockAsList = $derived({ ...field, widget: 'list' as const, elem: objectElem(field) });

	const inputCls =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

{#if field.widget === 'text'}
	<input
		class={inputCls}
		type={field.sensitive ? 'password' : 'text'}
		bind:value={value as string}
		placeholder={field.type}
	/>
{:else if field.widget === 'textarea'}
	<textarea class="{inputCls} mono min-h-20" bind:value={value as string}></textarea>
{:else if field.widget === 'number'}
	<input class={inputCls} type="number" bind:value={value as number} />
{:else if field.widget === 'checkbox'}
	<!-- Use an explicit onchange (not bind:checked) so an untouched checkbox
	     stays undefined rather than writing `false` back on mount, which would
	     send unintended attribute values to the CLI. -->
	<label class="inline-flex cursor-pointer items-center gap-2 text-sm">
		<input
			type="checkbox"
			class="h-4 w-4 accent-[var(--al-accent)]"
			checked={value === true}
			onchange={(e) => (value = e.currentTarget.checked)}
		/>
		<span class="text-[var(--al-muted)]">{value === undefined ? '(unset)' : value ? 'true' : 'false'}</span>
	</label>
{:else if field.widget === 'list'}
	<ListEditor {field} bind:value={value as unknown[]} />
{:else if field.widget === 'block'}
	<ListEditor field={blockAsList} bind:value={value as unknown[]} />
{:else if field.widget === 'map'}
	<MapEditor {field} bind:value={value as Record<string, unknown>} />
{:else if field.widget === 'object'}
	<NestedBlock {field} bind:value={value as Record<string, unknown>} />
{:else}
	<input class={inputCls} type="text" bind:value={value as string} />
{/if}
