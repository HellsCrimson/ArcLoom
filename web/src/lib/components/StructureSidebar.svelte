<script lang="ts">
	// Structure sidebar: provider source input + configured providers (with update
	// badges and alias management), the resource/data-source type palette, and the
	// structure tree (root scope, local modules, registry module calls).
	// Reads the resource-form store directly for the type/edit highlight states;
	// everything else arrives as data + callbacks (permission is a boolean, never
	// an auth import).
	import type { LocalModule, Module, Project, ProviderInstance, ProviderUpdate, Resource, ResourceKind } from '$lib/types';
	import { btnGhost, iconBtn } from '$lib/styles';
	import { form } from '$lib/stores/resourceForm.svelte';
	import { Loader2, Plus, Trash2, FolderGit2, Folder, Settings, Package, ArrowUp } from 'lucide-svelte';

	let {
		width,
		project,
		providers,
		providerUpdates,
		bumping,
		localModules,
		registryModules,
		activeModule,
		addingProvider = false,
		canResources = false,
		onAddProvider,
		onBumpProvider,
		onAddAlias,
		onDeleteInstance,
		onConfigureProvider,
		onPickType,
		onSelectScope,
		onEditResource,
		onDeleteResource,
		onNewModule,
		onDeleteModule,
		onOpenInputs,
		onOpenRegistryModule,
		onDeleteRegistryModule
	}: {
		width: number;
		project: Project | null;
		providers: ProviderInstance[];
		providerUpdates: Record<string, ProviderUpdate>;
		bumping: string;
		localModules: LocalModule[];
		registryModules: Module[];
		activeModule: string | null;
		addingProvider?: boolean;
		canResources?: boolean;
		onAddProvider: () => void;
		onBumpProvider: (p: ProviderInstance, latest: string) => void;
		onAddAlias: (p: ProviderInstance) => void;
		onDeleteInstance: (p: ProviderInstance) => void;
		onConfigureProvider: (p: ProviderInstance) => void;
		onPickType: (t: string, kind?: ResourceKind) => void;
		onSelectScope: (mod: string | null) => void;
		onEditResource: (r: Resource, kind?: ResourceKind) => void;
		onDeleteResource: (id: string, kind?: ResourceKind) => void;
		onNewModule: () => void;
		onDeleteModule: (name: string) => void;
		onOpenInputs: () => void;
		onOpenRegistryModule: (m?: Module) => void;
		onDeleteRegistryModule: (name: string) => void;
	} = $props();
</script>

<aside
	class="shrink-0 space-y-3 overflow-y-auto border-r border-[var(--al-border)] bg-[var(--al-panel)] p-3"
	style="width:{width}px;"
