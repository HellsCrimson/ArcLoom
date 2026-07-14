<script lang="ts">
	import { X } from 'lucide-svelte';
	import type { Snippet } from 'svelte';

	let {
		title,
		open = $bindable(false),
		wide = false,
		children,
		footer
	}: { title: string; open: boolean; wide?: boolean; children: Snippet; footer?: Snippet } = $props();

	function close() {
		open = false;
	}
	function onkey(e: KeyboardEvent) {
		if (e.key === 'Escape') close();
	}
</script>

<svelte:window onkeydown={onkey} />

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
		onclick={(e) => {
			if (e.target === e.currentTarget) close();
		}}
	>
		<div
			class="flex max-h-[85vh] w-full flex-col overflow-hidden rounded-lg border border-[var(--al-border)] bg-[var(--al-panel)] shadow-2xl {wide ? 'max-w-4xl' : 'max-w-2xl'}"
		>
			<div class="flex items-center justify-between border-b border-[var(--al-border)] px-4 py-2.5">
				<h2 class="text-sm font-semibold text-[var(--al-text)]">{title}</h2>
				<button class="text-[var(--al-muted)] hover:text-[var(--al-text)]" onclick={close} aria-label="Close">
					<X size={18} />
				</button>
			</div>
			<div class="min-h-0 flex-1 overflow-y-auto p-4">
				{@render children()}
			</div>
			{#if footer}
				<div class="flex justify-end gap-2 border-t border-[var(--al-border)] px-4 py-2.5">
					{@render footer()}
				</div>
			{/if}
		</div>
	</div>
{/if}
