<script lang="ts">
	import { api, type Environment } from '$lib/api/client';
	import Modal from './Modal.svelte';
	import { Loader2, Plus, Trash2, Layers } from 'lucide-svelte';

	let {
		open = $bindable(false),
		projectId,
		hasBackend = false,
		onSaved
	}: {
		open: boolean;
		projectId: string;
		hasBackend?: boolean;
		onSaved?: (envs: Environment[]) => void;
	} = $props();

	// KV rows are edited as arrays so keys can be renamed freely; collapsed to a
	// record on save.
	type KV = { key: string; value: string };
	type EnvRow = { name: string; backend: KV[]; vars: KV[] };

	let rows = $state<EnvRow[]>([]);
	let busy = $state(false);
	let err = $state('');
	let info = $state('');

	$effect(() => {
		if (open && projectId) load();
	});

	function toRows(env: Environment): EnvRow {
		const kv = (r?: Record<string, string>): KV[] =>
			Object.entries(r ?? {}).map(([key, value]) => ({ key, value }));
		return { name: env.name, backend: kv(env.backendConfig), vars: kv(env.vars) };
	}

	async function load() {
		err = '';
		info = '';
		try {
			rows = (await api.getEnvironments(projectId)).map(toRows);
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function addEnv() {
		rows = [...rows, { name: '', backend: [], vars: [] }];
	}
	function removeEnv(i: number) {
		rows = rows.filter((_, idx) => idx !== i);
	}
	function addKV(list: KV[]) {
		list.push({ key: '', value: '' });
		rows = rows;
	}
	function removeKV(list: KV[], i: number) {
		list.splice(i, 1);
		rows = rows;
	}

	function collapse(list: KV[]): Record<string, string> | undefined {
		const out: Record<string, string> = {};
		for (const { key, value } of list) {
			const k = key.trim();
			if (k) out[k] = value;
		}
		return Object.keys(out).length ? out : undefined;
	}

	async function save() {
		busy = true;
		err = '';
		info = '';
		try {
			const envs: Environment[] = rows
				.filter((r) => r.name.trim() !== '')
				.map((r) => ({
					name: r.name.trim(),
					backendConfig: collapse(r.backend),
					vars: collapse(r.vars)
				}));
			const saved = await api.setEnvironments(projectId, envs);
			rows = saved.map(toRows);
			info = 'Environments saved';
			onSaved?.(saved);
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const btnPrimary =
		'inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40';
	const btnGhost =
		'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]';
</script>

<Modal title="Environments" bind:open wide>
	{#if err}
		<div class="mb-2 rounded-md border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{err}</div>
	{/if}
	{#if info}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-xs text-[var(--al-muted)]">{info}</div>
	{/if}

	<p class="mb-3 text-xs text-[var(--al-muted)]">
		Deploy the same configuration to several targets. Each environment gets its own state workspace and
		can override input <span class="mono text-[var(--al-text)]">variables</span> (injected as
		<span class="mono text-[var(--al-text)]">TF_VAR_*</span>){#if hasBackend} and supply per-env
		<span class="mono text-[var(--al-text)]">backend</span> config (e.g. the state
		<span class="mono text-[var(--al-text)]">key</span>), applied at init{/if}. Select the active
		environment from the toolbar before plan / apply. With no environments the project uses one default workspace.
	</p>

	{#if rows.length === 0}
		<p class="rounded-md border border-[var(--al-border)] px-3 py-3 text-xs text-[var(--al-muted)]">
			No environments. Add one to promote this config across dev / staging / prod.
		</p>
	{/if}

	<div class="flex flex-col gap-3">
		{#each rows as row, i (i)}
			<div class="rounded-md border border-[var(--al-border)] p-3">
				<div class="mb-2 flex items-center gap-2">
					<Layers size={14} class="text-[var(--al-muted)]" />
					<input class="{inp} flex-1" bind:value={row.name} placeholder="environment name (e.g. prod)" />
					<button class={btnGhost} title="Remove environment" onclick={() => removeEnv(i)}><Trash2 size={13} /></button>
				</div>

				<div class="grid grid-cols-1 gap-3 md:grid-cols-2">
					<!-- Variable overrides -->
					<div>
						<div class="mb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">Variable overrides</div>
						{#each row.vars as kv, ki (ki)}
							<div class="mb-1 flex items-center gap-1">
								<input class="{inp} w-1/3" bind:value={kv.key} placeholder="name" />
								<input class="{inp} flex-1" bind:value={kv.value} placeholder="value" />
								<button class={btnGhost} title="Remove" onclick={() => removeKV(row.vars, ki)}><Trash2 size={12} /></button>
							</div>
						{/each}
						<button class={btnGhost} onclick={() => addKV(row.vars)}><Plus size={12} /> Variable</button>
					</div>

					<!-- Backend config -->
					<div>
						<div class="mb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">
							Backend config {#if !hasBackend}<span class="normal-case text-[var(--al-muted)]">(no backend configured)</span>{/if}
						</div>
						{#each row.backend as kv, ki (ki)}
							<div class="mb-1 flex items-center gap-1">
								<input class="{inp} w-1/3" bind:value={kv.key} placeholder="key" disabled={!hasBackend} />
								<input class="{inp} flex-1" bind:value={kv.value} placeholder="value" disabled={!hasBackend} />
								<button class={btnGhost} title="Remove" onclick={() => removeKV(row.backend, ki)}><Trash2 size={12} /></button>
							</div>
						{/each}
						<button class={btnGhost} disabled={!hasBackend} onclick={() => addKV(row.backend)}><Plus size={12} /> Backend value</button>
					</div>
				</div>
			</div>
		{/each}
	</div>

	<button class="{btnGhost} mt-3" onclick={addEnv}><Plus size={13} /> Add environment</button>

	<div class="mt-4 flex justify-end">
		<button class={btnPrimary} onclick={save} disabled={busy}>
			{#if busy}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	</div>
</Modal>
