<script lang="ts">
	// Structure sidebar: provider source input + configured providers (with update
	// badges and alias management), the resource/data-source type palette, and the
	// structure tree.
	//
	// The tree separates USES from DEFINITIONS, because they are separate things:
	// `main` lists what the root configuration actually contains — its resources,
	// its data sources and its module CALLS (expanded into one row per instance
	// when count/for_each is literal enough to enumerate) — while the modules
	// below it are the definitions those calls point at. A definition nobody
	// calls generates no block, and one called three times shows three entries
	// under main.
	//
	// Reads the resource-form store directly for the type/edit highlight states;
	// everything else arrives as data + callbacks (permission is a boolean, never
	// an auth import).
	import type { LocalModule, Module, Project, ProjectFile, ProviderInstance, ProviderUpdate, Resource, ResourceKind } from '$lib/types';
	import { btnGhost, iconBtn } from '$lib/styles';
	import { form } from '$lib/stores/resourceForm.svelte';
	import { multiBadge, type MultiBadge } from '$lib/model/multiplicity';
	import { expandCall, indexSuffix } from '$lib/model/expand';
	import { Loader2, Plus, Trash2, FolderGit2, Folder, Settings, Package, ArrowUp, FileCode, Boxes } from 'lucide-svelte';

	let {
		width,
		project,
		providers,
		providerUpdates,
		bumping,
		localModules,
		moduleCalls,
		files,
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
		onOpenCall,
		onDeleteCall,
		onOpenFile,
		onDeleteFile
	}: {
		width: number;
		project: Project | null;
		providers: ProviderInstance[];
		providerUpdates: Record<string, ProviderUpdate>;
		bumping: string;
		localModules: LocalModule[];
		moduleCalls: Module[];
		files: ProjectFile[];
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
		onOpenCall: (m?: Module, source?: string) => void;
		onDeleteCall: (name: string) => void;
		onOpenFile: (f?: ProjectFile) => void;
		onDeleteFile: (path: string) => void;
	} = $props();

	// localFolder mirrors model.Module.LocalFolder: "" for a registry source.
	const LOCAL_PREFIX = './modules/';
	function localFolder(m: Module): string {
		if (!m.source?.startsWith(LOCAL_PREFIX)) return '';
		const f = m.source.slice(LOCAL_PREFIX.length);
		return f && !f.includes('/') ? f : '';
	}

	// Root-scope entry count: what `main` actually contains, module calls (and
	// their instances) included — the number used to stop at resources, so a
	// root made almost entirely of module calls read as empty.
	const rootCount = $derived.by(() => {
		let n = (project?.resources.length ?? 0) + (project?.dataSources?.length ?? 0);
		for (const m of moduleCalls) {
			const e = expandCall(m, project);
			n += e ? e.keys.length + e.truncated : 1;
		}
		return n;
	});

	// callsFor drives the "N uses" hint on a definition row.
	function callsFor(folder: string): Module[] {
		return moduleCalls.filter((m) => localFolder(m) === folder);
	}

	// The declarations a block's for_each may name, per scope — what lets the
	// badge say ×2 instead of ∀ for a collection that is literal enough to
	// evaluate here. A root block sees the root's var/local, a module's interior
	// sees that module's.
	const rootScope = $derived({ variables: project?.variables ?? [], locals: project?.locals ?? [] });
	const moduleScope = (m: LocalModule) => ({ variables: m.variables ?? [], locals: m.locals ?? [] });
</script>

<!--
  multi — the ×N / ∀ multiplicity pill, the sidebar twin of the canvas card's
  multiPill. Every row in the structure tree lists ONE entry per block, so
  without this a count/for_each'd resource (or a repeated module call, where
  the whole folder multiplies) reads as a single object and the tree
  under-reports the project. `note` prefixes the tooltip for the module rows,
  whose multiplicity applies to everything inside them.
