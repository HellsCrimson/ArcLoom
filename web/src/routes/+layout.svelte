<script lang="ts">
	import '../app.css';
	import favicon from '$lib/assets/favicon.svg';
	import { auth } from '$lib/stores/auth.svelte';
	import { theme } from '$lib/stores/theme.svelte';
	import Login from '$lib/components/Login.svelte';
	import { Loader2 } from 'lucide-svelte';

	let { children } = $props();

	// Sync the theme with the DOM and watch the OS preference (runs once).
	$effect(() => {
		theme.init();
	});

	// Resolve the current session once on mount. The heavy app (+page.svelte)
	// only mounts when authenticated. Pass showLoading so the gate shows the
	// loading state for this first resolution only.
	$effect(() => {
		auth.refresh(true);
	});
</script>

<svelte:head>
	<!--
	  The SVG tab icon (hashed by Vite, hence the import). It is the SEAMLESS
	  tile from brand/: at 16-32 px the twist slits fall under half a pixel and
	  turn the cord to mush, so the small mark keeps only the interlace. The
	  .ico / apple-touch / manifest fallbacks are declared in app.html, which is
	  served on the first byte — this one arrives at hydration.
	-->
	<link rel="icon" href={favicon} type="image/svg+xml" />
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
