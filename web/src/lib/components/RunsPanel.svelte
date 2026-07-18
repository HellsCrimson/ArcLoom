<script lang="ts">
	import Modal from './Modal.svelte';
	import { api, type RunRecord } from '$lib/api/client';
	import { ACTION_COLOR } from '$lib/editor/graph';
	import { ArrowLeft, CheckCircle2, XCircle, Loader2 } from 'lucide-svelte';

	let { open = $bindable(false), projectId }: { open: boolean; projectId: string } = $props();

	let list = $state<RunRecord[]>([]);
	let detail = $state<RunRecord | null>(null);
	let loading = $state(false);
	let error = $state('');

	const OP_COLOR: Record<string, string> = {
		plan: '#4c8dff',
		apply: ACTION_COLOR.create,
		destroy: ACTION_COLOR.delete
	};

	$effect(() => {
		if (open) {
			detail = null;
			load();
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			list = await api.listRuns(projectId);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			loading = false;
		}
	}

	async function openRun(id: string) {
		loading = true;
		error = '';
		try {
			detail = await api.getRun(projectId, id);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			loading = false;
		}
	}

	function when(r: RunRecord): string {
		return new Date(r.startedAt).toLocaleString();
	}
	function duration(r: RunRecord): string {
		const s = (new Date(r.finishedAt).getTime() - new Date(r.startedAt).getTime()) / 1000;
		return s < 60 ? `${s.toFixed(s < 10 ? 1 : 0)}s` : `${Math.round(s / 60)}m ${Math.round(s % 60)}s`;
	}
</script>

<Modal title="Run history" bind:open wide>
	{#if error}
		<div class="mb-2 rounded border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{error}</div>
	{/if}

	{#if detail}
		<button class="mb-3 inline-flex items-center gap-1.5 text-xs font-medium text-[var(--al-muted)] hover:text-[var(--al-text)]" onclick={() => (detail = null)}>
			<ArrowLeft size={13} /> All runs
		</button>
		<div class="mb-2 flex items-center gap-2 text-sm">
			<span class="rounded px-1.5 py-0.5 font-mono text-[11px] font-bold uppercase" style="background:{OP_COLOR[detail.op]}22;color:{OP_COLOR[detail.op]};">{detail.op}</span>
			{#if detail.status === 'success'}
				<CheckCircle2 size={15} style="color:{ACTION_COLOR.create};" />
			{:else}
				<XCircle size={15} style="color:{ACTION_COLOR.delete};" />
			{/if}
			<span class="text-[var(--al-text)]">{when(detail)}</span>
			<span class="text-xs text-[var(--al-muted)]">· {duration(detail)}{#if detail.user}&nbsp;· {detail.user}{/if}</span>
		</div>
		{#if detail.error}
			<div class="mb-2 rounded border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 font-mono text-xs text-[var(--al-danger)]">{detail.error}</div>
		{/if}
		{#if detail.plan?.changes?.length}
			<div class="mb-2 rounded-md border border-[var(--al-border)] py-1">
				{#each detail.plan.changes as c (c.address)}
					<div class="flex items-center gap-2 px-3 py-0.5 font-mono text-[11.5px]">
						<span class="w-3 flex-none text-center font-bold" style="color:{ACTION_COLOR[c.action]}">{c.action === 'create' ? '+' : c.action === 'update' ? '~' : c.action === 'replace' ? '±' : c.action === 'delete' ? '−' : '→'}</span>
						<span class="truncate text-[var(--al-text)]">{c.address}</span>
						<span class="ml-auto flex-none text-[10px]" style="color:{ACTION_COLOR[c.action]}">{c.action}</span>
					</div>
				{/each}
			</div>
		{/if}
		{#if detail.outputs?.length}
			<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Outputs</div>
			<div class="mb-2 rounded-md border border-[var(--al-border)] py-1">
				{#each detail.outputs as o (o.name)}
					<div class="flex items-center gap-3 px-3 py-0.5 font-mono text-[11.5px]">
						<span class="flex-none text-[var(--al-text)]">{o.name}</span>
						<span class="ml-auto truncate text-[var(--al-muted)]" title={o.sensitive ? '' : o.value}>{o.sensitive ? '(sensitive)' : o.value}</span>
					</div>
				{/each}
			</div>
		{/if}
		<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Log</div>
		<pre class="max-h-72 overflow-auto whitespace-pre-wrap rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] px-3 py-2 font-mono" style="font-size:11px;line-height:1.6;color:#8b95a5;">{detail.log || '(empty)'}</pre>
	{:else if loading}
		<div class="flex items-center gap-2 py-6 text-sm text-[var(--al-muted)]"><Loader2 size={15} class="animate-spin" /> loading…</div>
	{:else if list.length === 0}
		<p class="py-6 text-center text-sm text-[var(--al-muted)]">No runs yet — plan or apply this project to start its history.</p>
	{:else}
		<ul class="space-y-1">
			{#each list as r (r.id)}
				<li>
					<button
						class="flex w-full items-center gap-2 rounded-md border border-[var(--al-border)] px-3 py-2 text-left hover:bg-[var(--al-panel-2)]"
						onclick={() => openRun(r.id)}
					>
						<span class="w-16 flex-none rounded px-1.5 py-0.5 text-center font-mono text-[11px] font-bold uppercase" style="background:{OP_COLOR[r.op]}22;color:{OP_COLOR[r.op]};">{r.op}</span>
						{#if r.status === 'success'}
							<CheckCircle2 size={15} class="flex-none" style="color:{ACTION_COLOR.create};" />
						{:else}
							<XCircle size={15} class="flex-none" style="color:{ACTION_COLOR.delete};" />
						{/if}
						<span class="text-sm text-[var(--al-text)]">{when(r)}</span>
						<span class="text-xs text-[var(--al-muted)]">· {duration(r)}{#if r.user}&nbsp;· {r.user}{/if}</span>
						<span class="ml-auto flex items-center gap-2 font-mono text-[11px] font-semibold">
							{#if r.op === 'plan' && r.plan}
								{#if r.plan.hasChanges}
									<span style="color:{ACTION_COLOR.create}">+{r.plan.add}</span>
									<span style="color:{ACTION_COLOR.update}">~{r.plan.change}</span>
									<span style="color:{ACTION_COLOR.replace}">±{r.plan.replace}</span>
									<span style="color:{ACTION_COLOR.delete}">−{r.plan.destroy}</span>
								{:else}
									<span class="text-[var(--al-muted)]">no changes</span>
								{/if}
							{:else if r.op === 'apply' && r.outputs?.length}
								<span class="text-[var(--al-muted)]">{r.outputs.length} output{r.outputs.length === 1 ? '' : 's'}</span>
							{/if}
						</span>
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</Modal>
