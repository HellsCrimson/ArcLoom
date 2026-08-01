<!--
  SecretPicker — choose an existing secret, or create one and select it in one
  step. Extracted from FieldRenderer so the Terraform-block modal's backend
  settings offer the same picker: a backend credential is the most obvious
  secret in a project, and it would otherwise be the one place that made you
  paste a token as a literal.

  It edits a bare secret NAME (not an AttrValue), which is what lets both
  callers use it — the resource form binds it to `value.secretRef`, the backend
  modal to its row's ref. `delivery` names how the value reaches the engine,
  because that differs by caller: a schema field becomes TF_VAR_<name>, while a
  backend setting cannot (Terraform forbids variables in a backend block) and
  travels through -backend-config instead.
-->
<script lang="ts">
	import { joinSecretName, secretGroups } from '$lib/secretPath';
	import { Loader2, Plus } from 'lucide-svelte';

	let {
		// NO fallback value: a bound prop with one rejects `undefined`
		// (svelte.dev/e/props_invalid_value), and `value.secretRef` is exactly
		// that until a secret has been chosen — so seeding this with '' made the
		// picker throw the moment a field was switched to Secret mode.
		secretRef = $bindable(),
		secrets = [],
		onCreateSecret,
		delivery = 'TF_VAR'
	}: {
		secretRef?: string;
		secrets?: string[];
		/** Store a new secret and refresh the list; absent = creating is
		 *  unavailable (no writable secret store), so only existing ones show. */
		onCreateSecret?: (name: string, value: string) => Promise<void>;
		/** How the resolved value reaches the engine, for the create hint. */
		delivery?: 'TF_VAR' | 'backend-config';
	} = $props();

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
			secretRef = newSecretComposed;
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

<div class="flex items-center gap-1.5">
	{#if secrets.length > 0}
		<!-- Grouped by the first `_` segment, matching the Secrets tree view. -->
		<select class="{inputCls} flex-1" bind:value={secretRef}>
			<option value="" disabled selected={!secretRef}>Select a secret…</option>
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
		<input class="{inputCls} mono flex-1" placeholder={onCreateSecret ? 'secret name — or create one below' : 'secret name (add one in Secrets)'} bind:value={secretRef} />
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
					Stored as <span class="mono text-[var(--al-text)]">{newSecretComposed}</span>, encrypted at rest and
					{#if delivery === 'TF_VAR'}
						injected as <span class="mono">TF_VAR_{newSecretComposed}</span>.
					{:else}
						passed to the engine as <span class="mono">-backend-config</span>.
					{/if}
				{:else}
					Encrypted at rest; <span class="mono">/</span> or <span class="mono">_</span> segments become categories in the Secrets tree.
				{/if}
			</p>
		{/if}
	</div>
{/if}
