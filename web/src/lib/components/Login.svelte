<script lang="ts">
	import { auth } from '$lib/stores/auth.svelte';
	import { Boxes, Loader2, LogIn } from 'lucide-svelte';

	let username = $state('');
	let password = $state('');
	let busy = $state(false);

	// Surface an SSO callback failure (the backend redirects to /?sso_error=...).
	let ssoError = $state('');
	$effect(() => {
		const params = new URLSearchParams(window.location.search);
		const err = params.get('sso_error');
		if (err) {
			ssoError = err;
			// Clean the URL so a refresh doesn't keep showing the error.
			window.history.replaceState({}, '', window.location.pathname);
		}
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (busy) return;
		busy = true;
		try {
			await auth.login(username, password);
		} finally {
			busy = false;
		}
	}

	const field =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] px-3 py-2 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

<div class="flex h-screen items-center justify-center bg-[var(--al-bg)] px-4">
	<form
		onsubmit={submit}
		class="w-full max-w-sm space-y-4 rounded-xl border border-[var(--al-border)] bg-[var(--al-panel)] p-6 shadow-xl"
	>
		<div class="flex items-center gap-2 text-lg font-semibold text-[var(--al-text)]">
			<Boxes size={22} class="text-[var(--al-accent)]" /> ArcLoom
		</div>
		<p class="text-sm text-[var(--al-muted)]">Sign in to continue.</p>

		<div class="space-y-1">
			<label for="login-username" class="block text-xs font-medium text-[var(--al-muted)]">Username</label>
			<input
				id="login-username"
				class={field}
				bind:value={username}
				autocomplete="username"
				autocapitalize="off"
				spellcheck="false"
				required
			/>
		</div>

		<div class="space-y-1">
			<label for="login-password" class="block text-xs font-medium text-[var(--al-muted)]">Password</label>
			<input
				id="login-password"
				type="password"
				class={field}
				bind:value={password}
				autocomplete="current-password"
				required
			/>
		</div>

		{#if auth.error || ssoError}
			<p class="text-sm text-[var(--al-danger)]">{auth.error || ssoError}</p>
		{/if}

		<button
			type="submit"
			disabled={busy}
			class="inline-flex w-full items-center justify-center gap-2 rounded-md bg-[var(--al-accent)] px-3 py-2 text-sm font-medium text-white transition hover:brightness-110 disabled:opacity-40"
		>
			{#if busy}<Loader2 size={16} class="animate-spin" />{:else}<LogIn size={16} />{/if}
			Sign in
		</button>

		{#if auth.authProviders.includes('oidc')}
			<!-- SSO (slice 2): rendered automatically once the backend advertises the
			     'oidc' provider. The endpoint is wired in slice 2. -->
			<a
				href="/api/auth/sso/oidc/login"
				class="inline-flex w-full items-center justify-center gap-2 rounded-md border border-[var(--al-border)] px-3 py-2 text-sm font-medium text-[var(--al-text)] transition hover:bg-[var(--al-panel-2)]"
			>
				Sign in with SSO
			</a>
		{/if}
	</form>
</div>
