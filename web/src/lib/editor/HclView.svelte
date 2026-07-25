<script lang="ts">
	import type { Snippet } from 'svelte';
	import { highlightHcl } from './highlight';

	let {
		hcl = '',
		title = 'main.tf',
		placeholder = '// save a resource to preview HCL',
		right,
		actions
	}: {
		hcl?: string;
		title?: string;
		placeholder?: string;
		/** Controls rendered inline in the title bar — for one or two small buttons. */
		right?: Snippet;
		/** Controls rendered on their own wrapping row below the title bar. A row of
		 *  its own is what keeps a narrow panel from overflowing horizontally. */
		actions?: Snippet;
	} = $props();

	const lines = $derived(highlightHcl(hcl || ''));
</script>

<!-- Live HCL viewer shared by the graph editor and the main shell so the
     generated code looks identical in both. -->
<div class="flex h-full min-h-0 flex-col" style="background:var(--al-code-bg);">
	<div
		class="flex h-11 flex-none items-center justify-between border-b px-4"
		style="border-color:var(--al-code-border);"
	>
		<div class="flex items-center gap-2">
			<div style="width:6px;height:6px;border-radius:50%;background:var(--al-ok);"></div>
			<div class="font-mono" style="font-size:12.5px;font-weight:600;color:var(--al-code-text);">{title}</div>
		</div>
		{#if right}{@render right()}{/if}
	</div>
	{#if actions}
		<div
			class="flex flex-none flex-wrap items-center gap-1.5 border-b px-4 py-2"
			style="border-color:var(--al-code-border);"
		>
			{@render actions()}
		</div>
	{/if}
	<div class="flex-1 overflow-auto px-[18px] py-4 font-mono" style="font-size:12.5px;line-height:1.7;">
		{#if hcl}
			{#each lines as line, i (i)}
				<div>{#each line as tok, j (j)}<span style="color:{tok.c};white-space:pre;">{tok.t}</span>{/each}</div>
			{/each}
		{:else}
			<div style="color:var(--al-code-muted);white-space:pre;">{placeholder}</div>
		{/if}
	</div>
</div>
