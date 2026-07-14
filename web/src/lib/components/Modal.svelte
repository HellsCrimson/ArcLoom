<script lang="ts">
	import { X } from 'lucide-svelte';
	import type { Snippet } from 'svelte';

	let {
		title,
		open = $bindable(false),
		children,
		footer
	}: { title: string; open: boolean; children: Snippet; footer?: Snippet } = $props();

	function close() {
		open = false;
	}
	function onkey(e: KeyboardEvent) {
		if (e.key === 'Escape') close();
	}
</script>

<svelte:window onkeydown={onkey} />

{#if open}
	<div
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
		role="button"
		tabindex="-1"
		onclick={(e) => {
			if (e.target === e.currentTarget) close();
		}}
		onkeydown={() => {}}
	>
		<div
			class="flex max-h-[85vh] w-full max-w-2xl flex-col overflow-hidden rounded-lg border border-[var(--al-border)] bg-[var(--al-panel)] shadow-2xl"
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
