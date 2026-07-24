<script lang="ts">
	import { api, type PolicyDoc } from '$lib/api/client';
	import Modal from './Modal.svelte';
	import { Loader2, Plus, Trash2, ShieldCheck } from 'lucide-svelte';

	let {
		open = $bindable(false),
		projectId
	}: { open: boolean; projectId: string } = $props();

	let enabled = $state(false);
	let enforce = $state(false);
	let failOnWarn = $state(false);
	let docs = $state<PolicyDoc[]>([]);
	let busy = $state(false);
	let err = $state('');
	let info = $state('');

	// A starter policy: the plan JSON is the standard Terraform plan
	// representation, so rules read input.resource_changes.
	const SAMPLE = `package main

deny contains msg if {
	rc := input.resource_changes[_]
	rc.change.actions[_] == "delete"
	msg := sprintf("%s would be destroyed", [rc.address])
}`;

	$effect(() => {
		if (open && projectId) load();
	});

	async function load() {
		err = '';
		info = '';
		try {
			const res = await api.getPolicy(projectId);
			enabled = res.config.enabled;
			enforce = res.config.enforce;
			failOnWarn = res.config.failOnWarn;
			docs = (res.config.docs ?? []).map((d) => ({ ...d }));
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function addDoc() {
		docs = [...docs, { name: `policy-${docs.length + 1}.rego`, rego: SAMPLE }];
	}

	function removeDoc(i: number) {
		docs = docs.filter((_, idx) => idx !== i);
	}

	async function save() {
		busy = true;
		err = '';
		info = '';
		try {
			const res = await api.setPolicy(projectId, {
				enabled,
				enforce,
				failOnWarn,
				docs: docs.filter((d) => d.rego.trim() !== '').map((d) => ({ name: d.name.trim(), rego: d.rego }))
			});
			enabled = res.config.enabled;
			enforce = res.config.enforce;
			failOnWarn = res.config.failOnWarn;
			docs = (res.config.docs ?? []).map((d) => ({ ...d }));
			info = 'Policies saved';
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

<Modal title="Policy as code" bind:open wide>
	{#if err}
		<div class="mb-2 rounded-md border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{err}</div>
	{/if}
	{#if info}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-xs text-[var(--al-muted)]">{info}</div>
	{/if}

	<p class="mb-3 text-xs text-[var(--al-muted)]">
		Rego policies are evaluated with <span class="mono text-[var(--al-text)]">conftest</span> against the plan JSON
		(rules read <span class="mono text-[var(--al-text)]">input.resource_changes</span>). A plan shows the result;
		with enforcement on, an apply first plans, checks, and only applies that exact plan when no
		<span class="mono text-[var(--al-text)]">deny</span> rule fires. Requires conftest on the server PATH — with
		enforcement on, a missing binary blocks the apply rather than waving it through.
	</p>

	<div class="mb-3 space-y-1.5">
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={enabled} onchange={() => (enabled = !enabled)} class="accent-[var(--al-accent)]" />
			<ShieldCheck size={14} /> Check policies on every plan
		</label>
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={enforce} onchange={() => (enforce = !enforce)} class="accent-[var(--al-accent)]" disabled={!enabled} />
			Block an apply when a policy denies
		</label>
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={failOnWarn} onchange={() => (failOnWarn = !failOnWarn)} class="accent-[var(--al-accent)]" disabled={!enabled || !enforce} />
			Treat <span class="mono">warn</span> rules as blocking too
		</label>
	</div>

	{#if docs.length === 0}
		<p class="rounded-md border border-[var(--al-border)] px-3 py-3 text-xs text-[var(--al-muted)]">
			No policies yet. Add one to start checking plans.
		</p>
	{/if}
	<div class="space-y-3">
		{#each docs as doc, i (i)}
			<div class="rounded-md border border-[var(--al-border)] p-2">
				<div class="mb-1.5 flex items-center gap-2">
					<input class="{inp} flex-1 mono" bind:value={doc.name} placeholder="policy.rego" />
					<button class={btnGhost} title="Remove policy" onclick={() => removeDoc(i)}><Trash2 size={13} /></button>
				</div>
				<textarea
					class="{inp} mono w-full"
					rows="10"
					spellcheck="false"
					bind:value={doc.rego}
					placeholder={SAMPLE}
				></textarea>
			</div>
		{/each}
	</div>
	<button class="{btnGhost} mt-2" onclick={addDoc}><Plus size={13} /> Add policy</button>

	<div class="mt-4 flex justify-end">
		<button class={btnPrimary} onclick={save} disabled={busy}>
			{#if busy}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	</div>
</Modal>
