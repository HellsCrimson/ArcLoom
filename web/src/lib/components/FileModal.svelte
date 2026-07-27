<!--
  FileModal — add / edit a companion file (a cloud-init template, script or
  JSON document that HCL reaches with file() / templatefile()).

  Markup only. Like RegistryModuleModal, the drafts live in +page and are
  threaded with $bindable: openFile() re-seeds them unconditionally and is
  reachable from the sidebar AND the command palette, and Ctrl+K is handled
  before the editable-target guard — so a seed-on-open $effect here would be a
  no-op on the second open and leave one file's content under another's path.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost, ta } from '$lib/styles';
	import { Loader2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		editing,
		path = $bindable(''),
		content = $bindable(''),
		saving = false,
		onSave
	}: {
		open?: boolean;
		/** Original path when editing an existing file; null when adding. */
		editing: string | null;
		path?: string;
		content?: string;
		saving?: boolean;
		onSave: () => void;
	} = $props();

	// Mirrors model.ValidateFilePath so the common mistakes are caught before a
	// round trip. The server is still the authority — this only explains.
	const pathError = $derived.by(() => {
		const p = path.trim();
		if (!p) return '';
		if (p.startsWith('/')) return 'Must be a relative path (no leading “/”).';
		if (p.includes('\\')) return 'Use “/” as the separator.';
		if (p.split('/').includes('..')) return 'Must not contain “..”.';
		if (/\.tf(\.json)?$/i.test(p))
			return 'HCL is generated from the model — use Import HCL for .tf files.';
		return '';
	});

	// The project .gitignore (internal/git) excludes state, tfvars and .env so
	// secrets never get committed. A companion file matching one of those still
	// works at plan time but would NOT be committed — surfaced as a warning
	// rather than a block, because keeping such a file out of Git is often the
	// point.
	const gitNote = $derived.by(() => {
		const p = path.trim().toLowerCase();
		if (!p || pathError) return '';
		const base = p.split('/').pop() ?? '';
		const ignored =
			base.endsWith('.tfvars') ||
			base.endsWith('.tfstate') ||
			base.includes('.tfstate.') ||
			base === '.env' ||
			base === 'secrets.enc';
		return ignored ? 'Heads up: this path is in the project’s .gitignore, so it will not be committed.' : '';
	});

	// path.module makes the reference explicit: a file at modules/<m>/x is
	// resolved relative to THAT module, which is the part people get wrong.
	const reference = $derived.by(() => {
		const p = path.trim();
		if (!p || pathError) return '';
		const m = p.match(/^modules\/[^/]+\/(.*)$/);
		return `\${path.module}/${m ? m[1] : p}`;
	});
</script>

<Modal title={editing ? `File “${editing}”` : 'Add file'} bind:open>
	<div class="space-y-3">
		<div>
			<label for="file-path" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Path</label>
			<input
				id="file-path"
				class="mono w-full rounded-md border bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] {pathError ? 'border-[var(--al-danger)]' : 'border-[var(--al-border)]'}"
				placeholder="templates/cloud-init.tftpl"
				bind:value={path}
			/>
			{#if pathError}
				<p class="mt-1 text-xs text-[var(--al-danger)]">{pathError}</p>
			{:else if reference}
				<p class="mt-1 text-xs text-[var(--al-muted)]">
					Reference it as <span class="mono text-[var(--al-text)]">templatefile("{reference}", {'{ … }'})</span>
				</p>
			{:else}
				<p class="mt-1 text-xs text-[var(--al-muted)]">
					Relative to the root module. Put it under <span class="mono">modules/&lt;name&gt;/</span> to use it inside a local module.
				</p>
			{/if}
			{#if gitNote}
				<p class="mt-1 text-xs" style="color:#e3b341;">{gitNote}</p>
			{/if}
		</div>
		<div>
			<label for="file-content" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Content</label>
			<textarea id="file-content" class="{ta} mono" rows="16" placeholder={'#cloud-config\nhostname: ${name}'} bind:value={content}></textarea>
		</div>
	</div>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onSave} disabled={saving || !path.trim() || !!pathError}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} {editing ? 'Save' : 'Add'} file
		</button>
	{/snippet}
</Modal>
