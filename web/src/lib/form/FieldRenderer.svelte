<script lang="ts">
	// FieldRenderer is the editor for one value at any nesting depth. It adds the
	// Literal / Reference / Expression / Secret value-mode toggle around the
	// underlying literal editor (PlainField). It is used both for top-level
	// attributes and — recursively — for the interior of lists/maps/objects, so a
	// reference or secret can be placed inside a collection.
	import type { AttrValue, Field, ValueMode } from '$lib/types';
	import PlainField from './PlainField.svelte';
	import { ensureLiteralShape } from './defaults';

	let {
		field,
		value = $bindable(),
		references = [],
		secrets = []
	}: { field: Field; value: AttrValue; references?: string[]; secrets?: string[] } = $props();

	const modes: { id: ValueMode; label: string }[] = [
		{ id: 'literal', label: 'Literal' },
		{ id: 'reference', label: 'Reference' },
		{ id: 'expression', label: 'Expression' },
		{ id: 'secret', label: 'Secret' }
	];

	function setMode(m: ValueMode) {
		value.mode = m;
		if (m === 'literal') ensureLiteralShape(field, value);
	}

	const inputCls =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

<div>
	<div class="mb-1.5 inline-flex overflow-hidden rounded-md border border-[var(--al-border)] text-[11px]">
		{#each modes as m (m.id)}
			<button
				type="button"
				class="px-2 py-0.5 {value.mode === m.id
					? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]'
					: 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}"
				onclick={() => setMode(m.id)}
			>
				{m.label}
			</button>
		{/each}
	</div>

	{#if value.mode === 'reference'}
		{#if references.length > 0}
			<select class={inputCls} bind:value={value.ref}>
				<option value="" disabled selected={!value.ref}>Select a reference…</option>
				{#each references as ref (ref)}
					<option value={ref}>{ref}</option>
				{/each}
			</select>
		{:else}
			<input class="{inputCls} mono" placeholder="type.name.attr" bind:value={value.ref} />
		{/if}
	{:else if value.mode === 'expression'}
		<input class="{inputCls} mono" placeholder="raw HCL expression" bind:value={value.expr} />
	{:else if value.mode === 'secret'}
		{#if secrets.length > 0}
			<select class={inputCls} bind:value={value.secretRef}>
				<option value="" disabled selected={!value.secretRef}>Select a secret…</option>
				{#each secrets as name (name)}
					<option value={name}>🔒 {name}</option>
				{/each}
			</select>
		{:else}
			<input class="{inputCls} mono" placeholder="secret name (add one in Secrets)" bind:value={value.secretRef} />
		{/if}
	{:else}
		<PlainField {field} bind:value {references} {secrets} />
	{/if}
</div>
