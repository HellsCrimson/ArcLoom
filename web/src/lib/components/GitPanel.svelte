<script lang="ts">
	import { api } from '$lib/api/client';
	import type { GitStatus, GitCommit, GitBranch as GitBranchType, Project } from '$lib/types';
	import Modal from './Modal.svelte';
	import {
		GitBranch, GitCommitVertical, History, Loader2, Upload, Download, RotateCcw, Cloud,
		GitMerge, Trash2, Plus, ArrowUp, ArrowDown
	} from 'lucide-svelte';

	let {
		open = $bindable(false),
		projectId,
		onRestored
	}: { open: boolean; projectId: string; onRestored: (p: Project) => void } = $props();

	let status = $state<GitStatus | null>(null);
	let commits = $state<GitCommit[]>([]);
	let branches = $state<GitBranchType[]>([]);
	let newBranch = $state('');
	let diff = $state('');
	let selectedCommit = $state<string | null>(null); // null = working diff
	let message = $state('');
	let busy = $state('');
	let err = $state('');
	let info = $state('');

	// Remote
	let remoteUrl = $state('');
	let username = $state('');
	let token = $state('');
	let showRemote = $state(false);

	$effect(() => {
		if (open && projectId) refresh();
	});

	async function refresh() {
		err = '';
		try {
			status = await api.git.status(projectId);
			commits = await api.git.log(projectId);
			branches = await api.git.branches(projectId);
			remoteUrl = status.remoteUrl ?? remoteUrl;
			await loadDiff(selectedCommit);
		} catch (e) {
			err = (e as Error).message;
		}
	}

	async function createBranch() {
		if (!newBranch.trim()) return;
		busy = 'branch';
		err = '';
		info = '';
		try {
			await api.git.createBranch(projectId, newBranch.trim());
			info = `Switched to new branch ${newBranch.trim()}`;
			newBranch = '';
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function switchBranch(name: string) {
		busy = 'switch';
		err = '';
		info = '';
		try {
			const p = await api.git.checkout(projectId, name);
			onRestored(p);
			info = `On branch ${name}`;
			selectedCommit = null;
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function mergeBranch(name: string) {
		if (!confirm(`Fast-forward merge ${name} into ${status?.branch}?`)) return;
		busy = 'merge';
		err = '';
		info = '';
		try {
			const p = await api.git.merge(projectId, name);
			onRestored(p);
			info = `Merged ${name} into ${status?.branch}`;
			selectedCommit = null;
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function deleteBranch(name: string) {
		if (!confirm(`Delete branch ${name}?`)) return;
		busy = 'delbranch';
		err = '';
		try {
			await api.git.deleteBranch(projectId, name);
			info = `Deleted branch ${name}`;
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function loadDiff(commit: string | null) {
		selectedCommit = commit;
		try {
			diff = await api.git.diff(projectId, commit ?? undefined);
		} catch (e) {
			diff = '';
			err = (e as Error).message;
		}
	}

	async function commit() {
		if (!message.trim()) return;
		busy = 'commit';
		err = '';
		info = '';
		try {
			const { hash } = await api.git.commit(projectId, message.trim());
			info = `Committed ${hash.slice(0, 7)}`;
			message = '';
			selectedCommit = null;
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function restore(hash: string) {
		if (!confirm(`Restore working files to ${hash.slice(0, 7)}? Review and commit to keep it.`)) return;
		busy = 'restore';
		err = '';
		try {
			const p = await api.git.restore(projectId, hash);
			onRestored(p);
			info = `Restored files from ${hash.slice(0, 7)} — review the diff and commit`;
			selectedCommit = null;
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function setRemote() {
		if (!remoteUrl.trim()) return;
		busy = 'remote';
		err = '';
		try {
			await api.git.setRemote(projectId, remoteUrl.trim());
			info = 'Remote saved';
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function sync(kind: 'push' | 'pull') {
		busy = kind;
		err = '';
		info = '';
		try {
			if (kind === 'push') await api.git.push(projectId, username, token);
			else await api.git.pull(projectId, username, token);
			info = kind === 'push' ? 'Pushed to remote' : 'Pulled from remote';
			if (kind === 'pull') {
				const p = await api.getProject(projectId);
				onRestored(p);
			}
			await refresh();
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	function lineColor(l: string): string {
		if (l.startsWith('+++') || l.startsWith('---')) return '#8b949e';
		if (l.startsWith('@@')) return '#79c0ff';
		if (l.startsWith('+')) return '#3fb950';
		if (l.startsWith('-')) return '#f85149';
		return '#8b949e';
	}
	const diffLines = $derived(diff ? diff.split('\n') : []);

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const btnGhost =
		'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs font-medium text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40';
	const btnPrimary =
		'inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40';
</script>

<Modal title="Git" bind:open wide>
	{#if err}
		<div class="mb-2 rounded-md border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{err}</div>
	{/if}
	{#if info}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-xs text-[var(--al-muted)]">{info}</div>
	{/if}

	<!-- status + commit -->
	<div class="mb-3 flex items-center gap-2 text-xs text-[var(--al-muted)]">
		<GitBranch size={14} />
		{#if status?.initialized}
			<span class="mono text-[var(--al-text)]">{status.branch || '(no commits)'}</span>
			{#if status.ahead > 0}
				<span class="inline-flex items-center gap-0.5 text-[var(--al-accent)]" title="commits ahead of origin"><ArrowUp size={11} />{status.ahead}</span>
			{/if}
			{#if status.behind > 0}
				<span class="inline-flex items-center gap-0.5 text-[var(--al-danger)]" title="commits behind origin"><ArrowDown size={11} />{status.behind}</span>
			{/if}
			<span>· {status.clean ? 'clean' : `${status.files?.length ?? 0} change(s)`}</span>
			{#if status.hasRemote}<span class="mono">· {status.remoteUrl}</span>{/if}
		{:else}
			<span>not a repository yet — your first commit initializes it</span>
		{/if}
	</div>

	<div class="mb-4 flex items-end gap-2">
		<input class="{inp} flex-1" placeholder="commit message" bind:value={message} onkeydown={(e) => { if (e.key === 'Enter') commit(); }} />
		<button class={btnPrimary} onclick={commit} disabled={busy === 'commit' || !message.trim() || status?.clean}>
			{#if busy === 'commit'}<Loader2 size={14} class="animate-spin" />{:else}<GitCommitVertical size={15} />{/if} Commit
		</button>
	</div>

	<!-- branches -->
	{#if status?.hasCommits}
		<div class="mb-4 rounded-md border border-[var(--al-border)] p-2.5">
			<div class="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				<GitBranch size={13} /> Branches
			</div>
			<div class="flex flex-wrap items-center gap-1.5">
				{#each branches as b (b.name)}
					<div class="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs {b.current ? 'border-[var(--al-accent)] bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'border-[var(--al-border)] text-[var(--al-muted)]'}">
						<button
							class="mono hover:text-[var(--al-text)] disabled:cursor-default"
							onclick={() => switchBranch(b.name)}
							disabled={b.current || busy !== ''}
							title={b.current ? 'current branch' : `switch to ${b.name}`}
						>{b.name}</button>
						{#if !b.current}
							<button class="text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => mergeBranch(b.name)} disabled={busy !== ''} title="fast-forward merge into current"><GitMerge size={11} /></button>
							<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteBranch(b.name)} disabled={busy !== ''} title="delete branch"><Trash2 size={11} /></button>
						{/if}
					</div>
				{/each}
				<div class="inline-flex items-center gap-1">
					<input class="{inp} w-32 py-1 text-xs" placeholder="new-branch" bind:value={newBranch} onkeydown={(e) => { if (e.key === 'Enter') createBranch(); }} />
					<button class={btnGhost} onclick={createBranch} disabled={busy === 'branch' || !newBranch.trim()}>
						{#if busy === 'branch'}<Loader2 size={12} class="animate-spin" />{:else}<Plus size={12} />{/if} Create
					</button>
				</div>
			</div>
		</div>
	{/if}

	<div class="grid grid-cols-[240px_1fr] gap-3" style="min-height:300px;">
		<!-- history -->
		<div class="overflow-y-auto rounded-md border border-[var(--al-border)]" style="max-height:52vh;">
			<button
				class="flex w-full items-center gap-1.5 border-b border-[var(--al-border)] px-2.5 py-1.5 text-left text-xs {selectedCommit === null ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}"
				onclick={() => loadDiff(null)}
			>
				<History size={12} /> Working changes
			</button>
			{#each commits as c (c.hash)}
				<div class="border-b border-[var(--al-border)] px-2.5 py-1.5 text-xs {selectedCommit === c.hash ? 'bg-[var(--al-accent-weak)]' : 'hover:bg-[var(--al-panel-2)]'}">
					<button class="block w-full text-left" onclick={() => loadDiff(c.hash)}>
						<div class="truncate text-[var(--al-text)]">{c.message}</div>
						<div class="mono text-[10px] text-[var(--al-muted)]">{c.short} · {c.author}</div>
					</button>
					<button class="mt-1 inline-flex items-center gap-1 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => restore(c.hash)} disabled={busy === 'restore'}>
						<RotateCcw size={10} /> Restore
					</button>
				</div>
			{/each}
			{#if commits.length === 0}
				<p class="px-2.5 py-2 text-[11px] text-[var(--al-muted)]">No commits yet.</p>
			{/if}
		</div>

		<!-- diff -->
		<div class="overflow-auto rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] p-2" style="max-height:52vh;">
			{#if diffLines.length === 0}
				<p class="mono p-2 text-[11px] text-[var(--al-muted)]">{selectedCommit ? 'no diff' : 'no uncommitted changes'}</p>
			{:else}
				<pre class="mono text-[11px] leading-[1.5]">{#each diffLines as l, i (i)}<span style="color:{lineColor(l)};white-space:pre-wrap;">{l}
</span>{/each}</pre>
			{/if}
		</div>
	</div>

	<!-- remote -->
	<div class="mt-3 border-t border-[var(--al-border)] pt-3">
		<button class="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]" onclick={() => (showRemote = !showRemote)}>
			<Cloud size={13} /> Remote {showRemote ? '▾' : '▸'}
		</button>
		{#if showRemote}
			<div class="space-y-2">
				<div class="flex items-end gap-2">
					<input class="{inp} mono flex-1" placeholder="https://github.com/you/repo.git" bind:value={remoteUrl} />
					<button class={btnGhost} onclick={setRemote} disabled={busy === 'remote'}>Save remote</button>
				</div>
				<div class="flex items-end gap-2">
					<input class="{inp} w-32" placeholder="username" bind:value={username} />
					<input class="{inp} flex-1" type="password" placeholder="token (transient)" bind:value={token} />
					<button class={btnGhost} onclick={() => sync('push')} disabled={busy === 'push' || !status?.hasRemote}>
						{#if busy === 'push'}<Loader2 size={12} class="animate-spin" />{:else}<Upload size={12} />{/if} Push
					</button>
					<button class={btnGhost} onclick={() => sync('pull')} disabled={busy === 'pull' || !status?.hasRemote}>
						{#if busy === 'pull'}<Loader2 size={12} class="animate-spin" />{:else}<Download size={12} />{/if} Pull
					</button>
				</div>
				<p class="text-[10px] text-[var(--al-muted)]">The token is used only for this request and never stored. State and secrets are excluded via .gitignore.</p>
			</div>
		{/if}
	</div>
</Modal>
