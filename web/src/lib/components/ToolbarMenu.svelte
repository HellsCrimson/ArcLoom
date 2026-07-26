<script lang="ts">
	// lucide-svelte ships legacy class components, so ComponentType (not
	// Component) is what an icon value satisfies.
	import type { ComponentType } from 'svelte';
	import { MoreHorizontal } from 'lucide-svelte';

	// One top-bar action. The same descriptor renders as a bar button or as a row
	// in this menu, so an action that no longer fits the width is never lost —
	// which is what used to push the bar past the viewport and produce a
	// document-level horizontal scrollbar.
	export interface ToolItem {
		id: string;
		label: string;
		icon: ComponentType;
		run: () => void;
		title?: string;
		/** Small count shown after the label (variables, secrets). */
		badge?: string;
		/** Renders in the accent colour (the Editor toggle when it is open). */
		active?: boolean;
		/** Lower stays in the bar longer; ties keep declaration order. */
		priority: number;
	}

	let {
		items,
		open = $bindable(false),
		label = 'More'
	}: { items: ToolItem[]; open?: boolean; label?: string } = $props();

	// Which edge the panel hangs from. The button is at the end of a full-width
	// toolbar on a desktop (so it must open leftwards to stay on screen) but sits
	// near the left edge once the bar has wrapped — anchoring blindly to one side
	// puts the panel half outside the viewport at one width or the other.
	let trigger = $state<HTMLButtonElement>();
	let anchorRight = $state(true);

	function toggle(e: MouseEvent) {
		// The window-level click handler closes every top-bar menu, so this toggle
		// must not bubble into it.
		e.stopPropagation();
		if (!open && trigger) {
			const r = trigger.getBoundingClientRect();
			anchorRight = r.left > window.innerWidth / 2;
		}
		open = !open;
	}
</script>

<div class="relative flex-none">
	<button
		bind:this={trigger}
		class="inline-flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs font-medium text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
		title="{label} actions ({items.length})"
		aria-haspopup="menu"
		aria-expanded={open}
		onclick={toggle}
	>
		<MoreHorizontal size={14} />
		<span class="hidden sm:inline">{label}</span>
	</button>

	{#if open}
		<div
			class="absolute top-full z-30 mt-1 max-h-[70vh] w-52 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] py-1 shadow-xl {anchorRight
				? 'right-0'
				: 'left-0'}"
			role="menu"
			tabindex="-1"
		>
			{#each items as item (item.id)}
				{@const Icon = item.icon}
				<button
					class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-[var(--al-panel-2)] {item.active
						? 'text-[var(--al-accent)]'
						: 'text-[var(--al-text)]'}"
					role="menuitem"
					title={item.title}
					onclick={() => {
						open = false;
						item.run();
					}}
				>
					<Icon size={13} />
					{item.label}
					{#if item.badge}<span class="ml-auto text-[10px] text-[var(--al-muted)]">{item.badge}</span>{/if}
				</button>
			{/each}
		</div>
	{/if}
</div>
