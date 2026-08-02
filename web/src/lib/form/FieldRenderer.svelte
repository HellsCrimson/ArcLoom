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
	import { parseHclCollection } from '$lib/model/hcltext';
	import { ChevronDown, ChevronRight } from 'lucide-svelte';

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
	const container = $derived(isContainer(field));
	const collapsible = $derived(compact && !container);
	let expanded = $state(false);
	const advanced = $derived(expanded || value.mode !== 'literal');

	// A container in the ordinary literal case shows a header instead of the
	// four-button mode toggle: repeating that toggle at every level was the bulk
	// of the noise in a nested object (three levels deep, the toggles outnumbered
	// the values). The advanced modes stay one ƒx click away, and the header
	// doubles as the fold control — without it a large map is a wall of rows with
	// no way to see the shape.
	const containerHeader = $derived(container && !advanced);
	let folded = $state(false);
	const childCount = $derived(value.entries?.length ?? value.items?.length ?? 0);
	const listLike = $derived(field.widget === 'list' || field.widget === 'block');
	// An object's fields are fixed by its schema, so counting them says nothing;
	// a map's or list's size is the useful summary.
	const summary = $derived(
		field.widget === 'object'
			? 'fields'
			: `${childCount} ${listLike ? (childCount === 1 ? 'item' : 'items') : childCount === 1 ? 'key' : 'keys'}`
	);

	// A container attribute can hold an inline collection written as RAW HCL —
	// `tags = { Name = "x", Env = var.env }`, which is what hclimport stores
	// whenever the members are not all literals. That is a structure, but it
	// arrives in Expression mode and therefore renders as one text box.
	//
	// Unlike the Variables modal (where structured/text is a per-row VIEW over one
	// stored value), the mode here IS the stored value: switching it on open would
	// rewrite a hand-written expression — re-emitted with sorted keys — on the
	// next save of a form the user only opened to read. So it is offered, not
	// applied: one click, and the rows arrive filled in.
	const asRows = $derived(
		container && value.mode === 'expression' ? parseHclCollection(value.expr ?? '') : null
	);

	function setMode(m: ValueMode) {
		if (m === 'literal' && value.mode === 'expression') seedFromExpr();
		value.mode = m;
		if (m === 'literal') ensureLiteralShape(field, value);
	}

	// Switching a parseable expression to Literal used to hand the user an EMPTY
	// map: ensureLiteralShape seeds from `literal`, which an expression value does
	// not have, so the whole collection had to be retyped. `value.expr` is left
	// intact, so switching back restores the original text verbatim.
	function seedFromExpr() {
		if (!container || value.entries !== undefined || value.items !== undefined) return;
		const parsed = parseHclCollection(value.expr ?? '');
		if (!parsed) return;
		value.entries = parsed.entries;
		value.items = parsed.items;
		value.literal = undefined;
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
{:else if containerHeader}
	<!-- Container, literal: fold control + summary + ƒx, then the body indented by
	     a thin rail. The rail costs ~10px a level, where the old bordered card cost
	     ~28px — which is what made a third level run out of width and overflow. -->
	<div>
		<div class="flex items-center gap-1.5">
			<button
				type="button"
				class="inline-flex items-center gap-1 rounded px-1 py-0.5 text-[11px] text-[var(--al-muted)] hover:text-[var(--al-text)]"
				onclick={() => (folded = !folded)}
				aria-expanded={!folded}
			>
				{#if folded}<ChevronRight size={12} />{:else}<ChevronDown size={12} />{/if}
				<span class="mono">{summary}</span>
			</button>
			<button
				type="button"
				class="rounded border border-[var(--al-border)] px-1.5 py-0.5 text-[11px] italic text-[var(--al-muted)] hover:text-[var(--al-text)]"
				title="Set this whole value from a reference, expression or secret instead"
				onclick={() => (expanded = true)}
			>
				ƒx
			</button>
		</div>
		{#if !folded}
			<div class="mt-1 border-l border-[var(--al-border)] pl-2">
				<PlainField {field} bind:value {references} {secrets} {onCreateSecret} />
			</div>
		{/if}
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
		<div class="flex items-center gap-1.5">
			<input class="{inputCls} mono" placeholder="raw HCL expression" bind:value={value.expr} />
			{#if asRows}
				<button
					type="button"
					class="flex-none rounded border border-[var(--al-border)] px-2 py-1 text-[11px] text-[var(--al-muted)] hover:text-[var(--al-text)]"
					title="This is a collection — edit it as rows instead (the text is kept if you switch back)"
					onclick={() => setMode('literal')}
				>Edit as rows</button>
			{/if}
		</div>
	{:else if value.mode === 'secret'}
		<SecretPicker bind:secretRef={value.secretRef} {secrets} {onCreateSecret} />
	{:else}
		<PlainField {field} bind:value {references} {secrets} {onCreateSecret} />
	{/if}
</div>
{/if}