-->
{#snippet multi(m: MultiBadge | undefined, note = '')}
	{#if m}
		<span
			class="mono ml-1 shrink-0 rounded-full px-1.5 py-px text-[10px] font-semibold"
			style="border:1px solid var(--al-border);background:var(--al-accent-weak);color:var(--al-accent);"
			title={note + m.title}
		>{m.label}</span>
	{/if}
{/snippet}

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

		<!-- Structure tree: main (with its module calls) + the definitions below -->
		<div>
			<div class="mb-1 flex items-center justify-between">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Structure</span>
				<span class="flex items-center">
					<button class={iconBtn} title="Add a module call (registry or local)" aria-label="Add module call" onclick={() => onOpenCall()}><Package size={14} /></button>
					<button class={iconBtn} title="New local module" aria-label="New local module" onclick={onNewModule}><Plus size={14} /></button>
				</span>
			</div>

			<!-- main (root) -->
			<button class="flex w-full items-center gap-1.5 rounded px-2 py-1 text-left text-xs {activeModule === null ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => onSelectScope(null)}>
				<Folder size={12} /> main
				<span class="ml-auto text-[10px]" title="Blocks in the root configuration, module instances included">{rootCount}</span>
			</button>
			{#if activeModule === null}
				<ul class="mb-1 ml-3 space-y-0.5 border-l border-[var(--al-border)] pl-2">
					{#each project.resources as r (r.id)}
						<li class="group flex items-center">
							<button class="mono min-w-0 flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px] {form.editingId === r.id ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}" onclick={() => onEditResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
							{@render multi(multiBadge(r, rootScope))}
							<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(r.id)} aria-label="Delete"><Trash2 size={11} /></button>
						</li>
					{/each}
					{#each project.dataSources ?? [] as d (d.id)}
						<li class="group flex items-center">
							<button class="mono min-w-0 flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]" style="color:{form.editingId === d.id ? 'var(--al-accent)' : '#56d4bc'};opacity:{form.editingId === d.id ? 1 : 0.85};" onclick={() => onEditResource(d, 'data')} title="Edit data.{d.type}.{d.name}">data.{d.type}.{d.name}</button>
							{@render multi(multiBadge(d, rootScope))}
							<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(d.id, 'data')} aria-label="Delete"><Trash2 size={11} /></button>
						</li>
					{/each}

					<!--
					  Module CALLS live here, in main, because that is where their
					  blocks are. A call whose count/for_each enumerates shows one
					  row per instance carrying the real address
					  (module.team["alpha"]) — which is the only way the tree tells
					  you three definitions can mean thirty resources. When the
					  expression needs the engine, it stays one row with the badge.
					-->
					{#each moduleCalls as m (m.name)}
						{@const folder = localFolder(m)}
						{@const exp = expandCall(m, project)}
						<li class="group flex items-center">
							<button
								class="mono min-w-0 flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]"
								style="color:{folder ? 'var(--al-accent)' : '#c297ff'};opacity:0.9;"
								onclick={() => onOpenCall(m)}
								title="Edit module.{m.name} — {m.source}{m.version ? ' @ ' + m.version : ''}"
							>module.{m.name}</button>
							{#if !exp}{@render multi(multiBadge(m))}{/if}
							<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteCall(m.name)} aria-label="Delete module call"><Trash2 size={11} /></button>
						</li>
						{#if exp}
							{#each exp.keys as key (key)}
								<li class="flex items-center">
									<button
										class="mono min-w-0 flex-1 truncate rounded py-0.5 pl-4 pr-1.5 text-left text-[11px] text-[var(--al-muted)] hover:text-[var(--al-text)]"
										onclick={() => onOpenCall(m)}
										title="Instance of module.{m.name} ({exp.kind === 'count' ? 'count' : 'for_each'})"
									>↳ module.{m.name}{indexSuffix(exp, key)}</button>
								</li>
							{/each}
							{#if exp.truncated > 0}
								<li class="pl-4 text-[10px] text-[var(--al-muted)]">+{exp.truncated} more instance{exp.truncated === 1 ? '' : 's'}</li>
							{:else if exp.keys.length === 0}
								<li class="pl-4 text-[10px] text-[var(--al-muted)]" title="count = 0 / for_each = [] — this call creates nothing">↳ no instances</li>
							{/if}
						{/if}
					{/each}
				</ul>
			{/if}

			<!--
			  Definitions. Separate from the calls above: a module here is a
			  modules/<name>/ folder, and whether it is used (and how often) is the
			  "N uses" hint, not a property of the folder.
			-->
			<div class="mb-1 mt-2 flex items-center gap-1 px-2 text-[10px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				<Boxes size={11} /> Module definitions
			</div>
			{#if localModules.length === 0}
				<p class="px-2 text-[11px] text-[var(--al-muted)]">No local modules. Add one to author a reusable modules/&lt;name&gt;/ folder.</p>
			{/if}
			{#each localModules as m (m.name)}
				{@const uses = callsFor(m.name)}
				<div class="group flex items-center">
					<button class="flex min-w-0 flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs {activeModule === m.name ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => onSelectScope(m.name)}>
						<FolderGit2 size={12} /> <span class="truncate">modules/{m.name}</span>
						<span class="ml-auto shrink-0 text-[10px]" title="Blocks defined in this module">{(m.resources ?? []).length}</span>
						<span
							class="shrink-0 rounded-full px-1.5 py-px text-[10px] font-semibold"
							style={uses.length
								? 'border:1px solid var(--al-border);background:var(--al-accent-weak);color:var(--al-accent);'
								: 'border:1px dashed var(--al-border);color:var(--al-muted);'}
							title={uses.length
								? `Used by ${uses.map((u) => 'module.' + u.name).join(', ')}`
								: 'Defined but never called — this module generates its folder and nothing else'}
						>{uses.length ? `${uses.length} use${uses.length === 1 ? '' : 's'}` : 'unused'}</span>
					</button>
					<button class="px-1 opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteModule(m.name)} aria-label="Delete module"><Trash2 size={12} /></button>
				</div>
				{#if activeModule === m.name}
					<ul class="mb-1 ml-3 space-y-0.5 border-l border-[var(--al-border)] pl-2">
						{#each m.resources ?? [] as r (r.id)}
							<li class="group flex items-center">
								<button class="mono min-w-0 flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px] {form.editingId === r.id ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}" onclick={() => onEditResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
								{@render multi(multiBadge(r, moduleScope(m)))}
								<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(r.id)} aria-label="Delete"><Trash2 size={11} /></button>
							</li>
						{/each}
						{#each m.dataSources ?? [] as d (d.id)}
							<li class="group flex items-center">
								<button class="mono min-w-0 flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]" style="color:{form.editingId === d.id ? 'var(--al-accent)' : '#56d4bc'};opacity:{form.editingId === d.id ? 1 : 0.85};" onclick={() => onEditResource(d, 'data')} title="Edit data.{d.type}.{d.name}">data.{d.type}.{d.name}</button>
								{@render multi(multiBadge(d, moduleScope(m)))}
								<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => onDeleteResource(d.id, 'data')} aria-label="Delete"><Trash2 size={11} /></button>
							</li>
						{/each}
						<!--
						  The arguments belong to a CALL, not to the folder, so from
						  here the only sensible action is to add another use of it.
						-->
						<li>
							<button class="mt-0.5 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => onOpenCall(undefined, `./modules/${m.name}`)}>+ use this module</button>
						</li>
					</ul>
				{/if}
			{/each}
		</div>

		<!--
		  Companion files: templates/scripts referenced with file()/templatefile().
		  Scope-independent (a path under modules/<name>/ targets that module), so
		  this sits below the whole tree rather than inside a scope.
		-->
		<div class="mt-3">
			<div class="mb-1 flex items-center justify-between">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Files</span>
				{#if canResources}
					<button class={iconBtn} title="Add file (template, script, JSON)" aria-label="Add file" onclick={() => onOpenFile()}><Plus size={14} /></button>
				{/if}
			</div>
			{#if files.length === 0}
				<p class="px-2 text-[11px] text-[var(--al-muted)]">No files. Add a template or script to reference with <span class="mono">templatefile()</span>.</p>
			{:else}
				{#each files as f (f.path)}
					<div class="group flex items-center">
						<button class="flex min-w-0 flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]" onclick={() => onOpenFile(f)} title={f.path}>
							<FileCode size={12} style="color:#7ad1a8" />
							<span class="truncate">{f.path}</span>
						</button>
						{#if canResources}
							<button class="px-1 text-[var(--al-muted)] opacity-0 hover:text-[var(--al-danger)] group-hover:opacity-100" onclick={() => onDeleteFile(f.path)} aria-label="Delete file"><Trash2 size={12} /></button>
						{/if}
					</div>
				{/each}
			{/if}
		</div>
	{:else}
		<p class="text-xs text-[var(--al-muted)]">Create a project to begin.</p>
	{/if}
</aside>
