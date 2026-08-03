<!--
  Environment variables for this project's engine runs.

  They exist because a provider's own environment variables are frequently the
  documented way to give it credentials (OPENNEBULA_PASSWORD, VAULT_TOKEN,
  AWS_PROFILE), and the only injection ArcLoom had was TF_VAR_<name> — which
  feeds `variable` blocks and nothing else. Setting them on the ArcLoom process
  worked but was instance-wide, so two projects could not each have their own.

  A value is a literal or a secret. The literal is stored in the clear
  server-side (like a per-env variable override), which is exactly why the
  secret mode exists and why the panel says so next to the input.
-->
<script lang="ts">
	import { api, type EnvVarRow, type Environment } from '$lib/api/client';
	import Modal from './Modal.svelte';
	import { Loader2, Plus, Trash2, KeyRound, Type } from 'lucide-svelte';


	let {
		open = $bindable(false),
		projectId,
		secrets = [],
		canEdit = true
	}: {
		open: boolean;
		projectId: string;
		secrets?: string[];
		canEdit?: boolean;
	} = $props();

	// Rows carry a `secret` flag rather than deriving it from secretRef being
	// set: a row switched to Secret mode has no ref chosen yet, and deriving
	// would snap it back to Literal on every keystroke.
	type Row = { name: string; value: string; secretRef: string; secret: boolean };

	let rows = $state<Row[]>([]);
	let blocked = $state<string[]>([]);
	// Which scope is being edited: '' = the project's own rows, else the name of
	// an environment whose overrides layer on top.
	let scope = $state('');
	let envs = $state<Environment[]>([]);
	let overrides = $state<Record<string, EnvVarRow[]>>({});
	let projectRows = $state<EnvVarRow[]>([]);

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const btnPrimary =
		'inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40';
	const btnGhost =
		'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40';
	let busy = $state(false);
	let err = $state('');
	let info = $state('');

	$effect(() => {
		if (open && projectId) load();
	});

	function toRows(list: EnvVarRow[]): Row[] {
		return list.map((v) => ({
			name: v.name,
			value: v.value ?? '',
			secretRef: v.secretRef ?? '',
			secret: !!v.secretRef
		}));
	}

	async function load() {
		err = '';
		info = '';
		try {
			const [res, envList] = await Promise.all([
				api.getEnvVars(projectId),
				api.getEnvironments(projectId).catch(() => [] as Environment[])
			]);
			blocked = res.blocked ?? [];
			projectRows = res.vars ?? [];
			overrides = res.envVars ?? {};
			envs = envList ?? [];
			showScope(scope);
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function showScope(name: string) {
		scope = name;
		rows = toRows(name === '' ? projectRows : (overrides[name] ?? []));
		info = '';
	}

	function add() {
		rows = [...rows, { name: '', value: '', secretRef: '', secret: false }];
	}

	function remove(i: number) {
		rows = rows.filter((_, j) => j !== i);
	}

	async function save() {
		busy = true;
		err = '';
		info = '';
		try {
			const payload: EnvVarRow[] = rows
				.filter((r) => r.name.trim() !== '')
				.map((r) =>
					r.secret
						? { name: r.name.trim(), secretRef: r.secretRef }
						: { name: r.name.trim(), value: r.value }
				);
			const res = await api.setEnvVars(projectId, payload, scope);
			if (scope === '') projectRows = res.vars ?? [];
			else overrides = { ...overrides, [scope]: res.vars ?? [] };
			rows = toRows(res.vars ?? []);
			info = scope === '' ? 'Environment variables saved' : `Overrides for ${scope} saved`;
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	// What the project row for this name holds, so an override screen shows what
	// it is overriding instead of looking like the only value.
	function baseFor(name: string): string {
		const base = projectRows.find((v) => v.name === name);
		if (!base) return '';
		return base.secretRef ? `secret ${base.secretRef}` : base.value || '(empty)';
	}
</script>

<Modal title="Environment variables" bind:open wide>
	<div class="space-y-4 text-sm">
		<p class="text-[var(--al-muted)]">
			Set on the engine process for this project's plan, apply and state operations — the
			way a provider reads its own credentials. Input variables come from Variables and
			Secrets instead; <code>TF_VAR_*</code> cannot be set here.
		</p>

		{#if envs.length > 0}
			<div class="flex flex-wrap items-center gap-2">
				<span class="text-[var(--al-muted)]">Scope</span>
				<button
					class={scope === '' ? btnPrimary : btnGhost}
					onclick={() => showScope('')}
					type="button">All environments</button
				>
				{#each envs as e (e.name)}
					<button
						class={scope === e.name ? btnPrimary : btnGhost}
						onclick={() => showScope(e.name)}
						type="button">{e.name}</button
					>
				{/each}
			</div>
			{#if scope !== ''}
				<p class="text-xs text-[var(--al-muted)]">
					These override the shared rows by name when deploying to <strong>{scope}</strong>;
					anything not listed here falls back to the shared value.
				</p>
			{/if}
		{/if}

		{#if rows.length === 0}
			<p class="text-[var(--al-muted)]">
				{scope === '' ? 'No environment variables.' : `No overrides for ${scope}.`}
			</p>
		{/if}

		{#each rows as row, i (i)}
			<div class="flex flex-wrap items-start gap-2">
				<input
					class="{inp} w-56 font-mono"
					placeholder="OPENNEBULA_PASSWORD"
					bind:value={row.name}
					disabled={!canEdit}
				/>
				<div class="flex min-w-[16rem] flex-1 flex-col gap-1">
					{#if row.secret}
						<select class={inp} bind:value={row.secretRef} disabled={!canEdit}>
							<option value="">Choose a secret…</option>
							{#each secrets as name (name)}
								<option value={name}>{name}</option>
							{/each}
						</select>
					{:else}
						<input
							class={inp}
							placeholder="value"
							bind:value={row.value}
							disabled={!canEdit}
						/>
						<span class="text-xs text-[var(--al-muted)]">
							Stored in the clear — use a secret for credentials.
						</span>
					{/if}
					{#if scope !== '' && baseFor(row.name)}
						<span class="text-xs text-[var(--al-muted)]">
							Shared value: {baseFor(row.name)}
						</span>
					{/if}
				</div>
				<button
					class={btnGhost}
					type="button"
					title={row.secret ? 'Use a literal value' : 'Use a stored secret'}
					onclick={() => (row.secret = !row.secret)}
					disabled={!canEdit}
				>
					{#if row.secret}<KeyRound class="h-4 w-4" />{:else}<Type class="h-4 w-4" />{/if}
				</button>
				<button
					class={btnGhost}
					type="button"
					title="Remove"
					onclick={() => remove(i)}
					disabled={!canEdit}
				>
					<Trash2 class="h-4 w-4" />
				</button>
			</div>
		{/each}

		{#if canEdit}
			<button class={btnGhost} type="button" onclick={add}>
				<Plus class="h-4 w-4" /> Add variable
			</button>
		{/if}

		{#if blocked.length > 0}
			<details class="text-xs text-[var(--al-muted)]">
				<summary class="cursor-pointer">Names this server refuses</summary>
				<p class="mt-1">
					These would change what the server runs rather than how a provider connects, so
					they are rejected: <code>{blocked.join(', ')}</code>, plus anything starting with
					<code>TF_VAR_</code>, <code>TF_CLI_ARGS</code> or <code>ARCLOOM_</code>.
				</p>
			</details>
		{/if}

		{#if err}<p class="text-[var(--al-danger)]">{err}</p>{/if}
		{#if info}<p class="text-[var(--al-ok)]">{info}</p>{/if}
	</div>

	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Close</button>
		{#if canEdit}
			<button class={btnPrimary} onclick={save} disabled={busy}>
				{#if busy}<Loader2 class="h-4 w-4 animate-spin" />{/if} Save
			</button>
		{/if}
	{/snippet}
</Modal>
