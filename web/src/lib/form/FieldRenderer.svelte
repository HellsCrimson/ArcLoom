<script lang="ts">
	// FieldRenderer is the editor for one value at any nesting depth. It adds the
	// Literal / Reference / Expression / Secret value-mode toggle around the
	// underlying literal editor (PlainField). It is used both for top-level
	// attributes and — recursively — for the interior of lists/maps/objects, so a
	// reference or secret can be placed inside a collection.
	import type { AttrValue, Field, ValueMode } from '$lib/types';
	import PlainField from './PlainField.svelte';
	import { ensureLiteralShape, isContainer } from './defaults';
	import { joinSecretName, secretGroups } from '$lib/secretPath';
	import { Loader2, Plus } from 'lucide-svelte';

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

	// Inline secret creation: adding a secret used to mean leaving the form, going
	// to the Secrets modal and coming back to select it. Here the field creates it
	// and selects it in one step.
	let creating = $state(false);
	let newSecretName = $state('');
	let newSecretValue = $state('');
	let secretBusy = $state(false);
	let secretError = $state('');
	// The name is composed the same way the Secrets tree composes it, so
	// "global/projectA/varA" and "global_projectA_varA" both work here.
	const newSecretComposed = $derived(joinSecretName([newSecretName]));

	async function createSecret() {
		if (!onCreateSecret || !newSecretComposed || !newSecretValue) return;
		secretBusy = true;
		secretError = '';
		try {
			await onCreateSecret(newSecretComposed, newSecretValue);
			value.secretRef = newSecretComposed;
			creating = false;
			newSecretName = '';
			newSecretValue = '';
		} catch (e) {
			secretError = (e as Error).message;
		} finally {
			secretBusy = false;
		}
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
		<div class="flex items-center gap-1.5">
			{#if secrets.length > 0}
				<!-- Grouped by the first `_` segment, matching the Secrets tree view. -->
				<select class="{inputCls} flex-1" bind:value={value.secretRef}>
					<option value="" disabled selected={!value.secretRef}>Select a secret…</option>
					{#each secretGroups(secrets) as g (g.group)}
						{#if g.group === ''}
							{#each g.names as name (name)}
								<option value={name}>🔒 {name}</option>
							{/each}
						{:else}
							<optgroup label={g.group}>
								{#each g.names as name (name)}
									<option value={name}>🔒 {name}</option>
								{/each}
							</optgroup>
						{/if}
					{/each}
				</select>
			{:else}
				<input class="{inputCls} mono flex-1" placeholder={onCreateSecret ? 'secret name — or create one below' : 'secret name (add one in Secrets)'} bind:value={value.secretRef} />
			{/if}
			{#if onCreateSecret}
				<button
					type="button"
					class="inline-flex flex-none items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-[11px] text-[var(--al-muted)] hover:text-[var(--al-accent)]"
					title="Create a secret and use it here"
					onclick={() => { creating = !creating; secretError = ''; }}
				><Plus size={12} /> New</button>
			{/if}
		</div>

		{#if creating && onCreateSecret}
			<div class="mt-1.5 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] p-2">
				<div class="flex items-center gap-1.5">
					<input
						class="{inputCls} mono flex-1"
						placeholder="name — global/projectA/varA"
						bind:value={newSecretName}
					/>
					<input
						class="{inputCls} flex-1"
						type="password"
						placeholder="value"
						bind:value={newSecretValue}
						onkeydown={(e) => { if (e.key === 'Enter') createSecret(); }}
					/>
					<button
						type="button"
						class="inline-flex flex-none items-center gap-1 rounded-md bg-[var(--al-accent)] px-2.5 py-1 text-[11px] font-medium text-white hover:brightness-110 disabled:opacity-40"
						onclick={createSecret}
						disabled={secretBusy || !newSecretComposed || !newSecretValue}
					>
						{#if secretBusy}<Loader2 size={12} class="animate-spin" />{/if} Save & use
					</button>
				</div>
				{#if secretError}
					<p class="mt-1 text-[11px] text-[var(--al-danger)]">{secretError}</p>
				{:else}
					<p class="mt-1 text-[11px] text-[var(--al-muted)]">
						{#if newSecretComposed}
							Stored as <span class="mono text-[var(--al-text)]">{newSecretComposed}</span>, encrypted at rest and injected as
							<span class="mono">TF_VAR_{newSecretComposed}</span>.
						{:else}
							Encrypted at rest; <span class="mono">/</span> or <span class="mono">_</span> segments become categories in the Secrets tree.
						{/if}
					</p>
				{/if}
			</div>
		{/if}
	{:else}
		<PlainField {field} bind:value {references} {secrets} {onCreateSecret} />
	{/if}
</div>
{/if}
