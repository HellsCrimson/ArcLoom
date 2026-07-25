<script lang="ts">
	// A draggable divider between two panes. The parent owns the size: the handle
	// only reports how far the pointer moved along its axis, and calls onEnd when
	// the drag finishes (so the parent can persist the new size once, not on every
	// pointer move).
	let {
		orientation = 'vertical',
		onResize,
		onEnd,
		label
	}: {
		/** 'vertical' = a vertical bar dragged left/right (resizes a width);
		 *  'horizontal' = a horizontal bar dragged up/down (resizes a height). */
		orientation?: 'vertical' | 'horizontal';
		onResize: (delta: number) => void;
		onEnd?: () => void;
		label?: string;
	} = $props();

	const vertical = $derived(orientation === 'vertical');

	let dragging = $state(false);
	let last = 0;

	function pointerdown(e: PointerEvent) {
		e.preventDefault();
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		dragging = true;
		last = vertical ? e.clientX : e.clientY;
	}

	function pointermove(e: PointerEvent) {
		if (!dragging) return;
		const pos = vertical ? e.clientX : e.clientY;
		onResize(pos - last);
		last = pos;
	}

	function pointerup(e: PointerEvent) {
		if (!dragging) return;
		dragging = false;
		(e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);
		onEnd?.();
	}

	// Arrow keys nudge the split so it is reachable without a pointer.
	function keydown(e: KeyboardEvent) {
		const step = e.shiftKey ? 64 : 16;
		const back = vertical ? 'ArrowLeft' : 'ArrowUp';
		const fwd = vertical ? 'ArrowRight' : 'ArrowDown';
		if (e.key !== back && e.key !== fwd) return;
		e.preventDefault();
		onResize(e.key === back ? -step : step);
		onEnd?.();
	}
</script>

<!-- A focusable separator is the ARIA "window splitter" widget, which is exactly
     this: the arrow keys move the split. Svelte's a11y rules treat every
     separator as non-interactive, hence the two ignores. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
	role="separator"
	aria-orientation={vertical ? 'vertical' : 'horizontal'}
	aria-label={label ?? 'Resize panel'}
	tabindex="0"
	class="group relative flex-none {vertical ? 'w-1 cursor-col-resize' : 'h-1 cursor-row-resize'}"
	style="background:{dragging ? 'var(--al-accent)' : 'transparent'};touch-action:none;"
	onpointerdown={pointerdown}
	onpointermove={pointermove}
	onpointerup={pointerup}
	onpointercancel={pointerup}
	onkeydown={keydown}
>
	<!-- Widen the hit area beyond the 4px line without shifting the layout. -->
	<div
		class="absolute {vertical ? '-inset-x-1 inset-y-0' : '-inset-y-1 inset-x-0'} group-hover:bg-[var(--al-accent)]/40 group-focus-visible:bg-[var(--al-accent)]/60"
	></div>
</div>
