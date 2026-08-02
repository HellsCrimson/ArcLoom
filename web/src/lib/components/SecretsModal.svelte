<script lang="ts">
	import type { SecretMeta } from '$lib/types';
	import Modal from './Modal.svelte';
	import SecretTreeNode from './SecretTreeNode.svelte';
	import { buildSecretTree, joinSecretName, PATH_SEP, splitSecretName } from '$lib/secretPath';
	import { Plus, Trash2, Loader2, KeyRound, Lock, List, FolderTree, Eye, EyeOff, AlertTriangle } from 'lucide-svelte';

	let {
		open = $bindable(false),
		secrets = [],
		writable = false,
		backend = '',
		busy = false,
		onAdd,
		onDelete,
		onReveal
	}: {
		open: boolean;
		secrets: SecretMeta[];
		/** Whether a secret can be stored at all (local store or an external one). */
		writable: boolean;
		/** Name of the writable store, shown so it is obvious where values land. */
		backend?: string;
		busy?: boolean;
		onAdd: (name: string, value: string) => void;
		onDelete: (name: string) => void;
		/** Fetch one secret's plaintext value. Absent = the viewer may not (the
		 *  server enforces it too); the eye button is then not rendered at all. */
		onReveal?: (name: string) => Promise<string>;
	} = $props();

	// ── Revealing a value ────────────────────────────────────────────────────
	// Hidden by default, one name at a time, and never without an explicit
	// confirmation: the click is recorded server-side as `secret.reveal`, and the
	// value lands on a screen someone may be sharing. The warning is inline
	// rather than a window.confirm for the same reason the Git panel's is — it
	// can say what actually happens, in the row it happens to.
	let revealed = $state<Record<string, string>>({});
	let confirming = $state('');
	let revealing = $state('');
	let revealError = $state('');

	// Closing the modal forgets everything shown. Without this, re-opening it
	// would put the plaintext straight back on screen with no second thought.
	$effect(() => {
		if (!open) {
			revealed = {};
			confirming = '';
			revealError = '';
		}
	});

	function askReveal(name: string) {
		revealError = '';
		confirming = confirming === name ? '' : name;
	}
	async function doReveal(name: string) {
		if (!onReveal) return;
		revealing = name;
		revealError = '';
		try {
			revealed = { ...revealed, [name]: await onReveal(name) };
			confirming = '';
		} catch (err) {
			revealError = (err as Error).message;
		} finally {
			revealing = '';
		}
	}
	function hideValue(name: string) {
		const next = { ...revealed };
		delete next[name];
		revealed = next;
	}

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

<!--
  value — the masked/plaintext cell shared by both views, plus the eye toggle.
  A revealed secret renders `select-all` so it can be copied by clicking it: no
  clipboard button, because navigator.clipboard is undefined on a plain-HTTP
  origin, which is exactly how a homelab reaches this app.
-->
{#snippet value(name: string, small = false)}
	{#if revealed[name] !== undefined}
		<span class="mono max-w-[22rem] select-all truncate {small ? 'text-[10px]' : 'text-xs'} text-[var(--al-text)]" title={revealed[name]}>
			{revealed[name] === '' ? '(empty)' : revealed[name]}
		</span>
		<button class="rounded p-0.5 text-[var(--al-accent)] hover:brightness-125" onclick={() => hideValue(name)} aria-label="Hide value" title="Hide again"><EyeOff size={13} /></button>
	{:else}
		<span class="mono {small ? 'text-[10px]' : 'text-xs'} text-[var(--al-muted)]">••••••</span>
		{#if onReveal}
			<button
				class="rounded p-0.5 text-[var(--al-muted)] hover:text-[var(--al-accent)]"
				onclick={() => askReveal(name)}
				aria-label="Reveal value"
				title="Show this value in plain text"
			>{#if revealing === name}<Loader2 size={13} class="animate-spin" />{:else}<Eye size={13} />{/if}</button>
		{/if}
	{/if}
{/snippet}

{#snippet revealWarning(name: string)}
	{#if confirming === name}
		<div class="mt-1 rounded-md border px-2.5 py-2 text-[11px]" style="border-color:var(--al-warn);background:var(--al-panel-2);">
			<p class="flex items-start gap-1.5 text-[var(--al-text)]">
				<AlertTriangle size={13} class="mt-px flex-none text-[var(--al-warn)]" />
				<span>
					Show the value of <span class="mono">{name}</span> in plain text? It appears on screen until you hide it
					or close this dialog, and the access is written to the <strong>audit log</strong> as
					<span class="mono">secret.reveal</span> with your username.
				</span>
			</p>
			<div class="mt-1.5 flex items-center gap-2">
				<button
					class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[var(--al-text)] hover:bg-[var(--al-panel)]"
					style="border-color:var(--al-warn);"
					onclick={() => doReveal(name)}
					disabled={revealing === name}
				>{#if revealing === name}<Loader2 size={12} class="animate-spin" />{:else}<Eye size={12} />{/if} Show value</button>
				<button class="text-[var(--al-muted)] hover:text-[var(--al-text)]" onclick={() => (confirming = '')}>Cancel</button>
			</div>
			{#if revealError}
				<p class="mt-1 text-[var(--al-danger)]">{revealError}</p>
			{/if}
		</div>
	{/if}
{/snippet}

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

	{#if !writable}
		<div class="mb-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-xs text-[var(--al-muted)]">
			No writable secret store. Set <span class="mono">ARCLOOM_SECRET_KEY</span> for local encrypted
			storage, configure an external store in Settings → Secret store, or provide values via
			<span class="mono">ARCLOOM_SECRET_&lt;name&gt;</span> environment variables.
		</div>
	{:else if backend && backend !== 'local'}
		<div class="mb-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-xs text-[var(--al-muted)]">
			Secrets are stored in <span class="mono">{backend}</span>, not on this host.
		</div>
	{/if}

	<!-- existing secrets -->
	{#if secrets.length === 0}
		<p class="mb-4 text-xs text-[var(--al-muted)]">No secrets yet.</p>
	{:else if view === 'list'}
		<div class="mb-4 space-y-1">
			{#each secrets as s (s.name + s.backend)}
				<div>
					<div class="flex items-center gap-2 rounded-md border border-[var(--al-border)] px-2.5 py-1.5">
						<KeyRound size={14} class="text-[var(--al-accent)]" />
						<button class="mono min-w-0 flex-1 truncate text-left text-sm text-[var(--al-text)] hover:text-[var(--al-accent)]" title="Set a new value" onclick={() => prefill(s.name, true)}>{s.name}</button>
						<span class="rounded bg-[var(--al-panel-2)] px-1.5 py-0.5 text-[10px] uppercase text-[var(--al-muted)]">{s.backend}</span>
						{@render value(s.name)}
						{#if s.backend !== 'env'}
							<button class="rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDelete(s.name)} aria-label="Delete secret"><Trash2 size={14} /></button>
						{/if}
					</div>
					{@render revealWarning(s.name)}
				</div>
			{/each}
		</div>
	{:else}
		<div class="mb-4 max-h-72 overflow-y-auto rounded-md border border-[var(--al-border)] p-1">
			{#each tree as node (node.name)}
				<SecretTreeNode {node} depth={0} {onDelete} onPick={prefill} {value} {revealWarning} />
			{/each}
		</div>
	{/if}

	<!-- add / overwrite -->
	{#if writable}
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
