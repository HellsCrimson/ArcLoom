<script lang="ts">
	// ResourceFormPane is the centre pane: the schema form for the block currently
	// being edited (a managed resource, a data source or a provider config), plus
	// the plan banner and the meta-arg inputs above it.
	//
	// It reads the `form` store directly (that store IS this pane's model — it is
	// seeded from four places while this branch is unmounted by the canvas toggle)
	// and `runPane` for the plan overlay. Permission arrives as data
	// (canResources/canDeploy), never by importing the auth store.
	import type { AttrDiff } from '$lib/api/client';
	import type { ProviderInstance } from '$lib/types';
	import SchemaForm from '$lib/form/SchemaForm.svelte';
	import PlanDiffPanel from '$lib/components/PlanDiffPanel.svelte';
	import { stripIndex } from '$lib/model/scope';
	import { btnPrimary, btnGhost } from '$lib/styles';
	import { form } from '$lib/stores/resourceForm.svelte';
	import { runPane } from '$lib/stores/runPane.svelte';
	import { Save, Loader2, Plus, HardDriveDownload } from 'lucide-svelte';

	let {
		hasProject,
		loadingSchema,
		saving,
		anyBusy,
		activeModule,
		references,
		secrets,
		onCreateSecret,
		providers,
		canResources,
		canDeploy,
		onSaveResource,
		onSaveProvider,
		onImportResource,
		onNewProject
	}: {
		hasProject: boolean;
		loadingSchema: boolean;
		saving: boolean;
		anyBusy: boolean;
		activeModule: string | null;
		references: string[];
		secrets: string[];
		/** Store a new secret and refresh the list; absent = creating is unavailable. */
		onCreateSecret?: (name: string, value: string) => Promise<void>;
		providers: ProviderInstance[];
		canResources: boolean;
		canDeploy: boolean;
		onSaveResource: () => void;
		onSaveProvider: () => void;
		onImportResource: () => void;
		onNewProject: () => void;
	} = $props();

	// Instances of the provider the form's resource type belongs to; a provider
	// picker is shown when the source has more than one instance (aliases).
	const formProviderInstances = $derived(
		providers.filter((p) => p.key === form.selectedType.split('_')[0])
	);

	// Address of the resource currently in the form, for `tofu import`
	// (managed resources only — data sources are read, never imported).
	const importAddress = $derived(
		`${activeModule ? `module.${activeModule}.` : ''}${form.selectedType}.${form.name}`
	);

	// --- Plan overlay in the form: which attributes the last plan touches. ---
	// The canvas and the run pane point AT a resource; the form is where the user
	// then looks for what actually changes, so the plan's per-attribute diff is
	// surfaced here instead of only in the streamed log.
	// Full address of the open form, matching plan addresses (data sources carry
	// the "data." prefix there).
	const formAddress = $derived(
		`${activeModule ? `module.${activeModule}.` : ''}${form.kind === 'data' ? 'data.' : ''}${form.selectedType}.${form.name}`
	);
	// Instances of the open resource with a pending action. A counted resource
	// plans per instance ("x.y[0]"), so several changes can match one form.
	const formPlanChanges = $derived(
		form.formKind === 'resource' && form.editingId && runPane.dismissedAddress !== formAddress
			? runPane.changes.filter((c) => stripIndex(c.address) === formAddress)
			: []
	);
	// Flattened for the per-field markers, deduped by path across instances (they
	// usually change identically; a path forcing replacement wins).
	const formPlanDiffs = $derived.by(() => {
		const byPath = new Map<string, AttrDiff>();
		for (const c of formPlanChanges) {
			for (const d of c.diffs ?? []) {
				const prev = byPath.get(d.path);
				if (!prev || (d.forcesReplace && !prev.forcesReplace)) byPath.set(d.path, d);
			}
		}
		return [...byPath.values()];
	});
</script>

