<script lang="ts">
	// One level of the secrets tree. Recursive: a node can be a folder, a secret,
	// or both (a secret named `global` alongside `global_projectA_varA`).
	import type { SecretNode } from '$lib/secretPath';
	import type { Snippet } from 'svelte';
	import Self from './SecretTreeNode.svelte';
	import { ChevronDown, ChevronRight, KeyRound, Trash2, FolderPlus } from 'lucide-svelte';

	let {
		node,
		depth = 0,
		onDelete,
		onPick,
		value,
		revealWarning
	}: {
		node: SecretNode;
		depth?: number;
		onDelete: (name: string) => void;
		/** Prefill the add form from this node (a folder path, or a leaf to replace). */
		onPick: (name: string, isLeaf: boolean) => void;
		/** The masked/plaintext cell + eye toggle, owned by SecretsModal so both
		 *  views share one reveal flow (and one "what is currently shown" map). */
		value?: Snippet<[string, boolean]>;
		/** The inline confirmation, rendered under the row it belongs to. */
		revealWarning?: Snippet<[string]>;
	} = $props();

	let open = $state(true);
	const isFolder = $derived(node.children.length > 0);
</script>

<div style="padding-left:{depth * 12}px;">
	<div class="group flex items-center gap-1.5 rounded px-1.5 py-1 hover:bg-[var(--al-panel-2)]">
		{#if isFolder}
			<button class="text-[var(--al-muted)] hover:text-[var(--al-text)]" onclick={() => (open = !open)} aria-label={open ? 'Collapse' : 'Expand'}>
				{#if open}<ChevronDown size={13} />{:else}<ChevronRight size={13} />{/if}
			</button>
		{:else}
			<span class="w-[13px]"></span>
		{/if}

		{#if node.secret}
			<KeyRound size={13} class="flex-none text-[var(--al-accent)]" />
			<button
				class="mono min-w-0 flex-1 truncate text-left text-xs text-[var(--al-text)] hover:text-[var(--al-accent)]"
				title={`${node.name} — click to set a new value`}
				onclick={() => onPick(node.name, true)}
			>{node.segment}</button>
			<span class="rounded bg-[var(--al-panel-2)] px-1.5 py-0.5 text-[10px] uppercase text-[var(--al-muted)]">{node.secret.backend}</span>
			{#if value}{@render value(node.name, true)}{:else}<span class="mono text-[10px] text-[var(--al-muted)]">••••••</span>{/if}
			{#if node.secret.backend === 'local'}
				<button class="rounded p-0.5 text-[var(--al-muted)] opacity-0 hover:text-[var(--al-danger)] group-hover:opacity-100" onclick={() => onDelete(node.name)} aria-label="Delete secret"><Trash2 size={13} /></button>
			{/if}
		{:else}
			<button class="mono min-w-0 flex-1 truncate text-left text-xs font-semibold text-[var(--al-text)]" onclick={() => (open = !open)} title={node.name}>{node.segment}</button>
			<span class="text-[10px] text-[var(--al-muted)]">{node.count}</span>
			<button
				class="rounded p-0.5 text-[var(--al-muted)] opacity-0 hover:text-[var(--al-accent)] group-hover:opacity-100"
				title={`Add a secret under ${node.name}`}
				aria-label="Add a secret here"
				onclick={() => onPick(node.name, false)}
			><FolderPlus size={13} /></button>
		{/if}
	</div>

	{#if node.secret && revealWarning}{@render revealWarning(node.name)}{/if}

	{#if isFolder && open}
		{#each node.children as child (child.name)}
			<Self node={child} depth={depth + 1} {onDelete} {onPick} {value} {revealWarning} />
		{/each}
	{/if}
</div>
