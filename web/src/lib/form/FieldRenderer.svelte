<script lang="ts">
	// FieldRenderer is the editor for one value at any nesting depth. It adds the
	// Literal / Reference / Expression / Secret value-mode toggle around the
	// underlying literal editor (PlainField). It is used both for top-level
	// attributes and — recursively — for the interior of lists/maps/objects, so a
	// reference or secret can be placed inside a collection.
	import type { AttrValue, Field, ValueMode } from '$lib/types';
	import PlainField from './PlainField.svelte';
	import { ensureLiteralShape, isContainer } from './defaults';
	import SecretPicker from './SecretPicker.svelte';
	import { referenceGroups } from '$lib/model/scope';

	let {
		field,
		value = $bindable(),
		references = [],
		secrets = [],
		onCreateSecret,
		// compact renders a primitive value (e.g. a map(string) entry) as a plain
		// inline input, tucking the Literal/Reference/Expression/Secret toggle
		// behind an "ƒx" button so a key/value map reads as clean pairs instead of
		// a stack of mode toggles. The advanced modes are one click away and stay
		// expanded once a non-literal mode is chosen.
		compact = false
	}: {
		field: Field;
		value: AttrValue;
		references?: string[];
		secrets?: string[];
		/** Store a new secret and refresh the list; absent = creating is unavailable
		 *  (no local secret store), so the field only offers existing ones. */
		onCreateSecret?: (name: string, value: string) => Promise<void>;
		compact?: boolean;
	} = $props();

	const modes: { id: ValueMode; label: string }[] = [
		{ id: 'literal', label: 'Literal' },
		{ id: 'reference', label: 'Reference' },
		{ id: 'expression', label: 'Expression' },
		{ id: 'secret', label: 'Secret' }
	];

	// A primitive value in a compact collection collapses its mode toggle until
	// the user opts into an advanced mode. A non-literal mode always expands it —
	// tracked reactively, not just on mount, because the mode can be set from
	// outside (the dynamic editor's kind selector picks "secret" directly) and a
	// collapsed row would then hide the secret picker it needs to show.
	const collapsible = $derived(compact && !isContainer(field));
	let expanded = $state(false);
	const advanced = $derived(expanded || value.mode !== 'literal');

	function setMode(m: ValueMode) {
		value.mode = m;
		if (m === 'literal') ensureLiteralShape(field, value);
	}

	const inputCls =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

{#if collapsible && !advanced}
	<!-- Clean key→value case: plain input + an unobtrusive advanced toggle. -->
	<div class="flex items-center gap-1.5">
		<div class="flex-1"><PlainField {field} bind:value {references} {secrets} {onCreateSecret} /></div>
		<button
			type="button"
			class="flex-none rounded border border-[var(--al-border)] px-1.5 py-1 text-[11px] italic text-[var(--al-muted)] hover:text-[var(--al-text)]"
			title="Use a reference, expression or secret instead of a literal"
			onclick={() => (expanded = true)}
		>
			ƒx
		</button>
	</div>
{:else}
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
			<!-- Grouped by what the address is (variables / locals / modules /
			     data / resources), like the secret picker below. -->
			<select class={inputCls} bind:value={value.ref}>
				<option value="" disabled selected={!value.ref}>Select a reference…</option>
				{#each referenceGroups(references) as g (g.group)}
					<optgroup label={g.group}>
						{#each g.refs as ref (ref)}
							<option value={ref}>{ref}</option>
						{/each}
					</optgroup>
				{/each}
			</select>
		{:else}
			<input class="{inputCls} mono" placeholder="type.name.attr" bind:value={value.ref} />
		{/if}
	{:else if value.mode === 'expression'}
		<input class="{inputCls} mono" placeholder="raw HCL expression" bind:value={value.expr} />
	{:else if value.mode === 'secret'}
		<SecretPicker bind:secretRef={value.secretRef} {secrets} {onCreateSecret} />
	{:else}
		<PlainField {field} bind:value {references} {secrets} {onCreateSecret} />
	{/if}
</div>
{/if}
