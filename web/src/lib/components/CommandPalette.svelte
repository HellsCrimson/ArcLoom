<script lang="ts">
	import { Search, CornerDownLeft } from 'lucide-svelte';

	// One palette entry: a command ("Plan", "Open state…") or a resource jump
	// target. Entries arrive pre-filtered by permission; the palette only does
	// text matching and keyboard navigation.
	export interface PaletteItem {
		id: string;
		label: string;
		hint?: string;
		group: string; // section header, e.g. "commands" | "resources"
		mono?: boolean;
		run: () => void;
	}

	let { open = $bindable(false), items }: { open: boolean; items: PaletteItem[] } = $props();

	let query = $state('');
	let active = $state(0);
	let listEl = $state<HTMLDivElement>();

	$effect(() => {
		if (open) {
			query = '';
			active = 0;
		}
	});

	// Every whitespace-separated term must appear in label+hint (case-insensitive).
	const filtered = $derived.by(() => {
		const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
		const pool = terms.length
			? items.filter((it) => {
					const hay = `${it.label} ${it.hint ?? ''}`.toLowerCase();
					return terms.every((t) => hay.includes(t));
				})
			: items;
		return pool.slice(0, 60);
	});
	$effect(() => {
		filtered; // clamp the cursor when the list shrinks
		if (active >= filtered.length) active = Math.max(0, filtered.length - 1);
	});

	function runItem(it: PaletteItem) {
		open = false;
		it.run();
	}

	function onkey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.stopPropagation();
			open = false;
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			active = Math.min(filtered.length - 1, active + 1);
			scrollActive();
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			active = Math.max(0, active - 1);
			scrollActive();
		} else if (e.key === 'Enter') {
			e.preventDefault();
			if (filtered[active]) runItem(filtered[active]);
		}
	}

	function scrollActive() {
		requestAnimationFrame(() => {
			listEl?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' });
		});
	}

	function focusInput(node: HTMLInputElement) {
		node.focus();
	}
</script>

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="fixed inset-0 z-50 flex justify-center bg-black/50 pt-[12vh]"
		onclick={(e) => {
			if (e.target === e.currentTarget) open = false;
		}}
	>
		<div class="flex h-fit max-h-[60vh] w-full max-w-lg flex-col overflow-hidden rounded-lg border border-[var(--al-border)] bg-[var(--al-panel)] shadow-2xl">
			<div class="flex items-center gap-2 border-b border-[var(--al-border)] px-3 py-2.5">
				<Search size={15} class="flex-none text-[var(--al-muted)]" />
				<!-- svelte-ignore a11y_autofocus -->
				<input
					class="w-full bg-transparent text-sm text-[var(--al-text)] outline-none placeholder:text-[var(--al-muted)]"
					placeholder="Search commands and resources…"
					bind:value={query}
					use:focusInput
					onkeydown={onkey}
				/>
				<span class="flex-none rounded border border-[var(--al-border)] px-1.5 py-0.5 text-[10px] text-[var(--al-muted)]">esc</span>
			</div>
			<div class="min-h-0 overflow-y-auto py-1" bind:this={listEl}>
				{#each filtered as it, i (it.id)}
					{#if i === 0 || filtered[i - 1].group !== it.group}
						<div class="px-3 pb-0.5 pt-1.5 text-[10px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">{it.group}</div>
					{/if}
					<button
						class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm {i === active ? 'bg-[var(--al-accent-weak)] text-[var(--al-accent)]' : 'text-[var(--al-text)] hover:bg-[var(--al-panel-2)]'}"
						data-active={i === active}
						onmouseenter={() => (active = i)}
						onclick={() => runItem(it)}
					>
						<span class="min-w-0 truncate {it.mono ? 'mono text-[12.5px]' : ''}">{it.label}</span>
						{#if it.hint}<span class="ml-auto flex-none text-[11px] text-[var(--al-muted)]">{it.hint}</span>{/if}
						{#if i === active}<CornerDownLeft size={12} class="flex-none {it.hint ? '' : 'ml-auto'} text-[var(--al-muted)]" />{/if}
					</button>
				{/each}
				{#if filtered.length === 0}
					<p class="px-3 py-4 text-center text-sm text-[var(--al-muted)]">No matches.</p>
				{/if}
			</div>
		</div>
	</div>
{/if}
