<script lang="ts">
	import { onMount } from 'svelte';
	import { api, streamPlan } from '$lib/api/client';
	import type { AttrValue, Engine, NormalizedSchema, Project, ProjectSummary, ProviderInstance, Resource } from '$lib/types';
	import SchemaForm from '$lib/form/SchemaForm.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import ProjectConfig from '$lib/components/ProjectConfig.svelte';
	import EditorView from '$lib/editor/EditorView.svelte';
	import type { ProjectConfig as ProjectConfigT } from '$lib/types';
	import {
		Play, Save, Loader2, Boxes, Plus, Trash2, FolderGit2, Settings, FileDown, Database, LayoutDashboard, Variable as VariableIcon
	} from 'lucide-svelte';

	const LAST_PROJECT_KEY = 'arcloom.lastProject';

	let engines = $state<Engine[]>([]);
	let defaultEngine = $state('tofu');

	let projects = $state<ProjectSummary[]>([]);
	let project = $state<Project | null>(null);
	let engine = $state('');

	let providerSource = $state('hashicorp/random');
	let resourceTypes = $state<string[]>([]);
	let selectedType = $state('');
	let schema = $state<NormalizedSchema | null>(null);

	// Center form can edit either a resource or a provider config block.
	let formKind = $state<'resource' | 'provider'>('resource');
	let resourceName = $state('example');
	let editingId = $state<string | null>(null);
	let editingProviderKey = $state('');
	let values = $state<Record<string, AttrValue>>({});

	let hcl = $state('');
	let planLog = $state<string[]>([]);
	let planResult = $state<string>('');

	let busy = $state<string>('');
	let error = $state('');

	// Import + graph UI
	let showImportHcl = $state(false);
	let importHclText = $state('');
	let importHclFiles = $state<Record<string, string>>({});
	let showImportState = $state(false);
	let importStateText = $state('');
	let importMsg = $state('');
	let showEditor = $state(false);

	// New-project modal
	let showNewProject = $state(false);
	let npName = $state('my-infra');
	let npEngine = $state('tofu');

	// Project config modal (variables / locals / outputs)
	let showConfig = $state(false);

	const configCount = $derived(
		(project?.variables?.length ?? 0) + (project?.locals?.length ?? 0) + (project?.outputs?.length ?? 0)
	);

	async function saveConfig(config: ProjectConfigT) {
		if (!project) return;
		busy = 'config';
		try {
			project = await api.setConfig(project.id, config);
			await refreshProjects();
			await refreshHcl();
			showConfig = false;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	const savedResources = $derived(project?.resources ?? []);
	const providers = $derived(project?.providers ?? []);
	const references = $derived(
		savedResources.flatMap((r) => [`${r.type}.${r.name}.id`, `${r.type}.${r.name}.result`])
	);

	onMount(async () => {
		try {
			const e = await api.engines();
			engines = e.engines;
			defaultEngine = e.defaultEngine;
			await refreshProjects();
			const last = localStorage.getItem(LAST_PROJECT_KEY);
			const pick = projects.find((p) => p.id === last) ?? projects[0];
			if (pick) await selectProject(pick.id);
		} catch (err) {
			error = `init failed: ${(err as Error).message}`;
		}
	});

	async function refreshProjects() {
		projects = await api.listProjects();
	}

	async function selectProject(id: string) {
		error = '';
		resetEditor();
		try {
			project = await api.getProject(id);
			engine = project.engine || defaultEngine;
			localStorage.setItem(LAST_PROJECT_KEY, id);
			await refreshHcl();
			if (project.providers?.length) providerSource = project.providers[0].source;
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function newProject() {
		npName = 'my-infra';
		npEngine = defaultEngine;
		showNewProject = true;
	}

	async function createProjectConfirmed() {
		if (!npName.trim()) return;
		busy = 'project';
		try {
			const p = await api.createProject(npName.trim(), npEngine);
			showNewProject = false;
			await refreshProjects();
			await selectProject(p.id);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function removeProject() {
		if (!project) return;
		if (!confirm(`Delete project "${project.name}"? This removes its folder on disk.`)) return;
		const id = project.id;
		try {
			await api.deleteProject(id);
			if (localStorage.getItem(LAST_PROJECT_KEY) === id) localStorage.removeItem(LAST_PROJECT_KEY);
			project = null;
			resetEditor();
			await refreshProjects();
			if (projects[0]) await selectProject(projects[0].id);
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function resetEditor() {
		schema = null;
		selectedType = '';
		editingId = null;
		editingProviderKey = '';
		formKind = 'resource';
		values = {};
		resourceName = 'example';
		hcl = '';
		planLog = [];
		planResult = '';
		resourceTypes = [];
	}

	async function refreshHcl() {
		if (!project) return;
		try {
			hcl = await api.projectHCL(project.id);
		} catch {
			hcl = '';
		}
	}

	async function addProvider() {
		error = '';
		busy = 'provider';
		schema = null;
		selectedType = '';
		try {
			const res = await api.addProvider(providerSource, engine);
			resourceTypes = res.resourceTypes;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function pickType(t: string) {
		error = '';
		busy = 'schema';
		formKind = 'resource';
		selectedType = t;
		editingId = null;
		values = {};
		resourceName = suggestName(t);
		try {
			schema = await api.resourceSchema(providerSource, t, engine);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function editResource(r: { id: string; type: string; name: string; attrs: Record<string, AttrValue> }) {
		error = '';
		busy = 'schema';
		formKind = 'resource';
		selectedType = r.type;
		editingId = r.id;
		resourceName = r.name;
		values = JSON.parse(JSON.stringify(r.attrs ?? {}));
		const src = providerForType(r.type);
		try {
			schema = await api.resourceSchema(src, r.type, engine);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	// Edit a provider's configuration block (host, api_key, ...).
	async function configureProvider(prov: ProviderInstance) {
		error = '';
		busy = 'schema';
		formKind = 'provider';
		editingProviderKey = prov.key;
		providerSource = prov.source;
		editingId = null;
		selectedType = '';
		try {
			schema = await api.providerSchema(prov.source, engine);
			values = JSON.parse(JSON.stringify(prov.config ?? {}));
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	function providerForType(type: string): string {
		const key = type.split('_')[0];
		return project?.providers?.find((p) => p.key === key)?.source ?? providerSource;
	}

	function suggestName(type: string): string {
		const base = type.replace(/^[a-z0-9]+_/, '') || type;
		let n = base;
		let i = 1;
		const taken = new Set(savedResources.filter((r) => r.type === type).map((r) => r.name));
		while (taken.has(n)) n = `${base}_${++i}`;
		return n;
	}

	function buildAttrs(): Record<string, AttrValue> {
		const out: Record<string, AttrValue> = {};
		for (const [name, v] of Object.entries(values)) {
			if (v.mode === 'literal') {
				const lit = v.literal;
				if (lit === undefined || lit === null || lit === '') continue;
				if (Array.isArray(lit) && lit.length === 0) continue;
				if (typeof lit === 'object' && !Array.isArray(lit) && Object.keys(lit).length === 0) continue;
				out[name] = { mode: 'literal', literal: lit };
			} else if (v.mode === 'reference' && v.ref) {
				out[name] = { mode: 'reference', ref: v.ref };
			} else if (v.mode === 'expression' && v.expr) {
				out[name] = { mode: 'expression', expr: v.expr };
			}
		}
		return out;
	}

	async function saveResource() {
		if (!schema || !project) return;
		error = '';
		busy = 'save';
		try {
			await api.upsertResource(project.id, {
				id: editingId ?? undefined,
				type: selectedType,
				name: resourceName,
				providerSource,
				attrs: buildAttrs()
			});
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			editingId = editingId ?? project.resources.find((r) => r.name === resourceName && r.type === selectedType)?.id ?? null;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function saveProviderConfig() {
		if (!project) return;
		error = '';
		busy = 'save';
		try {
			project = await api.setProviderConfig(project.id, editingProviderKey, {
				source: providerSource,
				config: buildAttrs()
			});
			await refreshProjects();
			await refreshHcl();
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function readFiles(list: FileList | null, target: 'hcl' | 'state') {
		if (!list) return;
		for (const f of Array.from(list)) {
			const text = await f.text();
			if (target === 'hcl') importHclFiles = { ...importHclFiles, [f.name]: text };
			else importStateText = text;
		}
	}

	async function doImportHcl() {
		if (!project) return;
		busy = 'import';
		importMsg = '';
		try {
			const files = { ...importHclFiles };
			if (importHclText.trim()) files['pasted.tf'] = importHclText;
			if (Object.keys(files).length === 0) throw new Error('provide .tf files or paste HCL');
			const res = await api.importHCL(project.id, files);
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			importMsg = `Imported ${res.addedResources} resources, ${res.addedProviders} providers${res.warnings?.length ? ` · ${res.warnings.length} warning(s)` : ''}`;
			showImportHcl = false;
			importHclText = '';
			importHclFiles = {};
		} catch (err) {
			importMsg = 'Error: ' + (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function doImportState() {
		if (!project) return;
		busy = 'import';
		importMsg = '';
		try {
			if (!importStateText.trim()) throw new Error('provide a state file');
			const res = await api.importState(project.id, importStateText);
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			importMsg = `Imported ${res.addedResources} resources${res.warnings?.length ? ` · ${res.warnings.length} warning(s)` : ''}`;
			showImportState = false;
			importStateText = '';
		} catch (err) {
			importMsg = 'Error: ' + (err as Error).message;
		} finally {
			busy = '';
		}
	}

	// ---- visual editor callbacks ----
	function openEditor() {
		if (!project) return;
		error = '';
		showEditor = true;
	}

	async function refreshProject() {
		if (!project) return;
		project = await api.getProject(project.id);
		await refreshProjects();
		await refreshHcl();
	}

	// Returns a provider's resource types for the editor palette.
	async function editorLoadTypes(source: string): Promise<string[]> {
		const res = await api.addProvider(source, engine);
		return res.resourceTypes;
	}

	// Palette "add": leave the editor and open the schema form for a new resource.
	async function editorAddType(type: string, source: string) {
		showEditor = false;
		providerSource = source;
		await pickType(type);
	}

	// Clicking a node opens it in the schema form.
	function editorEditResource(r: Resource) {
		showEditor = false;
		editResource(r);
	}

	async function editorDeleteResource(id: string) {
		if (!project) return;
		try {
			await api.deleteResource(project.id, id);
			await refreshProject();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// Re-save an existing resource with a changed field (name or depends_on),
	// preserving its type/attrs.
	async function resaveResource(id: string, patch: { name?: string; dependsOn?: string[] }) {
		if (!project) return;
		const r = project.resources.find((x) => x.id === id);
		if (!r) return;
		try {
			await api.upsertResource(project.id, {
				id: r.id,
				type: r.type,
				name: patch.name ?? r.name,
				providerSource: project.providers.find((p) => p.key === r.type.split('_')[0])?.source,
				attrs: r.attrs,
				dependsOn: patch.dependsOn ?? r.dependsOn
			});
			await refreshProject();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function runPlan() {
		if (!project) return;
		error = '';
		planLog = [];
		planResult = '';
		busy = 'plan';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'result') planResult = ev.hasChanges ? 'Changes pending' : 'No changes';
			else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') busy = '';
		});
	}

	const btn = 'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-40';
	const btnPrimary = `${btn} bg-[var(--al-accent)] text-white hover:brightness-110`;
	const btnGhost = `${btn} border border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]`;
	const iconBtn = 'rounded-md p-1.5 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)] hover:text-[var(--al-text)]';
	const sideBtn = 'flex w-full items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2 py-1.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]';
	const ta = 'mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] p-2.5 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

<div class="flex h-screen flex-col">
	<header class="flex items-center gap-3 border-b border-[var(--al-border)] bg-[var(--al-panel)] px-4 py-2">
		<div class="flex items-center gap-2 font-semibold">
			<Boxes size={18} class="text-[var(--al-accent)]" /> ArcLoom
		</div>
		<div class="ml-2 flex items-center gap-1.5">
			<FolderGit2 size={15} class="text-[var(--al-muted)]" />
			<select
				class="rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm"
				value={project?.id ?? ''}
				onchange={(e) => selectProject(e.currentTarget.value)}
			>
				{#if projects.length === 0}<option value="" disabled selected>No projects</option>{/if}
				{#each projects as p (p.id)}<option value={p.id}>{p.name} · {p.resourceCount}</option>{/each}
			</select>
			<button class={iconBtn} title="New project" onclick={newProject} aria-label="New project"><Plus size={16} /></button>
			{#if project}
				<button class={iconBtn} title="Delete project" onclick={removeProject} aria-label="Delete project"><Trash2 size={16} /></button>
			{/if}
		</div>
		<div class="ml-auto flex items-center gap-2 text-sm">
			<span class="text-xs text-[var(--al-muted)]">engine</span>
			<span class="mono rounded border border-[var(--al-border)] px-2 py-0.5 text-xs text-[var(--al-muted)]">{engine || defaultEngine}</span>
		</div>
	</header>

	{#if error}
		<div class="border-b border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-4 py-1.5 text-sm text-[var(--al-danger)]">{error}</div>
	{/if}
	{#if importMsg}
		<div class="border-b border-[var(--al-border)] bg-[var(--al-panel-2)] px-4 py-1.5 text-xs text-[var(--al-muted)]">{importMsg}</div>
	{/if}

	<div class="flex min-h-0 flex-1">
		<aside class="w-64 shrink-0 space-y-3 overflow-y-auto border-r border-[var(--al-border)] bg-[var(--al-panel)] p-3">
			{#if project}
				<div>
					<label for="provider-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Provider</label>
					<div class="flex gap-1.5">
						<input id="provider-source" class="min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={providerSource} placeholder="hashicorp/random" />
						<button class={btnGhost} onclick={addProvider} disabled={busy === 'provider'}>
							{#if busy === 'provider'}<Loader2 size={14} class="animate-spin" />{:else}Add{/if}
						</button>
					</div>
				</div>

				{#if providers.length > 0}
					<div>
						<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Configured providers</div>
						<ul class="space-y-0.5">
							{#each providers as prov (prov.key)}
								<li class="flex items-center gap-1">
									<span class="mono flex-1 truncate text-[11px] text-[var(--al-muted)]" title={prov.source}>{prov.key}</span>
									<button class={iconBtn} title="Configure {prov.key}" aria-label="Configure provider" onclick={() => configureProvider(prov)}><Settings size={14} /></button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				{#if resourceTypes.length > 0}
					<div>
						<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Resource types</div>
						<ul class="space-y-0.5">
							{#each resourceTypes as t (t)}
								<li>
									<button class="mono w-full rounded px-2 py-1 text-left text-xs {selectedType === t && formKind === 'resource' && !editingId ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => pickType(t)}>{t}</button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				{#if savedResources.length > 0}
					<div>
						<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">In project</div>
						<ul class="space-y-0.5">
							{#each savedResources as r (r.id)}
								<li>
									<button class="mono w-full truncate rounded px-2 py-1 text-left text-[11px] {editingId === r.id ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => editResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				<div class="space-y-1.5 border-t border-[var(--al-border)] pt-3">
					<button class={sideBtn} onclick={() => (showConfig = true)}>
						<VariableIcon size={14} /> Variables & outputs{#if configCount > 0}<span class="ml-auto text-[10px] text-[var(--al-muted)]">{configCount}</span>{/if}
					</button>
					<button class={sideBtn} onclick={() => { importMsg=''; showImportHcl = true; }}><FileDown size={14} /> Import HCL</button>
					<button class={sideBtn} onclick={() => { importMsg=''; showImportState = true; }}><Database size={14} /> Import state</button>
					<button class={sideBtn} onclick={openEditor}>
						<LayoutDashboard size={14} /> Visual editor
					</button>
				</div>
			{:else}
				<p class="text-xs text-[var(--al-muted)]">Create a project to begin.</p>
			{/if}
		</aside>

		<main class="min-w-0 flex-1 overflow-y-auto p-5">
			{#if !project}
				<div class="grid h-full place-items-center text-sm text-[var(--al-muted)]">
					<button class={btnPrimary} onclick={newProject}><Plus size={15} /> New project</button>
				</div>
			{:else if busy === 'schema'}
				<div class="flex items-center gap-2 text-sm text-[var(--al-muted)]"><Loader2 size={16} class="animate-spin" /> Loading schema…</div>
			{:else if schema && formKind === 'provider'}
				<div class="mb-4 flex items-end gap-3">
					<div class="mono text-sm text-[var(--al-muted)]">provider "<span class="text-[var(--al-accent)]">{editingProviderKey}</span>"</div>
					<button class={btnPrimary + ' ml-auto'} onclick={saveProviderConfig} disabled={busy === 'save'}><Save size={15} /> Save provider config</button>
				</div>
				{#if (schema.fields?.length ?? 0) === 0}
					<p class="text-sm text-[var(--al-muted)]">This provider has no configurable arguments.</p>
				{:else}
					<SchemaForm {schema} bind:values {references} />
				{/if}
			{:else if schema}
				<div class="mb-4 flex items-end gap-3">
					<div>
						<label for="resource-name" class="mb-1 block text-xs text-[var(--al-muted)]">resource name</label>
						<input id="resource-name" class="mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={resourceName} />
					</div>
					<div class="mono text-sm text-[var(--al-muted)]">resource "<span class="text-[var(--al-accent)]">{selectedType}</span>" "{resourceName}"{#if editingId}<span class="ml-1 text-[10px] uppercase">editing</span>{/if}</div>
					<button class={btnPrimary + ' ml-auto'} onclick={saveResource} disabled={busy === 'save'}><Save size={15} /> {editingId ? 'Update' : 'Save'} resource</button>
				</div>
				<SchemaForm {schema} bind:values {references} />
			{:else}
				<div class="grid h-full place-items-center text-center text-sm text-[var(--al-muted)]">
					Add a provider and pick a resource type, select an existing resource to edit,<br />or import existing HCL / state.
				</div>
			{/if}
		</main>

		<aside class="flex w-[26rem] shrink-0 flex-col border-l border-[var(--al-border)] bg-[var(--al-panel)]">
			<div class="flex items-center justify-between border-b border-[var(--al-border)] px-3 py-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Generated HCL</span>
				<button class={btnGhost} onclick={runPlan} disabled={!project || busy === 'plan'}>
					{#if busy === 'plan'}<Loader2 size={14} class="animate-spin" />{:else}<Play size={14} />{/if} Plan
				</button>
			</div>
			<pre class="mono max-h-[45%] overflow-auto whitespace-pre-wrap px-3 py-2 text-[11px] text-[var(--al-text)]">{hcl || '// save a resource to preview HCL'}</pre>
			<div class="border-t border-[var(--al-border)] px-3 py-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				Plan output
				{#if planResult}<span class="ml-2 rounded bg-[var(--al-accent-weak)] px-1.5 py-0.5 text-[10px] normal-case text-[var(--al-accent)]">{planResult}</span>{/if}
			</div>
			<pre class="mono flex-1 overflow-auto whitespace-pre-wrap bg-[var(--al-bg)] px-3 py-2 text-[11px] text-[var(--al-muted)]">{planLog.join('\n') || '// run plan to see output'}</pre>
		</aside>
	</div>
</div>

<!-- New project modal -->
<Modal title="New project" bind:open={showNewProject}>
	<div class="space-y-3">
		<div>
			<label for="np-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Name</label>
			<input
				id="np-name"
				class="w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
				bind:value={npName}
				placeholder="my-infra"
				onkeydown={(e) => { if (e.key === 'Enter') createProjectConfirmed(); }}
			/>
		</div>
		<div>
			<label for="np-engine" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Engine</label>
			<select id="np-engine" class="w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm" bind:value={npEngine}>
				{#each engines as e (e.engine)}
					<option value={e.engine}>{e.engine} {e.version}</option>
				{/each}
			</select>
		</div>
	</div>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (showNewProject = false)}>Cancel</button>
		<button class={btnPrimary} onclick={createProjectConfirmed} disabled={busy === 'project' || !npName.trim()}>
			{#if busy === 'project'}<Loader2 size={14} class="animate-spin" />{/if} Create
		</button>
	{/snippet}
</Modal>

{#if project}
	<ProjectConfig {project} bind:open={showConfig} saving={busy === 'config'} onSave={saveConfig} />
{/if}

<!-- Import HCL modal -->
<Modal title="Import existing HCL" bind:open={showImportHcl}>
	<p class="mb-2 text-xs text-[var(--al-muted)]">Select one or more <span class="mono">.tf</span> files, or paste HCL below. Resources, providers and required_providers are reconstructed into the model.</p>
	<input type="file" multiple accept=".tf,.hcl" class="mb-2 text-xs" onchange={(e) => readFiles(e.currentTarget.files, 'hcl')} />
	{#if Object.keys(importHclFiles).length > 0}
		<p class="mb-2 text-xs text-[var(--al-muted)]">Files: {Object.keys(importHclFiles).join(', ')}</p>
	{/if}
	<textarea class={ta} rows="12" placeholder="resource &quot;...&quot; ..." bind:value={importHclText}></textarea>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (showImportHcl = false)}>Cancel</button>
		<button class={btnPrimary} onclick={doImportHcl} disabled={busy === 'import'}>
			{#if busy === 'import'}<Loader2 size={14} class="animate-spin" />{/if} Import
		</button>
	{/snippet}
</Modal>

<!-- Import state modal -->
<Modal title="Import local state" bind:open={showImportState}>
	<p class="mb-2 text-xs text-[var(--al-muted)]">Select a <span class="mono">terraform.tfstate</span> file or paste its JSON. Managed resources are imported; computed/sensitive values are dropped so the generated config stays valid.</p>
	<input type="file" accept=".tfstate,.json" class="mb-2 text-xs" onchange={(e) => readFiles(e.currentTarget.files, 'state')} />
	<textarea class={ta} rows="12" placeholder={'{ "version": 4, "resources": [...] }'} bind:value={importStateText}></textarea>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (showImportState = false)}>Cancel</button>
		<button class={btnPrimary} onclick={doImportState} disabled={busy === 'import'}>
			{#if busy === 'import'}<Loader2 size={14} class="animate-spin" />{/if} Import
		</button>
	{/snippet}
</Modal>

<!-- Visual editor (full-screen) -->
{#if showEditor && project}
	<EditorView
		{project}
		{hcl}
		loadTypes={editorLoadTypes}
		onAddType={editorAddType}
		onEditResource={editorEditResource}
		onDeleteResource={editorDeleteResource}
		onRenameResource={(id, name) => resaveResource(id, { name })}
		onSetDependsOn={(id, deps) => resaveResource(id, { dependsOn: deps })}
		onClose={() => (showEditor = false)}
	/>
{/if}
