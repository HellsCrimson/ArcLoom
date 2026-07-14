<script lang="ts">
	import type { Snippet } from 'svelte';
	import { highlightHcl } from './highlight';

	let {
		hcl = '',
		title = 'main.tf',
		placeholder = '// save a resource to preview HCL',
		right
	}: {
		hcl?: string;
		title?: string;
		placeholder?: string;
		right?: Snippet;
	} = $props();

	const lines = $derived(highlightHcl(hcl || ''));
</script>

<!-- Live HCL viewer shared by the graph editor and the main shell so the
     generated code looks identical in both. -->
<div class="flex h-full min-h-0 flex-col" style="background:#0b0f18;">
	<div
		class="flex h-11 flex-none items-center justify-between border-b px-4"
		style="border-color:rgba(255,255,255,0.07);"
	>
		<div class="flex items-center gap-2">
			<div style="width:6px;height:6px;border-radius:50%;background:#3fb950;"></div>
			<div class="font-mono" style="font-size:12.5px;font-weight:600;color:#c9d3dc;">{title}</div>
		</div>
		{#if right}{@render right()}{/if}
	</div>
	<div class="flex-1 overflow-auto px-[18px] py-4 font-mono" style="font-size:12.5px;line-height:1.7;">
		{#if hcl}
			{#each lines as line, i (i)}
				<div>{#each line as tok, j (j)}<span style="color:{tok.c};white-space:pre;">{tok.t}</span>{/each}</div>
			{/each}
		{:else}
			<div style="color:#5b6472;white-space:pre;">{placeholder}</div>
		{/if}
	</div>
</div>
