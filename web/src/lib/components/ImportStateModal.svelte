<!--
  ImportStateModal — paste or upload a terraform.tfstate; both halves (model +
  state) are adopted by doImportState in +page.

  Markup only. Like ImportHclModal the drafts stay in +page and survive Cancel.
  The replace checkbox stays `checked=… onchange=…` — never bind:checked, which
  writes false on mount.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost, ta } from '$lib/styles';
	import { Loader2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		text = $bindable(''),
		replace = $bindable(false),
		envName,
		saving = false,
		onFiles,
		onImport
	}: {
		open?: boolean;
		text?: string;
		replace?: boolean;
		envName: string;
		saving?: boolean;
		onFiles: (list: FileList | null) => void;
		onImport: () => void;
	} = $props();
</script>

<Modal title="Import local state" bind:open>
	<p class="mb-2 text-xs text-[var(--al-muted)]">
		Select a <span class="mono">terraform.tfstate</span> file or paste its JSON. Managed resources become
		editable configuration (computed/sensitive values are dropped so the generated config stays valid),
		<strong class="text-[var(--al-text)]">and the state itself is adopted</strong> as this project's state
		{#if envName}for environment <span class="mono">{envName}</span>{/if} — so a plan diffs against
		what already exists instead of proposing to create it all again.
	</p>
	<input type="file" accept=".tfstate,.json" class="mb-2 text-xs" onchange={(e) => onFiles(e.currentTarget.files)} />
	<textarea class={ta} rows="12" placeholder={'{ "version": 4, "resources": [...] }'} bind:value={text}></textarea>
	<label class="mt-2 flex items-center gap-2 text-xs text-[var(--al-muted)]">
		<input type="checkbox" checked={replace} onchange={() => (replace = !replace)} class="accent-[var(--al-accent)]" />
		Replace the existing state (needed only when this project already tracks resources)
	</label>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onImport} disabled={saving}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Import
		</button>
	{/snippet}
</Modal>
