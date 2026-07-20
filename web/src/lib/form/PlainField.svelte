<script lang="ts">
	// PlainField is the literal-mode editor for one value. Scalars edit the raw
	// `value.literal`; containers edit the structural `value.items` / `value.entries`
	// (each element itself an AttrValue with its own mode toggle), so a reference
	// or secret can live inside a list/map/object.
	import type { AttrValue, Field } from '$lib/types';
	import ListEditor from './ListEditor.svelte';
	import MapEditor from './MapEditor.svelte';
	import NestedBlock from './NestedBlock.svelte';
	import { ensureLiteralShape } from './defaults';

	let {
		field,
		value = $bindable(),
		references = [],
		secrets = []
	}: { field: Field; value: AttrValue; references?: string[]; secrets?: string[] } = $props();

	// Seed items/entries for container widgets (idempotent) before the structural
	// editors bind. Also migrates legacy `literal` collections on first open.
	$effect(() => {
		ensureLiteralShape(field, value);
	});

	const inputCls =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

{#if field.widget === 'text'}
	<input
		class={inputCls}
		type={field.sensitive ? 'password' : 'text'}
		bind:value={value.literal as string}
		placeholder={field.type}
	/>
{:else if field.widget === 'textarea'}
	<textarea class="{inputCls} mono min-h-20" bind:value={value.literal as string}></textarea>
{:else if field.widget === 'number'}
	<input class={inputCls} type="number" bind:value={value.literal as number} />
{:else if field.widget === 'checkbox'}
	<!-- Use an explicit onchange (not bind:checked) so an untouched checkbox
	     stays undefined rather than writing `false` back on mount, which would
	     send unintended attribute values to the CLI. -->
	<label class="inline-flex cursor-pointer items-center gap-2 text-sm">
		<input
			type="checkbox"
			class="h-4 w-4 accent-[var(--al-accent)]"
			checked={value.literal === true}
			onchange={(e) => (value.literal = e.currentTarget.checked)}
		/>
		<span class="text-[var(--al-muted)]"
			>{value.literal === undefined ? '(unset)' : value.literal ? 'true' : 'false'}</span
		>
	</label>
{:else if field.widget === 'list' || field.widget === 'block'}
	<ListEditor {field} bind:items={value.items as AttrValue[]} {references} {secrets} />
{:else if field.widget === 'map'}
	<MapEditor {field} bind:entries={value.entries!} {references} {secrets} />
{:else if field.widget === 'object'}
	<NestedBlock {field} bind:entries={value.entries!} {references} {secrets} />
{:else}
	<input class={inputCls} type="text" bind:value={value.literal as string} />
{/if}
