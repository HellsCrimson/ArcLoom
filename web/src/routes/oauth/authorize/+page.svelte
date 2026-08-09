<script lang="ts">
	// OAuth consent screen. An MCP client sends the browser here; the user is
	// already signed into ArcLoom (the layout's login gate guarantees it), so
	// approving is one click rather than a second login.
	//
	// The scopes are ticked by the USER, not requested by the client: a generic
	// MCP client has no idea ArcLoom's vocabulary is resources/deploy/git/approve,
	// so asking it to request them would mean everything or nothing. The person
	// approving is the one who knows.
	import { onMount } from 'svelte';
	import { ShieldCheck, TriangleAlert } from 'lucide-svelte';

	type Info = {
		clientName: string;
		clientId: string;
		redirect: string;
		scopes: string[];
		requested: string[];
	};

	let info = $state<Info | null>(null);
	let error = $state('');
	let loading = $state(true);
	let submitting = $state(false);
	let chosen = $state<string[]>([]);
	let query = $state('');

	const HINTS: Record<string, string> = {
		resources: 'edit resources, modules, variables, providers, secrets and files',
		deploy: 'run plan, apply and state operations',
		git: 'commit, push, pull and branch',
		approve: 'approve a saved plan so a gated apply can run'
	};

	onMount(async () => {
		query = window.location.search;
		try {
			const res = await fetch(`/api/oauth/authorize/info${query}`, { credentials: 'same-origin' });
			if (!res.ok) throw new Error((await res.json())?.error || res.statusText);
			info = await res.json();
			// Pre-tick only what the client explicitly asked for and the user can
			// actually grant. Defaulting to everything is how consent screens become
			// a formality nobody reads.
			chosen = (info?.requested ?? []).filter((s) => info?.scopes.includes(s));
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	});

	function toggle(s: string) {
		chosen = chosen.includes(s) ? chosen.filter((x) => x !== s) : [...chosen, s];
	}

	async function decide(approve: boolean) {
		if (!approve) {
			// Denying just closes the loop locally; the client times out its wait.
			window.close();
			error = 'Request denied. You can close this tab.';
			info = null;
			return;
		}
		submitting = true;
		error = '';
		try {
			const res = await fetch('/api/oauth/authorize', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ query, scopes: chosen, projects: [] })
			});
			if (!res.ok) throw new Error((await res.json())?.error || res.statusText);
			const { redirect } = await res.json();
			window.location.href = redirect;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			submitting = false;
		}
	}
</script>

<div class="flex min-h-screen items-center justify-center bg-[var(--al-bg)] p-6">
	<div class="w-full max-w-md rounded-lg border border-[var(--al-border)] bg-[var(--al-panel)] p-6">
		{#if loading}
			<p class="text-sm text-[var(--al-muted)]">Checking the request…</p>
		{:else if error}
			<div class="flex items-start gap-2 text-sm text-red-300">
				<TriangleAlert size={16} class="mt-0.5 shrink-0" />
				<span>{error}</span>
			</div>
		{:else if info}
			<div class="mb-4 flex items-center gap-2">
				<ShieldCheck size={18} class="text-[var(--al-accent)]" />
				<h1 class="text-base font-medium text-[var(--al-text)]">Authorize {info.clientName}</h1>
			</div>
			<p class="mb-4 text-xs text-[var(--al-muted)]">
				<span class="text-[var(--al-text)]">{info.clientName}</span> wants to access ArcLoom as you.
				Choose what it may do — it can never do more than you can, and it can never perform admin
				actions. You can revoke this at any time from the API keys panel.
			</p>

			<div class="mb-4 space-y-1.5">
				{#each info.scopes as s (s)}
					<label class="flex items-start gap-2 text-xs">
						<input type="checkbox" class="mt-0.5" checked={chosen.includes(s)} onchange={() => toggle(s)} />
						<span>
							<span class="mono text-[var(--al-text)]">{s}</span>
							<span class="text-[var(--al-muted)]"> — {HINTS[s] ?? ''}</span>
						</span>
					</label>
				{/each}
				{#if info.scopes.length === 0}
					<p class="text-xs text-[var(--al-muted)]">
						You hold no grantable permissions, so this will be a read-only authorization.
					</p>
				{/if}
			</div>

			<p class="mb-4 text-[11px] text-[var(--al-muted)]">
				With nothing ticked it gets read-only access. Redirects to
				<span class="mono">{info.redirect}</span>.
			</p>

			<div class="flex gap-2">
				<button
					class="flex-1 rounded-md bg-[var(--al-accent)] px-3 py-2 text-sm font-medium text-black disabled:opacity-50"
					disabled={submitting}
					onclick={() => decide(true)}
				>
					{submitting ? 'Authorizing…' : 'Authorize'}
				</button>
				<button
					class="rounded-md border border-[var(--al-border)] px-3 py-2 text-sm text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
					disabled={submitting}
					onclick={() => decide(false)}
				>
					Deny
				</button>
			</div>
		{/if}
	</div>
</div>
