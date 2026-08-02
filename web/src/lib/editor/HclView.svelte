<script lang="ts">
	// Live viewer for the generated configuration, shared by the graph editor and
	// the main shell so the code looks identical in both.
	//
	// It takes the FILE MAP the server generates, not one document: the config is
	// split by theme (terraform.tf / variables.tf / … / moved.tf) exactly as it is
	// written to disk and to Git, and this renders one tab per file. Before that
	// the pane showed `main.tf` alone, so `imports.tf` and `moved.tf` existed in
	// the project and in Git but nowhere the user could see them.
	import type { Snippet } from 'svelte';
	import { highlightHcl } from './highlight';

	let {
		files = {},
		placeholder = '// save a resource to preview HCL',
		right,
		actions
	}: {
		/** file name -> contents, as generated. Empty = nothing to preview yet. */
		files?: Record<string, string>;
		placeholder?: string;
		/** Controls rendered inline in the title bar — for one or two small buttons. */
		right?: Snippet;
		/** Controls rendered on their own wrapping row below the title bar. A row of
		 *  its own is what keeps a narrow panel from overflowing horizontally. */
		actions?: Snippet;
	} = $props();

	// Tabs read in the order the config is meant to be read, not alphabetically:
	// what the project IS (terraform, variables, locals), what it reads (data),
	// what it manages (main), what it exposes (outputs), then the two transient
	// refactoring files. Anything unrecognised sorts after, alphabetically.
	const ORDER = [
		'terraform.tf',
		'variables.tf',
		'locals.tf',
		'data.tf',
		'main.tf',
		'outputs.tf',
		'imports.tf',
		'moved.tf'
	];
	const rank = (n: string) => {
		const i = ORDER.indexOf(n);
		return i < 0 ? ORDER.length : i;
	};
	const names = $derived(Object.keys(files).sort((a, b) => rank(a) - rank(b) || a.localeCompare(b)));

	// The selected tab is derived from a *wish*, not stored directly: files come
	// and go as the model changes (a project with no outputs has no outputs.tf),
	// and a stored selection would leave the pane blank the moment its file stops
	// being generated.
	let wanted = $state('');
	const active = $derived(names.includes(wanted) ? wanted : (names[0] ?? ''));
	const lines = $derived(highlightHcl(files[active] ?? ''));
</script>

<div class="flex h-full min-h-0 flex-col" style="background:var(--al-code-bg);">
	<div
		class="flex h-11 flex-none items-center justify-between gap-2 border-b px-4"
		style="border-color:var(--al-code-border);"
	>
		<!-- The strip scrolls on its own rather than widening the pane: the three-pane
		     row is overflow-hidden, and a document-level horizontal scrollbar is the
		     one thing this layout must never grow. -->
		<div class="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
			<div class="flex-none" style="width:6px;height:6px;border-radius:50%;background:var(--al-ok);"></div>
			{#if names.length === 0}
				<div class="font-mono" style="font-size:12.5px;font-weight:600;color:var(--al-code-text);">main.tf</div>
			{:else}
				{#each names as name (name)}
					<button
						class="flex-none rounded px-1.5 py-0.5 font-mono"
						style="font-size:12.5px;font-weight:{name === active ? 600 : 400};color:{name === active
							? 'var(--al-code-text)'
							: 'var(--al-code-muted)'};background:{name === active ? 'var(--al-code-border)' : 'transparent'};"
						onclick={() => (wanted = name)}
						title={name}
					>{name}</button>
				{/each}
			{/if}
		</div>
		{#if right}<div class="flex-none">{@render right()}</div>{/if}
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
		{#if names.length > 0}
			{#each lines as line, i (i)}
				<div>{#each line as tok, j (j)}<span style="color:{tok.c};white-space:pre;">{tok.t}</span>{/each}</div>
			{/each}
		{:else}
			<div style="color:var(--al-code-muted);white-space:pre;">{placeholder}</div>
		{/if}
	</div>
</div>
