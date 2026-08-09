<script lang="ts">
	import { api, CommitPushedError } from '$lib/api/client';
	import type { GitStatus, GitCommit, GitBranch as GitBranchType, GitCredMeta, Project } from '$lib/types';
	import Modal from './Modal.svelte';
	import {
		GitBranch, GitCommitVertical, History, Loader2, Upload, Download, RotateCcw, Cloud,
		GitMerge, Trash2, Plus, ArrowUp, ArrowDown, KeyRound, Undo2, AlertTriangle
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

	// Per-file staging: which changed paths the next commit will include.
	let staged = $state<Set<string>>(new Set());

	// Remote
	let remoteUrl = $state('');
	let username = $state('');
	let token = $state('');
	let showRemote = $state(false);

	// Stored credentials
	let cred = $state<GitCredMeta | null>(null);
	let credType = $state<'https' | 'ssh'>('https');
	let credUser = $state('');
	let credToken = $state('');
	let credKey = $state('');
	let credPass = $state('');

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
			// Default the next commit to stage every changed file.
			staged = new Set((status.files ?? []).map((f) => f.path));
			cred = await api.git.credentials(projectId);
			if (cred.stored && (cred.authType === 'https' || cred.authType === 'ssh')) {
				credType = cred.authType;
				credUser = cred.username ?? credUser;
			}
			await loadDiff(selectedCommit);
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function toggleStaged(path: string) {
		const next = new Set(staged);
		if (next.has(path)) next.delete(path);
		else next.add(path);
		staged = next;
	}

	function toggleAllStaged() {
		const all = (status?.files ?? []).map((f) => f.path);
		staged = staged.size === all.length ? new Set() : new Set(all);
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
			const all = (status?.files ?? []).map((f) => f.path);
			// Send paths only for a partial selection; empty = stage everything.
			const paths = staged.size === all.length ? undefined : [...staged];
			const { hash } = await api.git.commit(projectId, message.trim(), paths);
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

	async function saveCred() {
		busy = 'cred';
		err = '';
		info = '';
		try {
			cred = await api.git.setCredentials(projectId, {
				authType: credType,
				username: credUser.trim() || undefined,
				token: credType === 'https' ? credToken || undefined : undefined,
				sshKey: credType === 'ssh' ? credKey || undefined : undefined,
				passphrase: credType === 'ssh' ? credPass || undefined : undefined
			});
			credToken = '';
			credKey = '';
			credPass = '';
			info = 'Credential saved';
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = '';
		}
	}

	async function clearCred() {
		if (!confirm('Remove the stored git credential for this project?')) return;
		busy = 'cred';
		err = '';
		info = '';
		try {
			await api.git.deleteCredentials(projectId);
			cred = await api.git.credentials(projectId);
			credToken = '';
			credKey = '';
			credPass = '';
			info = 'Credential removed';
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

	// --- Undo the last commit (the one history-rewriting operation) ---
	// Everything else in this panel moves history forward; this drops the newest
	// commit and hands its files back as uncommitted changes, so a commit made by
	// mistake can be reworded, split or extended. Guarded by an inline warning
	// rather than a bare confirm() because it rewrites history, and escalated
	// when origin already has the commit (the server refuses without force).
	let undoing = $state(false);
	let undoPushed = $state(false);
	// Best-effort local read of "already on origin": a tracking ref exists and we
	// are not ahead of it. The server decides for real.
	const lastCommitPushed = $derived(
		!!status?.hasRemote && !!status?.hasCommits && (status?.ahead ?? 0) === 0
	);

	function askUndo() {
		undoing = true;
		undoPushed = false;
		err = '';
		info = '';
	}

	async function undoLastCommit(force: boolean) {
		busy = 'undo';
		err = '';
		try {
			const undone = await api.git.undoCommit(projectId, force);
			undoing = false;
			undoPushed = false;
			info = `Undid ${undone.short} — its files are uncommitted changes again${
				commits.length === 1 ? ' (the repository has no commits now)' : ''
			}`;
			selectedCommit = null;
			await refresh();
		} catch (e) {
			if (e instanceof CommitPushedError) {
				// Stale status: the commit is on origin after all — re-ask with the
				// stronger warning instead of failing the user's intent outright.
				undoPushed = true;
			} else {
				err = (e as Error).message;
				undoing = false;
			}
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
	function fileColor(s: string): string {
		if (s === 'added' || s === 'untracked') return '#3fb950';
		if (s === 'deleted') return '#f85149';
		return '#d29922'; // modified
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
			{#if status.hasRemote}<span class="mono min-w-0 truncate">· {status.remoteUrl}</span>{/if}
			<!-- Push/pull live here, not inside the collapsible Remote section: they are
			     everyday actions and were unreachable while it was folded. -->
			<div class="ml-auto flex shrink-0 items-center gap-1.5">
				{#if status.hasRemote}
					<button class={btnGhost} onclick={() => sync('push')} disabled={busy === 'push'} title="Push to {status.remoteUrl}">
						{#if busy === 'push'}<Loader2 size={12} class="animate-spin" />{:else}<Upload size={12} />{/if} Push
					</button>
					<button class={btnGhost} onclick={() => sync('pull')} disabled={busy === 'pull'} title="Pull from {status.remoteUrl}">
						{#if busy === 'pull'}<Loader2 size={12} class="animate-spin" />{:else}<Download size={12} />{/if} Pull
					</button>
				{:else}
					<button class={btnGhost} onclick={() => (showRemote = true)} title="No remote configured yet">
						<Cloud size={12} /> Add remote
					</button>
				{/if}
			</div>
		{:else}
			<span>not a repository yet — your first commit initializes it</span>
		{/if}
	</div>

	<!-- changed files (per-file staging) -->
	{#if status && !status.clean && (status.files?.length ?? 0) > 0}
		<div class="mb-3 max-h-40 overflow-y-auto rounded-md border border-[var(--al-border)]">
			<button
				class="flex w-full items-center gap-2 border-b border-[var(--al-border)] px-2.5 py-1.5 text-left text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]"
				onclick={toggleAllStaged}
			>
				<input type="checkbox" checked={staged.size === (status.files?.length ?? 0)} tabindex="-1" class="pointer-events-none accent-[var(--al-accent)]" />
				Stage {staged.size}/{status.files?.length} for commit
			</button>
			{#each status.files ?? [] as f (f.path)}
				<label class="flex cursor-pointer items-center gap-2 px-2.5 py-1 text-xs hover:bg-[var(--al-panel-2)]">
					<input type="checkbox" checked={staged.has(f.path)} onchange={() => toggleStaged(f.path)} class="accent-[var(--al-accent)]" />
					<span class="mono truncate text-[var(--al-text)]" style="flex:1;">{f.path}</span>
					<span class="text-[10px] uppercase" style="color:{fileColor(f.status)};">{f.status}</span>
				</label>
			{/each}
		</div>
	{/if}

	<div class="mb-4 flex items-end gap-2">
		<input class="{inp} flex-1" placeholder="commit message" bind:value={message} onkeydown={(e) => { if (e.key === 'Enter') commit(); }} />
		<button class={btnPrimary} onclick={commit} disabled={busy === 'commit' || !message.trim() || status?.clean || ((status?.files?.length ?? 0) > 0 && staged.size === 0)}>
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
			{#each commits as c, i (c.hash)}
				<div class="border-b border-[var(--al-border)] px-2.5 py-1.5 text-xs {selectedCommit === c.hash ? 'bg-[var(--al-accent-weak)]' : 'hover:bg-[var(--al-panel-2)]'}">
					<button class="block w-full text-left" onclick={() => loadDiff(c.hash)}>
						<div class="truncate text-[var(--al-text)]">{c.message}</div>
						<div class="mono text-[10px] text-[var(--al-muted)]">{c.short} · {c.author}{#if i === 0}<span class="uppercase"> · head</span>{/if}</div>
					</button>
					<div class="mt-1 flex items-center gap-2">
						<button class="inline-flex items-center gap-1 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => restore(c.hash)} disabled={busy === 'restore'}>
							<RotateCcw size={10} /> Restore
						</button>
						{#if i === 0 && !undoing}
							<!-- Only HEAD can be undone: dropping an older commit would mean
							     rewriting everything on top of it. -->
							<button
								class="inline-flex items-center gap-1 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-danger)]"
								title="Drop this commit and keep its files as uncommitted changes (rewrites history)"
								onclick={askUndo}
								disabled={busy !== ''}
							>
								<Undo2 size={10} /> Undo commit
							</button>
						{/if}
					</div>

					{#if i === 0 && undoing}
						<div
							class="mt-1.5 rounded border p-2 text-[10px] leading-snug"
							style="border-color:var(--al-danger);background:color-mix(in srgb, var(--al-danger) 10%, transparent);"
						>
							<div class="mb-1 flex items-center gap-1 font-semibold text-[var(--al-danger)]">
								<AlertTriangle size={11} /> This rewrites history
							</div>
							<p class="mb-1.5 text-[var(--al-muted)]">
								<span class="mono text-[var(--al-text)]">{c.short}</span> is dropped from the log.
								Its files stay on disk as uncommitted changes, so nothing is lost and you can
								commit again — but the commit itself disappears from the record.
							</p>
							{#if undoPushed || lastCommitPushed}
								<p class="mb-1.5 text-[var(--al-danger)]">
									This commit is already on <span class="mono">origin</span>. Undoing it locally
									diverges from the remote, and merge here is fast-forward only — you would have
									to force-push to reconcile.
								</p>
							{/if}
							<div class="flex items-center gap-2">
								<button
									class="rounded px-2 py-0.5 font-semibold text-white"
									style="background:var(--al-danger);"
									onclick={() => undoLastCommit(undoPushed || lastCommitPushed)}
									disabled={busy === 'undo'}
								>
									{#if busy === 'undo'}<Loader2 size={10} class="inline animate-spin" />{/if}
									{undoPushed || lastCommitPushed ? 'Undo anyway' : 'Undo commit'}
								</button>
								<button
									class="text-[var(--al-muted)] hover:text-[var(--al-text)]"
									onclick={() => { undoing = false; undoPushed = false; }}
									disabled={busy === 'undo'}>Cancel</button
								>
							</div>
						</div>
					{/if}
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
			<div class="space-y-3">
				<div class="flex items-end gap-2">
					<input class="{inp} mono flex-1" placeholder="https://github.com/you/repo.git  ·  git@github.com:you/repo.git" bind:value={remoteUrl} />
					<button class={btnGhost} onclick={setRemote} disabled={busy === 'remote'}>Save remote</button>
				</div>

				<!-- stored credentials -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-2 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">
						<KeyRound size={12} /> Saved credential
						{#if cred?.stored}
							<span class="rounded bg-[var(--al-accent-weak)] px-1.5 py-0.5 text-[10px] font-medium normal-case text-[var(--al-text)]">
								{cred.authType === 'ssh' ? 'SSH key' : 'HTTPS token'} stored
							</span>
							<button class="ml-auto text-[10px] normal-case text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={clearCred} disabled={busy === 'cred'}>Remove</button>
						{/if}
					</div>
					<div class="mb-2 flex gap-1.5 text-xs">
						<button class="rounded-md border px-2 py-1 {credType === 'https' ? 'border-[var(--al-accent)] bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'border-[var(--al-border)] text-[var(--al-muted)]'}" onclick={() => (credType = 'https')}>HTTPS + token</button>
						<button class="rounded-md border px-2 py-1 {credType === 'ssh' ? 'border-[var(--al-accent)] bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'border-[var(--al-border)] text-[var(--al-muted)]'}" onclick={() => (credType = 'ssh')}>SSH key</button>
					</div>
					{#if credType === 'https'}
						<div class="flex items-end gap-2">
							<input class="{inp} w-32" placeholder="username" bind:value={credUser} />
							<input class="{inp} flex-1" type="password" placeholder={cred?.hasToken ? 'token (kept if blank)' : 'personal access token'} bind:value={credToken} />
						</div>
					{:else}
						<div class="space-y-2">
							<textarea class="{inp} mono w-full text-[11px]" rows="3" placeholder={cred?.hasSSHKey ? 'SSH private key (kept if blank)' : '-----BEGIN OPENSSH PRIVATE KEY-----'} bind:value={credKey}></textarea>
							<input class="{inp} w-full" type="password" placeholder={cred?.hasPassphrase ? 'key passphrase (kept if blank)' : 'key passphrase (optional)'} bind:value={credPass} />
						</div>
					{/if}
					<div class="mt-2 flex justify-end">
						<button class={btnGhost} onclick={saveCred} disabled={busy === 'cred'}>
							{#if busy === 'cred'}<Loader2 size={12} class="animate-spin" />{:else}<KeyRound size={12} />{/if} Save credential
						</button>
					</div>
				</div>

				<!-- transient credentials for the header's push/pull -->
				<div>
					<div class="mb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">One-off credentials for push/pull</div>
					<div class="flex items-end gap-2">
						<input class="{inp} w-32" placeholder="username" bind:value={username} />
						<input class="{inp} flex-1" type="password" placeholder={cred?.stored ? 'token (uses saved credential if blank)' : 'token (transient)'} bind:value={token} />
					</div>
				</div>
				<p class="text-[10px] text-[var(--al-muted)]">Push and Pull are at the top of this panel. Saved credentials are encrypted at rest (needs ARCLOOM_SECRET_KEY). The token above is transient and overrides the saved one for that request only. State and secrets are excluded via .gitignore.</p>
			</div>
		{/if}
	</div>
</Modal>