<main class="min-w-0 flex-1 overflow-y-auto p-5">
	{#if !hasProject}
		<div class="grid h-full place-items-center text-sm text-[var(--al-muted)]">
			<button class={btnPrimary} onclick={onNewProject}><Plus size={15} /> New project</button>
		</div>
	{:else if loadingSchema}
		<div class="flex items-center gap-2 text-sm text-[var(--al-muted)]"><Loader2 size={16} class="animate-spin" /> Loading schema…</div>
	{:else if form.schema && form.formKind === 'provider'}
		<div class="mb-4 flex items-end gap-3">
			<div class="mono text-sm text-[var(--al-muted)]">provider "<span class="text-[var(--al-accent)]">{form.providerKey}</span>"{#if form.providerAlias && !form.providerAliasNew}<span> alias "<span class="text-[var(--al-accent)]">{form.providerAlias}</span>"</span>{/if}</div>
			{#if form.providerAliasNew}
				<div>
					<label for="provider-alias" class="mb-1 block text-xs text-[var(--al-muted)]" title="alias meta-argument — resources select this instance via provider = {form.providerKey}.<alias>">alias</label>
					<input id="provider-alias" class="mono w-36 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={form.providerAlias} placeholder="euwest" />
				</div>
				<span class="pb-1.5 text-[10px] uppercase text-[var(--al-muted)]">new instance</span>
			{/if}
			{#if canResources}<button class={btnPrimary + ' ml-auto'} onclick={onSaveProvider} disabled={saving || (form.providerAliasNew && !form.providerAlias.trim())}><Save size={15} /> Save provider config</button>{/if}
		</div>
		<!--
		  required_providers, edited where the data lives. ArcLoom keeps source and
		  version on each ProviderInstance and hclgen assembles the terraform
		  block's required_providers from them, so this IS that block's entry —
		  there is no separate place to edit it.
		-->
		<div class="mb-4 flex flex-wrap items-end gap-3 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2">
			<span class="pb-1.5 text-[10px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">required_providers</span>
			<div>
				<label for="provider-src" class="mb-1 block text-xs text-[var(--al-muted)]" title="registry address — [host/][namespace/]type">source</label>
				<input id="provider-src" class="mono w-56 rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={form.providerFormSource} placeholder="hashicorp/null" disabled={!canResources} />
			</div>
			<div>
				<label for="provider-ver" class="mb-1 block text-xs text-[var(--al-muted)]" title="version constraint, e.g. 3.2.1 or ~> 3.2 — empty means latest">version</label>
				<input id="provider-ver" class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={form.providerFormVersion} placeholder="latest (unpinned)" disabled={!canResources} />
			</div>
			{#if form.providerFormVersion.trim() && canResources}
				<button
					class="pb-2 text-xs text-[var(--al-muted)] underline hover:text-[var(--al-accent)]"
					onclick={() => (form.providerFormVersion = '')}
					title="Remove the version constraint so the newest release is used"
				>unpin</button>
			{/if}
			<span class="pb-1.5 text-[11px] text-[var(--al-muted)]">Saved with the config. One <span class="mono">required_providers</span> entry is emitted per provider key.</span>
		</div>
		{#if (form.schema.fields?.length ?? 0) === 0}
			<p class="text-sm text-[var(--al-muted)]">This provider has no configurable arguments.</p>
		{:else}
			<SchemaForm
				schema={form.schema}
				bind:values={form.values}
				{references}
				{secrets}
				{onCreateSecret}
			/>
		{/if}
	{:else if form.schema}
		<div class="mb-4 flex items-end gap-3">
			<div>
				<label for="resource-name" class="mb-1 block text-xs text-[var(--al-muted)]">resource name</label>
				<input id="resource-name" class="mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={form.name} />
			</div>
			<div>
				<label for="resource-count" class="mb-1 block text-xs text-[var(--al-muted)]" title="count meta-argument — an HCL expression, e.g. 3 or var.replicas">count</label>
				<input id="resource-count" class="mono w-28 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" bind:value={form.count} placeholder="3 · var.n" disabled={form.forEach.trim() !== ''} />
			</div>
			<div>
				<label for="resource-foreach" class="mb-1 block text-xs text-[var(--al-muted)]" title="for_each meta-argument — a set or map expression, e.g. toset([&quot;a&quot;, &quot;b&quot;])">for_each</label>
				<input id="resource-foreach" class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" bind:value={form.forEach} placeholder="toset([…]) · var.map" disabled={form.count.trim() !== ''} />
			</div>
			{#if formProviderInstances.length > 1}
				<div>
					<label for="resource-provider" class="mb-1 block text-xs text-[var(--al-muted)]" title="provider meta-argument — which instance of the provider manages this {form.kind === 'data' ? 'data source' : 'resource'}">provider</label>
					<select id="resource-provider" class="mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={form.provider}>
						{#each formProviderInstances as pi (pi.alias ?? '')}
							<option value={pi.alias ? `${pi.key}.${pi.alias}` : ''}>{pi.alias ? `${pi.key}.${pi.alias}` : `${pi.key} (default)`}</option>
						{/each}
					</select>
				</div>
			{/if}
			<div class="mono text-sm text-[var(--al-muted)]">{form.kind === 'data' ? 'data' : 'resource'} "<span class="text-[var(--al-accent)]">{form.selectedType}</span>" "{form.name}"{#if form.editingId}<span class="ml-1 text-[10px] uppercase">editing</span>{/if}</div>
			{#if canResources}<button class={btnPrimary + ' ml-auto'} onclick={onSaveResource} disabled={saving}><Save size={15} /> {form.editingId ? 'Update' : 'Save'} {form.kind === 'data' ? 'data source' : 'resource'}</button>{/if}
			{#if form.editingId && form.kind === 'resource' && canDeploy}
				<button class={btnGhost} title="Adopt an existing real-world resource into state ({importAddress})" onclick={onImportResource} disabled={anyBusy}>
					<HardDriveDownload size={15} /> Import
				</button>
			{/if}
		</div>
		{#if formPlanChanges.length > 0}
			<PlanDiffPanel
				changes={formPlanChanges}
				onDismiss={() => (runPane.dismissedAddress = formAddress)}
			/>
		{/if}
		<SchemaForm
				schema={form.schema}
				bind:values={form.values}
				{references}
				{secrets}
				{onCreateSecret}
				planDiffs={formPlanDiffs}
			/>
	{:else}
		<div class="grid h-full place-items-center text-center text-sm text-[var(--al-muted)]">
			Add a provider and pick a resource type, select an existing resource to edit,<br />or import existing HCL / state.
		</div>
	{/if}
</main>