>
	{#if project}
		<div>
			<label for="provider-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Provider</label>
			<div class="flex gap-1.5">
				<input id="provider-source" class="min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={form.providerSource} placeholder="" />
				<button class={btnGhost} onclick={onAddProvider} disabled={addingProvider}>
					{#if addingProvider}<Loader2 size={14} class="animate-spin" />{:else}Add{/if}
				</button>
			</div>
		</div>

		{#if providers.length > 0}
			<div>
				<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Configured providers</div>
				<ul class="space-y-0.5">
					{#each providers as prov (prov.key + '.' + (prov.alias ?? ''))}
						{@const addr = prov.alias ? `${prov.key}.${prov.alias}` : prov.key}
						{@const up = providerUpdates[prov.source]}
						<li class="group flex items-center gap-1">
							<span class="mono flex-1 truncate text-[11px] text-[var(--al-muted)]" title={prov.source}>
								{prov.key}{#if prov.alias}<span class="text-[var(--al-accent)]">.{prov.alias}</span>{/if}
							</span>
							{#if up?.update && up.latest}
								{#if canResources}
									<button
										class="flex items-center gap-0.5 rounded-full border px-1.5 py-px text-[10px] font-semibold transition-colors hover:text-black disabled:opacity-50"
										style="border-color:#e3b34166;color:#e3b341;background:#e3b34114;"
										onmouseenter={(e) => (e.currentTarget.style.background = '#e3b341')}
										onmouseleave={(e) => (e.currentTarget.style.background = '#e3b34114')}
										title="Update available: {up.current ?? up.pinned ?? '?'} → {up.latest}. Click to pin {addr} to {up.latest}."
										disabled={bumping === prov.source}
										onclick={() => onBumpProvider(prov, up.latest!)}
									>
										{#if bumping === prov.source}<Loader2 size={10} class="animate-spin" />{:else}<ArrowUp size={10} />{/if}{up.latest}
									</button>
								{:else}
									<span class="mono text-[10px]" style="color:#e3b341;" title="Update available: {up.current ?? up.pinned ?? '?'} → {up.latest}">↑{up.latest}</span>
								{/if}
							{/if}
							{#if canResources}
								{#if !prov.alias}
									<button class="{iconBtn} opacity-0 group-hover:opacity-100" title="Add aliased instance of {prov.key}" aria-label="Add provider alias" onclick={() => onAddAlias(prov)}><Plus size={13} /></button>
								{:else}
									<button class="{iconBtn} opacity-0 group-hover:opacity-100 hover:text-[var(--al-danger)]" title="Remove {addr}" aria-label="Remove provider instance" onclick={() => onDeleteInstance(prov)}><Trash2 size={13} /></button>
								{/if}
							{/if}
							<button class={iconBtn} title="Configure {addr}" aria-label="Configure provider" onclick={() => onConfigureProvider(prov)}><Settings size={14} /></button>
						</li>
					{/each}
				</ul>
			</div>
		{/if}

		{#if form.resourceTypes.length > 0}
			<div>
				<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Resource types</div>
				<ul class="space-y-0.5">
					{#each form.resourceTypes as t (t)}
						<li>
							<button class="mono w-full rounded px-2 py-1 text-left text-xs {form.selectedType === t && form.formKind === 'resource' && form.kind === 'resource' && !form.editingId ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => onPickType(t)}>{t}</button>
						</li>
					{/each}
				</ul>
			</div>
		{/if}

		{#if form.dataSourceTypes.length > 0}
			<div>
				<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Data sources</div>
				<ul class="space-y-0.5">
					{#each form.dataSourceTypes as t (t)}
						<li>
							<button class="mono w-full rounded px-2 py-1 text-left text-xs {form.selectedType === t && form.formKind === 'resource' && form.kind === 'data' && !form.editingId ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => onPickType(t, 'data')} title="Add data source {t}">{t}</button>
						</li>
					{/each}
				</ul>
			</div>
		{/if}

		<!-- Structure tree: main + local modules as folders -->
		<div>
			<div class="mb-1 flex items-center justify-between">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Structure</span>
				<span class="flex items-center">
					<button class={iconBtn} title="Add registry module" aria-label="Add registry module" onclick={() => onOpenRegistryModule()}><Package size={14} /></button>
					<button class={iconBtn} title="New local module" aria-label="New local module" onclick={onNewModule}><Plus size={14} /></button>
				</span>
			</div>

			<!-- main (root) -->
			<button class="flex w-full items-center gap-1.5 rounded px-2 py-1 text-left text-xs {activeModule === null ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => onSelectScope(null)}>
				<Folder size={12} /> main
				<span class="ml-auto text-[10px]">{project.resources.length}</span>
			</button>
			{#if activeModule === null}
				<ul class="mb-1 ml-3 space-y-0.5 border-l border-[var(--al-border)] pl-2">
					{#each project.resources as r (r.id)}
						<li class="group flex items-center">
							<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px] {form.editingId === r.id ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}" onclick={() => onEditResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
							<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(r.id)} aria-label="Delete"><Trash2 size={11} /></button>
						</li>
					{/each}
					{#each project.dataSources ?? [] as d (d.id)}
						<li class="group flex items-center">
							<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]" style="color:{form.editingId === d.id ? 'var(--al-accent)' : '#56d4bc'};opacity:{form.editingId === d.id ? 1 : 0.85};" onclick={() => onEditResource(d, 'data')} title="Edit data.{d.type}.{d.name}">data.{d.type}.{d.name}</button>
							<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(d.id, 'data')} aria-label="Delete"><Trash2 size={11} /></button>
						</li>
					{/each}
				</ul>
			{/if}

			<!-- local modules -->
			{#each localModules as m (m.name)}
				<div class="group flex items-center">
					<button class="flex flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs {activeModule === m.name ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => onSelectScope(m.name)}>
						<FolderGit2 size={12} /> modules/{m.name}
						<span class="ml-auto text-[10px]">{(m.resources ?? []).length}</span>
					</button>
					<button class="px-1 opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteModule(m.name)} aria-label="Delete module"><Trash2 size={12} /></button>
				</div>
				{#if activeModule === m.name}
					<ul class="mb-1 ml-3 space-y-0.5 border-l border-[var(--al-border)] pl-2">
						{#each m.resources ?? [] as r (r.id)}
							<li class="group flex items-center">
								<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px] {form.editingId === r.id ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}" onclick={() => onEditResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
								<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(r.id)} aria-label="Delete"><Trash2 size={11} /></button>
							</li>
						{/each}
						{#each m.dataSources ?? [] as d (d.id)}
							<li class="group flex items-center">
								<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]" style="color:{form.editingId === d.id ? 'var(--al-accent)' : '#56d4bc'};opacity:{form.editingId === d.id ? 1 : 0.85};" onclick={() => onEditResource(d, 'data')} title="Edit data.{d.type}.{d.name}">data.{d.type}.{d.name}</button>
								<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(d.id, 'data')} aria-label="Delete"><Trash2 size={11} /></button>
							</li>
						{/each}
						<li>
							<button class="mt-0.5 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => onOpenInputs()}>⚙ module inputs</button>
						</li>
					</ul>
				{/if}
			{/each}

			<!-- registry/external module calls -->
			{#each registryModules as m (m.name)}
				<div class="group flex items-center">
					<button class="flex flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]" onclick={() => onOpenRegistryModule(m)} title="{m.source}{m.version ? ' @ ' + m.version : ''}">
						<Package size={12} style="color:#c297ff" /> module.{m.name}
					</button>
					<button class="px-1 opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteRegistryModule(m.name)} aria-label="Delete registry module"><Trash2 size={12} /></button>
				</div>
			{/each}
		</div>
	{:else}
		<p class="text-xs text-[var(--al-muted)]">Create a project to begin.</p>
	{/if}
</aside>
