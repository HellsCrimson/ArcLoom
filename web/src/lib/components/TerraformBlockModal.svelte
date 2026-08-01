<!--
  TerraformBlockModal — the `terraform { required_version + backend }` dialog.

  Markup only: openBackend/backendSig/bkOpenedSig/saveBackend all stay in +page.
  `bkOpenedSig = backendSig()` is a POINT-IN-TIME snapshot taken at open and
  compared once at save — turning it into a $derived would make backendChanged
  permanently false and the backend would silently stop re-initialising. The
  drafts stay in +page too (threaded with $bindable), so a re-entrant open
  (Ctrl+K → "Terraform block" over the open modal) re-seeds exactly as before.
-->
<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { BACKENDS, backendField, backendFields, backendKind } from '$lib/model/backends';
	import { Loader2, Trash2 } from 'lucide-svelte';

	let {
		open = $bindable(false),
		requiredVersion = $bindable(''),
		type = $bindable(''),
		rows = $bindable<{ key: string; expr: string }[]>([]),
		mode = $bindable<'migrate' | 'reconfigure'>('migrate'),
		canDeploy = false,
		saving = false,
		onSave
	}: {
		open?: boolean;
		requiredVersion?: string;
		type?: string;
		rows?: { key: string; expr: string }[];
		mode?: 'migrate' | 'reconfigure';
		canDeploy?: boolean;
		saving?: boolean;
		onSave: () => void;
	} = $props();

	// Known keys for the selected type — advisory only (the rows stay free-form),
	// so an unlisted key still saves and an unknown type just renders no help.
	const kind = $derived(backendKind(type));
	const fields = $derived(backendFields(type));
	const used = $derived(new Set(rows.map((r) => r.key.trim()).filter(Boolean)));
	const missing = $derived(fields.filter((f) => !used.has(f.key)));
	let showAllKeys = $state(false);

	// Seeding happens in the type input's handler, NEVER in an $effect: the modal
	// is re-entrant (Ctrl+K over an open modal re-opens it) and an effect keyed on
	// `type` would fire on every open, wiping a config the user came back to edit.
	function typeChanged() {
		if (rows.some((r) => r.key.trim() || r.expr.trim())) return; // never clobber real values
		rows = backendFields(type)
			.filter((f) => f.required)
			.map((f) => ({ key: f.key, expr: '' }));
		showAllKeys = false;
	}

	function addKey(key: string) {
		const blank = rows.findIndex((r) => !r.key.trim() && !r.expr.trim());
		if (blank >= 0) rows[blank].key = key;
		else rows = [...rows, { key, expr: '' }];
	}
</script>

