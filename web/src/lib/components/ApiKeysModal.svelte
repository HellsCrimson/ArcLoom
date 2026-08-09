<script lang="ts">
	// Self-service API keys: the non-browser credential for CI, scripts and MCP
	// clients. Any authenticated user manages their own, and a key can only ever
	// be narrower than its owner — so the scope checkboxes are filtered to what
	// this user actually holds, and the server refuses anything wider anyway.
	import { api, type ApiKey } from '$lib/api/client';
	import { auth } from '$lib/stores/auth.svelte';
	import Modal from './Modal.svelte';
	import { Copy, Plus, Trash2, TriangleAlert } from 'lucide-svelte';

	let { open = $bindable(false), projects = [] }: { open?: boolean; projects?: { id: string; name: string }[] } =
		$props();

	let keys = $state<ApiKey[]>([]);
	let loading = $state(false);
	let error = $state('');

	// Create form.
	let label = $state('');
	let scopes = $state<string[]>([]);
	let restrictProjects = $state(false);
	let chosenProjects = $state<string[]>([]);
	let expiresIn = $state('');
	let creating = $state(false);

	// The freshly minted token — shown once, never retrievable again.
	let newToken = $state('');

	const SCOPES: { id: string; label: string; hint: string }[] = [
		{ id: 'resources', label: 'resources', hint: 'edit resources, modules, variables, providers, secrets, files' },
		{ id: 'deploy', label: 'deploy', hint: 'run plan, apply, destroy and state operations' },
		{ id: 'git', label: 'git', hint: 'commit, push, pull, branch' },
		{ id: 'approve', label: 'approve', hint: 'approve a saved plan for a gated apply' }
	];

	// Only offer what the signed-in user holds: granting more is a 400 server-side.
	const grantable = $derived(SCOPES.filter((s) => auth.can(s.id)));

	async function load() {
		loading = true;
		error = '';
		try {
			keys = await api.keys.list();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		if (open && keys.length === 0 && !loading) load();
	});

	// Closing must clear the one-time token: re-opening should never put a live
	// credential back on screen unasked (same rule as the secret reveal).
	$effect(() => {
		if (!open) {
			newToken = '';
			error = '';
		}
	});

	function toggleScope(id: string) {
		scopes = scopes.includes(id) ? scopes.filter((s) => s !== id) : [...scopes, id];
	}

	function toggleProject(id: string) {
		chosenProjects = chosenProjects.includes(id)
			? chosenProjects.filter((p) => p !== id)
			: [...chosenProjects, id];
	}

	async function create() {
		if (!label.trim()) {
			error = 'Give the key a label so you can recognise it later.';
			return;
		}
		creating = true;
		error = '';
		try {
			const res = await api.keys.create({
				label: label.trim(),
				scopes,
				projects: restrictProjects ? chosenProjects : [],
				expiresIn: expiresIn || undefined
			});
			newToken = res.token;
			label = '';
			scopes = [];
			restrictProjects = false;
			chosenProjects = [];
			expiresIn = '';
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			creating = false;
		}
	}

	async function remove(k: ApiKey) {
		if (!confirm(`Revoke "${k.label}"? Anything using it stops working immediately.`)) return;
		try {
			await api.keys.remove(k.id);
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	// navigator.clipboard is undefined on a plain-HTTP origin, which is how a
	// homelab reaches this app — so copying is best-effort and the field stays
	// select-all either way.
	let copied = $state(false);
	async function copy() {
		try {
			await navigator.clipboard.writeText(newToken);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* select-all is the fallback */
		}
	}

	function fmt(ts?: string) {
		if (!ts) return '—';
		return new Date(ts).toLocaleString();
	}

	const input =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm text-[var(--al-text)]';
</script>

<Modal title="API keys" bind:open wide>
	<div class="space-y-4 text-sm">
		<p class="text-xs text-[var(--al-muted)]">
			API keys let non-browser clients — CI, scripts, and AI agents over MCP — use ArcLoom. A key can
			never do more than you can: its access is your permissions <em>and</em> its scopes. Keys cannot
			perform admin actions.
		</p>

		{#if error}
			<div class="rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-300">
				{error}
			</div>
		{/if}

		{#if newToken}
			<div class="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
				<div class="flex items-center gap-2 text-xs font-medium text-amber-300">
					<TriangleAlert size={14} /> Copy this now — it is not shown again.
				</div>
				<div class="flex items-center gap-2">
					<input class="{input} mono" readonly value={newToken} onclick={(e) => e.currentTarget.select()} />
					<button
						class="flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1.5 text-xs hover:bg-[var(--al-panel-2)]"
						onclick={copy}
					>
						<Copy size={13} />
						{copied ? 'Copied' : 'Copy'}
					</button>
				</div>
				<p class="text-[11px] text-[var(--al-muted)]">
					Register it with an MCP client, e.g.
					<code class="mono">claude mcp add --transport http arcloom {location.origin}/api/mcp --header "Authorization: Bearer …"</code>
				</p>
			</div>
		{/if}

		<!-- Create -->
		<div class="space-y-3 rounded-md border border-[var(--al-border)] p-3">
			<div class="text-xs font-medium text-[var(--al-text)]">New key</div>
			<input class={input} placeholder="Label, e.g. claude-code or ci" bind:value={label} />

			<div>
				<div class="mb-1 text-[11px] text-[var(--al-muted)]">
					Scopes — leave all unchecked for a read-only key.
				</div>
				<div class="space-y-1">
					{#each grantable as s (s.id)}
						<label class="flex items-start gap-2 text-xs">
							<input
								type="checkbox"
								class="mt-0.5"
								checked={scopes.includes(s.id)}
								onchange={() => toggleScope(s.id)}
							/>
							<span>
								<span class="mono">{s.label}</span>
								<span class="text-[var(--al-muted)]"> — {s.hint}</span>
							</span>
						</label>
					{/each}
				</div>
			</div>

			{#if projects.length > 0}
				<div>
					<label class="flex items-center gap-2 text-xs">
						<input type="checkbox" checked={restrictProjects} onchange={() => (restrictProjects = !restrictProjects)} />
						Limit to specific projects
					</label>
					{#if restrictProjects}
						<div class="mt-1 max-h-32 space-y-1 overflow-y-auto rounded border border-[var(--al-border)] p-2">
							{#each projects as p (p.id)}
								<label class="flex items-center gap-2 text-xs">
									<input
										type="checkbox"
										checked={chosenProjects.includes(p.id)}
										onchange={() => toggleProject(p.id)}
									/>
									{p.name}
								</label>
							{/each}
						</div>
					{/if}
				</div>
			{/if}

			<div class="flex items-center gap-2">
				<label class="text-xs text-[var(--al-muted)]" for="expiry">Expires in</label>
				<select id="expiry" class="{input} w-auto" bind:value={expiresIn}>
					<option value="">never</option>
					<option value="720h">30 days</option>
					<option value="2160h">90 days</option>
					<option value="8760h">1 year</option>
				</select>
				<button
					class="ml-auto flex items-center gap-1 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-xs font-medium text-black disabled:opacity-50"
					disabled={creating}
					onclick={create}
				>
					<Plus size={13} />
					{creating ? 'Creating…' : 'Create key'}
				</button>
			</div>
		</div>

		<!-- List -->
		{#if loading}
			<div class="text-xs text-[var(--al-muted)]">Loading…</div>
		{:else if keys.length === 0}
			<div class="text-xs text-[var(--al-muted)]">No API keys yet.</div>
		{:else}
			<table class="w-full text-left text-xs">
				<thead class="text-[var(--al-muted)]">
					<tr>
						<th class="py-1">Label</th>
						<th class="py-1">Scopes</th>
						<th class="py-1">Projects</th>
						<th class="py-1">Last used</th>
						<th class="py-1">Expires</th>
						<th></th>
					</tr>
				</thead>
				<tbody>
					{#each keys as k (k.id)}
						<tr class="border-t border-[var(--al-border)]">
							<td class="py-1.5">{k.label}</td>
							<td class="mono py-1.5">{k.scopes.length ? k.scopes.join(', ') : 'read-only'}</td>
							<td class="py-1.5">{k.projects.length ? k.projects.length + ' project(s)' : 'all'}</td>
							<td class="py-1.5 text-[var(--al-muted)]">{fmt(k.lastUsedAt)}</td>
							<td class="py-1.5 text-[var(--al-muted)]">{fmt(k.expiresAt)}</td>
							<td class="py-1.5 text-right">
								<button
									class="rounded p-1 text-red-400 hover:bg-[var(--al-panel-2)]"
									title="Revoke"
									aria-label="Revoke {k.label}"
									onclick={() => remove(k)}
								>
									<Trash2 size={13} />
								</button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{/if}
	</div>
</Modal>
