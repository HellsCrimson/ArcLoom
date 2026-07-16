<script lang="ts">
	import '../app.css';
	import favicon from '$lib/assets/favicon.svg';
	import { auth } from '$lib/stores/auth.svelte';
	import Login from '$lib/components/Login.svelte';
	import { Loader2 } from 'lucide-svelte';

	let { children } = $props();

	// Resolve the current session once on mount. The heavy app (+page.svelte)
	// only mounts when authenticated.
	$effect(() => {
		auth.refresh();
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>ArcLoom</title>
</svelte:head>

{#if auth.loading}
	<div class="flex h-screen items-center justify-center bg-[var(--al-bg)] text-[var(--al-muted)]">
		<Loader2 size={22} class="animate-spin" />
	</div>
{:else if !auth.authenticated}
	<Login />
{:else}
	{@render children()}
{/if}
