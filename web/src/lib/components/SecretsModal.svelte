<script lang="ts">
	import type { SecretMeta } from '$lib/types';
	import Modal from './Modal.svelte';
	import SecretTreeNode from './SecretTreeNode.svelte';
	import { buildSecretTree, joinSecretName, PATH_SEP, splitSecretName } from '$lib/secretPath';
	import { Plus, Trash2, Loader2, KeyRound, Lock, List, FolderTree } from 'lucide-svelte';

	let {
		open = $bindable(false),
		secrets = [],
		localEnabled = false,
		busy = false,
		onAdd,
		onDelete
	}: {
		open: boolean;
		secrets: SecretMeta[];
		localEnabled: boolean;
		busy?: boolean;
		onAdd: (name: string, value: string) => void;
		onDelete: (name: string) => void;
	} = $props();

	const VIEW_KEY = 'arcloom.secretsView';

	// The tree is a second view of the SAME secrets: segments come from the `_`
	// in each name, and adding through it just composes the name back.
	let view = $state<'list' | 'tree'>('list');
	$effect(() => {
		const saved = localStorage.getItem(VIEW_KEY);
		if (saved === 'tree' || saved === 'list') view = saved;
	});
	function setView(v: 'list' | 'tree') {
		view = v;
		localStorage.setItem(VIEW_KEY, v);
	}

	let newPath = $state(''); // "global/projectA" — only used by the tree view
	let newName = $state('');
	let newValue = $state('');

	const tree = $derived(buildSecretTree(secrets));
	// The name that will actually be stored, referenced and injected.
	const composedName = $derived(
		view === 'tree' ? joinSecretName([newPath, newName]) : joinSecretName([newName])
	);
	const existing = $derived(secrets.some((s) => s.name === composedName));

	function add() {
		if (!composedName || !newValue) return;
		onAdd(composedName, newValue);
		newName = '';
		newValue = '';
	}

	// Prefills the form from a folder (its path) or a leaf (path + name), so
	// setting a new value for an existing secret is a click, not retyping.
	function prefill(name: string, isLeaf: boolean) {
		const segments = splitSecretName(name);
		if (isLeaf) {
			newName = segments.pop() ?? '';
		} else {
			newName = '';
		}
		newPath = segments.join(PATH_SEP);
		if (view === 'list' && isLeaf) newName = name;
	}

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const viewBtn = (active: boolean) =>
		`inline-flex items-center gap-1 px-2 py-0.5 text-[11px] ${
			active ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'
		}`;
</script>

<Modal title="Secrets" bind:open>
	<div class="mb-3 flex items-start gap-3">
		<p class="flex flex-1 items-center gap-1.5 text-xs text-[var(--al-muted)]">
			<Lock size={12} /> Values are encrypted at rest and injected as <span class="mono">TF_VAR_*</span> at plan/apply — never written to HCL, state, or Git.
		</p>
		<div class="flex flex-none overflow-hidden rounded-md border border-[var(--al-border)]">
			<button class={viewBtn(view === 'list')} onclick={() => setView('list')}><List size={12} /> List</button>
			<button class={viewBtn(view === 'tree')} onclick={() => setView('tree')}><FolderTree size={12} /> Tree</button>
		</div>
	</div>

	{#if !localEnabled}
		<div class="mb-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-xs text-[var(--al-muted)]">
			Local encrypted storage is disabled. Set <span class="mono">ARCLOOM_SECRET_KEY</span> to store secrets, or provide them via <span class="mono">ARCLOOM_SECRET_&lt;name&gt;</span> environment variables.
		</div>
	{/if}

	<!-- existing secrets -->
	{#if secrets.length === 0}
		<p class="mb-4 text-xs text-[var(--al-muted)]">No secrets yet.</p>
	{:else if view === 'list'}
		<div class="mb-4 space-y-1">
			{#each secrets as s (s.name + s.backend)}
				<div class="flex items-center gap-2 rounded-md border border-[var(--al-border)] px-2.5 py-1.5">
					<KeyRound size={14} class="text-[var(--al-accent)]" />
					<button class="mono flex-1 text-left text-sm text-[var(--al-text)] hover:text-[var(--al-accent)]" title="Set a new value" onclick={() => prefill(s.name, true)}>{s.name}</button>
					<span class="rounded bg-[var(--al-panel-2)] px-1.5 py-0.5 text-[10px] uppercase text-[var(--al-muted)]">{s.backend}</span>
					<span class="mono text-xs text-[var(--al-muted)]">••••••</span>
					{#if s.backend === 'local'}
						<button class="rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDelete(s.name)} aria-label="Delete secret"><Trash2 size={14} /></button>
					{/if}
				</div>
			{/each}
		</div>
	{:else}
		<div class="mb-4 max-h-72 overflow-y-auto rounded-md border border-[var(--al-border)] p-1">
			{#each tree as node (node.name)}
				<SecretTreeNode {node} depth={0} {onDelete} onPick={prefill} />
			{/each}
		</div>
	{/if}

	<!-- add / overwrite -->
	{#if localEnabled}
		<div class="border-t border-[var(--al-border)] pt-3">
			<div class="flex items-end gap-2">
				{#if view === 'tree'}
					<div class="flex-1">
						<label for="sec-path" class="mb-1 block text-[10px] uppercase tracking-wide text-[var(--al-muted)]">category path</label>
						<input id="sec-path" class="mono w-full {inp}" placeholder="global/projectA" bind:value={newPath} />
					</div>
				{/if}
				<div class="flex-1">
					<label for="sec-name" class="mb-1 block text-[10px] uppercase tracking-wide text-[var(--al-muted)]">name</label>
					<input id="sec-name" class="mono w-full {inp}" placeholder={view === 'tree' ? 'varA' : 'db_password'} bind:value={newName} />
				</div>
				<div class="flex-1">
					<label for="sec-val" class="mb-1 block text-[10px] uppercase tracking-wide text-[var(--al-muted)]">value</label>
					<input id="sec-val" type="password" class="w-full {inp}" placeholder="secret value" bind:value={newValue} onkeydown={(e) => { if (e.key === 'Enter') add(); }} />
				</div>
				<button class="inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40" onclick={add} disabled={busy || !composedName || !newValue}>
					{#if busy}<Loader2 size={14} class="animate-spin" />{:else}<Plus size={14} />{/if} {existing ? 'Replace' : 'Add'}
				</button>
			</div>
			{#if view === 'tree'}
				<p class="mt-1.5 text-[11px] text-[var(--al-muted)]">
					{#if composedName}
						Stored as <span class="mono text-[var(--al-text)]">{composedName}</span> — that is the name you pick in a resource field, and it becomes
						<span class="mono">TF_VAR_{composedName}</span>.{#if existing} <span class="text-[var(--al-warn,#d29922)]">A secret with this name exists; saving replaces its value.</span>{/if}
					{:else}
						Categories are just <span class="mono">_</span>-separated segments of the name: <span class="mono">global/projectA</span> + <span class="mono">varA</span> is stored as <span class="mono">global_projectA_varA</span>.
					{/if}
				</p>
			{/if}
		</div>
	{/if}
</Modal>