<Modal title="Terraform block" bind:open>
	<div class="space-y-3">
		<div>
			<label for="bk-required-version" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Required version</label>
			<input
				id="bk-required-version"
				class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
				placeholder={'>= 1.6.0  ·  ~> 1.12  ·  empty = unconstrained'}
				bind:value={requiredVersion}
			/>
			<p class="mt-1 text-xs text-[var(--al-muted)]">
				Emitted as <span class="mono">required_version</span> in the terraform block — the engine
				refuses to run outside the constraint, and tflint's
				<span class="mono">terraform_required_version</span> rule expects it.
			</p>
		</div>
		<div class="border-t border-[var(--al-border)] pt-3">
			<span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">State backend</span>
		</div>
		<p class="text-xs text-[var(--al-muted)]">
			Where this project's state lives. Empty type = local state in the project workspace.
			Values are raw HCL literals (no interpolation allowed by Terraform); put credentials in the
			server's environment, never here.
		</p>
		<div>
			<label for="bk-type" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Backend type</label>
			<input id="bk-type" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="s3 · gcs · http · pg · … (empty = local)" bind:value={type} onchange={typeChanged} list="bk-types" />
			<datalist id="bk-types">
				{#each BACKENDS as b (b.type)}<option value={b.type}>{b.label}</option>{/each}
			</datalist>
			{#if kind}
				<p class="mt-1 text-xs text-[var(--al-muted)]">{kind.label}</p>
			{/if}
			{#if kind?.note}
				<p class="mt-1 rounded-md border border-[var(--al-warn)] px-2 py-1 text-xs text-[var(--al-muted)]">{kind.note}</p>
			{/if}
		</div>
		<div>
			<div class="mb-1 flex items-center justify-between">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Configuration</span>
				<button class="text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => (rows = [...rows, { key: '', expr: '' }])}>+ add value</button>
			</div>
			{#if rows.length === 0}
				<p class="text-xs text-[var(--al-muted)]">No configuration values.</p>
			{/if}
			<div class="space-y-1.5">
				{#each rows as row, i (i)}
					{@const f = backendField(type, row.key)}
					<div>
						<div class="flex items-center gap-2">
							<input class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" aria-label="Configuration key" placeholder={fields[0]?.key ?? 'key'} bind:value={row.key} list={fields.length ? 'bk-keys' : undefined} />
							<input class="mono min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" aria-label="Configuration value" placeholder={f?.example ?? '"a string" · true · 5432'} bind:value={row.expr} />
							<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => (rows = rows.filter((_, j) => j !== i))} aria-label="Remove value"><Trash2 size={12} /></button>
						</div>
						{#if f}
							<p class="ml-[10.5rem] mt-0.5 text-[11px] text-[var(--al-muted)]">{f.hint}{#if f.required}<span class="text-[var(--al-warn)]">&nbsp;· required</span>{/if}</p>
						{/if}
					</div>
				{/each}
			</div>
			{#if fields.length}
				<datalist id="bk-keys">
					{#each fields as f (f.key)}<option value={f.key}>{f.hint}</option>{/each}
				</datalist>
				{#if missing.length}
					<div class="mt-2 flex flex-wrap items-center gap-1.5">
						<span class="text-[11px] uppercase tracking-wide text-[var(--al-muted)]">Add</span>
						{#each showAllKeys ? missing : missing.filter((f) => f.required) as f (f.key)}
							<button class="mono rounded-md border border-[var(--al-border)] px-1.5 py-0.5 text-[11px] text-[var(--al-muted)] hover:border-[var(--al-accent)] hover:text-[var(--al-text)]" title={f.hint} onclick={() => addKey(f.key)}>+ {f.key}</button>
						{/each}
						{#if !showAllKeys && missing.some((f) => !f.required)}
							<button class="text-[11px] text-[var(--al-muted)] underline hover:text-[var(--al-accent)]" onclick={() => (showAllKeys = true)}>show all {missing.length} keys</button>
						{:else if showAllKeys}
							<button class="text-[11px] text-[var(--al-muted)] underline hover:text-[var(--al-accent)]" onclick={() => (showAllKeys = false)}>show less</button>
						{/if}
					</div>
				{/if}
			{/if}
		</div>
		{#if canDeploy}
			<div>
				<span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">On save</span>
				<label class="flex items-start gap-2 text-sm text-[var(--al-text)]">
					<input type="radio" class="mt-1" name="bk-mode" value="migrate" checked={mode === 'migrate'} onchange={() => (mode = 'migrate')} />
					<span>Migrate existing state<span class="block text-xs text-[var(--al-muted)]">Copies the current state into the new backend (init -migrate-state).</span></span>
				</label>
				<label class="mt-1 flex items-start gap-2 text-sm text-[var(--al-text)]">
					<input type="radio" class="mt-1" name="bk-mode" value="reconfigure" checked={mode === 'reconfigure'} onchange={() => (mode = 'reconfigure')} />
					<span>Reconfigure only<span class="block text-xs text-[var(--al-muted)]">Uses whatever the new backend already holds; nothing is copied.</span></span>
				</label>
			</div>
		{:else}
			<p class="text-xs text-[var(--al-muted)]">You lack the deploy permission — the backend is saved but the workspace is re-initialized on the next deploy-user operation.</p>
		{/if}
	</div>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (open = false)}>Cancel</button>
		<button class={btnPrimary} onclick={onSave} disabled={saving}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	{/snippet}
</Modal>
