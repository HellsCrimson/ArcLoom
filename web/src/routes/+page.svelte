<script lang="ts">
	import { onMount } from 'svelte';
	import { api, streamPlan, streamOp, type PlanChange } from '$lib/api/client';
	import type { AttrValue, Engine, NormalizedSchema, Project, ProjectSummary, ProviderInstance, Resource } from '$lib/types';
	import SchemaForm from '$lib/form/SchemaForm.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import ProjectConfig from '$lib/components/ProjectConfig.svelte';
	import SecretsModal from '$lib/components/SecretsModal.svelte';
	import GitPanel from '$lib/components/GitPanel.svelte';
	import UserMenu from '$lib/components/UserMenu.svelte';
	import SettingsModal from '$lib/components/SettingsModal.svelte';
	import { auth } from '$lib/stores/auth.svelte';
	import EditorView from '$lib/editor/EditorView.svelte';
	import HclView from '$lib/editor/HclView.svelte';
	import { ACTION_COLOR, ACTION_SYMBOL } from '$lib/editor/graph';
	import type { ProjectConfig as ProjectConfigT, SecretMeta } from '$lib/types';
	import {
		Play, Save, Loader2, Boxes, Plus, Trash2, FolderGit2, Folder, Settings, FileDown, Database, LayoutDashboard, ChevronDown, KeyRound, GitBranch, Variable as VariableIcon, Rocket, Flame
	} from 'lucide-svelte';

	const LAST_PROJECT_KEY = 'arcloom.lastProject';

	let engines = $state<Engine[]>([]);
	let defaultEngine = $state('tofu');

	let projects = $state<ProjectSummary[]>([]);
	let project = $state<Project | null>(null);
	let engine = $state('');

	let providerSource = $state('');
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
	let planChanges = $state<PlanChange[]>([]);
	let planCounts = $state<{ add: number; change: number; destroy: number; replace: number } | null>(null);
	let showApplyConfirm = $state(false);
	let showDestroyConfirm = $state(false);
	let destroyName = $state('');

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
	// Import dropdown in the top bar
	let showImportMenu = $state(false);
	let showSettings = $state(false);

	// Git
	let showGit = $state(false);

	// Secrets
	let showSecrets = $state(false);
	let secretsList = $state<SecretMeta[]>([]);
	let secretsLocalEnabled = $state(false);
	const secretNames = $derived(secretsList.map((s) => s.name));

	async function loadSecrets() {
		if (!project) return;
		try {
			const res = await api.listSecrets(project.id);
			secretsList = res.secrets ?? [];
			secretsLocalEnabled = res.localEnabled;
		} catch {
			secretsList = [];
		}
	}

	async function addSecret(name: string, value: string) {
		if (!project) return;
		busy = 'secret';
		try {
			await api.setSecret(project.id, name, value);
			await loadSecrets();
			await refreshHcl();
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function deleteSecret(name: string) {
		if (!project) return;
		try {
			await api.deleteSecret(project.id, name);
			await loadSecrets();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	async function saveConfig(config: ProjectConfigT) {
		if (!project) return;
		busy = 'config';
		try {
			project = await api.setConfig(project.id, config, activeModule ?? undefined);
			await refreshProjects();
			await refreshHcl();
			showConfig = false;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	// Module scoping: activeModule = null means the root ("main") module.
	let activeModule = $state<string | null>(null);
	const localModules = $derived(project?.localModules ?? []);
	const activeMod = $derived(activeModule ? (localModules.find((m) => m.name === activeModule) ?? null) : null);
	const savedResources = $derived(activeMod ? (activeMod.resources ?? []) : (project?.resources ?? []));
	const providers = $derived(project?.providers ?? []);
	const references = $derived.by(() => {
		const refs: string[] = [];
		for (const r of savedResources) refs.push(`${r.type}.${r.name}.id`, `${r.type}.${r.name}.result`);
		if (!activeMod) {
			// From the root you can reference each module's outputs.
			for (const m of localModules) for (const o of m.outputs ?? []) refs.push(`module.${m.name}.${o.name}`);
		} else {
			// Inside a module you can reference its own input variables.
			for (const v of activeMod.variables ?? []) refs.push(`var.${v.name}`);
		}
		return refs;
	});
	// Config (variables/locals/outputs) for the active scope, fed to the modal.
	const scopeConfig = $derived({
		variables: (activeMod ?? project)?.variables ?? [],
		locals: (activeMod ?? project)?.locals ?? [],
		outputs: (activeMod ?? project)?.outputs ?? []
	});
	const configCount = $derived(
		scopeConfig.variables.length + scopeConfig.locals.length + scopeConfig.outputs.length
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
			await loadSecrets();
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
		planChanges = [];
		planCounts = null;
		resourceTypes = [];
		activeModule = null;
	}

	async function refreshHcl() {
		if (!project) return;
		try {
			hcl = await api.projectHCL(project.id, activeModule ?? undefined);
		} catch {
			hcl = '';
		}
	}

	// Switch the editing scope (null = root/main, else a local module).
	async function selectScope(mod: string | null) {
		activeModule = mod;
		schema = null;
		selectedType = '';
		editingId = null;
		formKind = 'resource';
		values = {};
		await refreshHcl();
	}

	// ---- local modules ----
	let showNewModule = $state(false);
	let nmName = $state('');
	let showInputs = $state(false);

	async function createModule() {
		if (!project || !nmName.trim()) return;
		busy = 'module';
		try {
			project = await api.createLocalModule(project.id, nmName.trim());
			await refreshProjects();
			showNewModule = false;
			await selectScope(nmName.trim());
			nmName = '';
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function deleteModule(name: string) {
		if (!project) return;
		if (!confirm(`Delete module "${name}" and all its resources?`)) return;
		try {
			project = await api.deleteLocalModule(project.id, name);
			await refreshProjects();
			if (activeModule === name) await selectScope(null);
			else await refreshHcl();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	async function deleteScopedResource(rid: string) {
		if (!project) return;
		try {
			await api.deleteResource(project.id, rid, activeModule ?? undefined);
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			if (editingId === rid) resetFormOnly();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function resetFormOnly() {
		schema = null;
		selectedType = '';
		editingId = null;
		values = {};
	}

	// Draft values for the module-inputs modal (raw HCL text per variable).
	let inputDraft = $state<Record<string, string>>({});
	$effect(() => {
		if (showInputs && activeMod) {
			const d: Record<string, string> = {};
			for (const v of activeMod.variables ?? []) {
				const av = activeMod.inputs?.[v.name];
				d[v.name] = av
					? (av.mode === 'expression' ? (av.expr ?? '') : av.mode === 'reference' ? (av.ref ?? '') : av.mode === 'literal' ? JSON.stringify(av.literal ?? '') : '')
					: '';
			}
			inputDraft = d;
		}
	});

	function submitInputs() {
		const inputs: Record<string, AttrValue> = {};
		for (const [k, text] of Object.entries(inputDraft)) {
			const t = (text ?? '').trim();
			if (t) inputs[k] = { mode: 'expression', expr: t };
		}
		saveModuleInputs(inputs);
	}

	async function saveModuleInputs(inputs: Record<string, AttrValue>) {
		if (!project || !activeModule) return;
		busy = 'inputs';
		try {
			project = await api.setModuleInputs(project.id, activeModule, inputs);
			await refreshProjects();
			await refreshHcl();
			showInputs = false;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
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
			} else if (v.mode === 'secret' && v.secretRef) {
				out[name] = { mode: 'secret', secretRef: v.secretRef };
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
				module: activeModule ?? undefined,
				attrs: buildAttrs()
			});
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			const scopeRes = activeModule
				? (project.localModules?.find((m) => m.name === activeModule)?.resources ?? [])
				: project.resources;
			editingId = editingId ?? scopeRes.find((r) => r.name === resourceName && r.type === selectedType)?.id ?? null;
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
			const res = await api.importHCL(project.id, files, activeModule ?? undefined);
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			const where = activeModule ? `module "${activeModule}"` : 'root';
			const parts = [
				`${res.addedResources} resources`,
				res.addedVariables ? `${res.addedVariables} variables` : '',
				res.addedLocalModules ? `${res.addedLocalModules} modules` : '',
				`${res.addedProviders} providers`
			].filter(Boolean);
			importMsg = `Imported into ${where}: ${parts.join(', ')}${res.warnings?.length ? ` · ${res.warnings.length} warning(s)` : ''}`;
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
		showEditor = !showEditor;
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

	// scopeResources returns the resource list for a scope (module name or root).
	function scopeResources(moduleName?: string): Resource[] {
		if (!project) return [];
		return moduleName
			? (project.localModules?.find((m) => m.name === moduleName)?.resources ?? [])
			: project.resources;
	}

	// Clicking a node opens it in the schema form (in the node's own scope). The
	// canvas passes a shallow node, so we resolve the full resource (with attrs).
	function editorEditResource(r: Resource, moduleName?: string) {
		const real = scopeResources(moduleName).find((x) => x.id === r.id) ?? r;
		activeModule = moduleName ?? null;
		showEditor = false;
		editResource(real);
	}

	async function editorDeleteResource(id: string, moduleName?: string) {
		if (!project) return;
		try {
			await api.deleteResource(project.id, id, moduleName);
			await refreshProject();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// Re-save an existing resource with a changed field (name or depends_on),
	// preserving its type/attrs, within its scope.
	async function resaveResource(id: string, patch: { name?: string; dependsOn?: string[] }, moduleName?: string) {
		if (!project) return;
		const r = scopeResources(moduleName).find((x) => x.id === id);
		if (!r) return;
		try {
			await api.upsertResource(project.id, {
				id: r.id,
				type: r.type,
				name: patch.name ?? r.name,
				providerSource: project.providers.find((p) => p.key === r.type.split('_')[0])?.source,
				module: moduleName,
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
		planChanges = [];
		planCounts = null;
		busy = 'plan';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'result') {
				planResult = ev.hasChanges ? 'Changes pending' : 'No changes';
				planChanges = ev.changes ?? [];
				planCounts = ev.hasChanges
					? { add: ev.add, change: ev.change, destroy: ev.destroy, replace: ev.replace }
					: null;
			} else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') busy = '';
		});
	}

	// Apply/destroy run against the project's persisted state, so a later plan
	// reflects what is really deployed.
	function runOp(op: 'apply' | 'destroy') {
		if (!project) return;
		error = '';
		planLog = [];
		planResult = '';
		planChanges = [];
		planCounts = null;
		busy = op;
		streamOp(project.id, op, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') {
				planResult = op === 'apply' ? 'Applied' : 'Destroyed';
				busy = '';
			}
		});
	}

	const btn = 'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-40';
	const btnPrimary = `${btn} bg-[var(--al-accent)] text-white hover:brightness-110`;
	const btnGhost = `${btn} border border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]`;
	const iconBtn = 'rounded-md p-1.5 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)] hover:text-[var(--al-text)]';
	const hdrBtn = 'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs font-medium text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40';
	const ta = 'mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] p-2.5 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';

	// Accent as literal hex (for inline styles that concatenate/tint).
	const accentHex = '#4c8dff';
	const accentWeak = '#1f3a5f';

</script>

<svelte:window onclick={() => (showImportMenu = false)} />

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

		{#if project}
			<div class="ml-3 flex items-center gap-1.5 border-l border-[var(--al-border)] pl-3">
				<button
					class="{hdrBtn} {showEditor ? 'border-[var(--al-accent)] bg-[var(--al-accent-weak)] text-[var(--al-accent)]' : ''}"
					onclick={openEditor}
				><LayoutDashboard size={14} /> Editor</button>
				<button class={hdrBtn} onclick={() => (showConfig = true)}>
					<VariableIcon size={14} /> Variables{#if configCount > 0}<span class="text-[10px] text-[var(--al-muted)]">{configCount}</span>{/if}
				</button>
				<button class={hdrBtn} onclick={() => { loadSecrets(); showSecrets = true; }}>
					<KeyRound size={14} /> Secrets{#if secretsList.length > 0}<span class="text-[10px] text-[var(--al-muted)]">{secretsList.length}</span>{/if}
				</button>
				{#if auth.can('git')}
					<button class={hdrBtn} onclick={() => (showGit = true)}>
						<GitBranch size={14} /> Git
					</button>
				{/if}
				{#if auth.can('resources')}
				<div class="relative">
					<button class={hdrBtn} onclick={(e) => { e.stopPropagation(); importMsg = ''; showImportMenu = !showImportMenu; }}>
						<FileDown size={14} /> Import <ChevronDown size={13} />
					</button>
					{#if showImportMenu}
						<div class="absolute left-0 top-full z-20 mt-1 w-40 overflow-hidden rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] shadow-xl">
							<button class="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]" onclick={() => { showImportMenu = false; showImportHcl = true; }}><FileDown size={13} /> Import HCL</button>
							<button class="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]" onclick={() => { showImportMenu = false; showImportState = true; }}><Database size={13} /> Import state</button>
						</div>
					{/if}
				</div>
				{/if}
			</div>
		{/if}

		<div class="ml-auto flex items-center gap-2 text-sm">
			<span class="text-xs text-[var(--al-muted)]">engine</span>
			<span class="mono rounded border border-[var(--al-border)] px-2 py-0.5 text-xs text-[var(--al-muted)]">{engine || defaultEngine}</span>
			{#if auth.isAdmin}
				<button class={iconBtn} title="Settings" aria-label="Settings" onclick={() => (showSettings = true)}><Settings size={16} /></button>
			{/if}
			{#if auth.authEnabled && auth.user}
				<UserMenu />
			{/if}
		</div>
	</header>

	{#if error}
		<div class="border-b border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-4 py-1.5 text-sm text-[var(--al-danger)]">{error}</div>
	{/if}
	{#if importMsg}
		<div class="border-b border-[var(--al-border)] bg-[var(--al-panel-2)] px-4 py-1.5 text-xs text-[var(--al-muted)]">{importMsg}</div>
	{/if}

	<div class="flex min-h-0 flex-1">
		{#if showEditor && project}
			<EditorView
				{project}
				{hcl}
				accent="#4c8dff"
				{planChanges}
				planning={busy === 'plan'}
				onPlan={auth.can('deploy') ? runPlan : undefined}
				loadTypes={editorLoadTypes}
				onAddType={editorAddType}
				onEditResource={editorEditResource}
				onDeleteResource={editorDeleteResource}
				onRenameResource={(id, name, mod) => resaveResource(id, { name }, mod)}
				onSetDependsOn={(id, deps, mod) => resaveResource(id, { dependsOn: deps }, mod)}
				onClose={() => (showEditor = false)}
			/>
		{:else}
		<aside class="w-64 shrink-0 space-y-3 overflow-y-auto border-r border-[var(--al-border)] bg-[var(--al-panel)] p-3">
			{#if project}
				<div>
					<label for="provider-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Provider</label>
					<div class="flex gap-1.5">
						<input id="provider-source" class="min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={providerSource} placeholder="" />
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

				<!-- Structure tree: main + local modules as folders -->
				<div>
					<div class="mb-1 flex items-center justify-between">
						<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Structure</span>
						<button class={iconBtn} title="New module" aria-label="New module" onclick={() => { nmName = ''; showNewModule = true; }}><Plus size={14} /></button>
					</div>

					<!-- main (root) -->
					<button class="flex w-full items-center gap-1.5 rounded px-2 py-1 text-left text-xs {activeModule === null ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => selectScope(null)}>
						<Folder size={12} /> main
						<span class="ml-auto text-[10px]">{project.resources.length}</span>
					</button>
					{#if activeModule === null}
						<ul class="mb-1 ml-3 space-y-0.5 border-l border-[var(--al-border)] pl-2">
							{#each project.resources as r (r.id)}
								<li class="group flex items-center">
									<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px] {editingId === r.id ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}" onclick={() => editResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
									<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteScopedResource(r.id)} aria-label="Delete"><Trash2 size={11} /></button>
								</li>
							{/each}
						</ul>
					{/if}

					<!-- local modules -->
					{#each localModules as m (m.name)}
						<div class="group flex items-center">
							<button class="flex flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs {activeModule === m.name ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => selectScope(m.name)}>
								<FolderGit2 size={12} /> modules/{m.name}
								<span class="ml-auto text-[10px]">{(m.resources ?? []).length}</span>
							</button>
							<button class="px-1 opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteModule(m.name)} aria-label="Delete module"><Trash2 size={12} /></button>
						</div>
						{#if activeModule === m.name}
							<ul class="mb-1 ml-3 space-y-0.5 border-l border-[var(--al-border)] pl-2">
								{#each m.resources ?? [] as r (r.id)}
									<li class="group flex items-center">
										<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px] {editingId === r.id ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}" onclick={() => editResource(r)} title="Edit {r.type}.{r.name}">{r.type}.{r.name}</button>
										<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteScopedResource(r.id)} aria-label="Delete"><Trash2 size={11} /></button>
									</li>
								{/each}
								<li>
									<button class="mt-0.5 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => (showInputs = true)}>⚙ module inputs</button>
								</li>
							</ul>
						{/if}
					{/each}
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
					{#if auth.can('resources')}<button class={btnPrimary + ' ml-auto'} onclick={saveProviderConfig} disabled={busy === 'save'}><Save size={15} /> Save provider config</button>{/if}
				</div>
				{#if (schema.fields?.length ?? 0) === 0}
					<p class="text-sm text-[var(--al-muted)]">This provider has no configurable arguments.</p>
				{:else}
					<SchemaForm {schema} bind:values {references} secrets={secretNames} />
				{/if}
			{:else if schema}
				<div class="mb-4 flex items-end gap-3">
					<div>
						<label for="resource-name" class="mb-1 block text-xs text-[var(--al-muted)]">resource name</label>
						<input id="resource-name" class="mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={resourceName} />
					</div>
					<div class="mono text-sm text-[var(--al-muted)]">resource "<span class="text-[var(--al-accent)]">{selectedType}</span>" "{resourceName}"{#if editingId}<span class="ml-1 text-[10px] uppercase">editing</span>{/if}</div>
					{#if auth.can('resources')}<button class={btnPrimary + ' ml-auto'} onclick={saveResource} disabled={busy === 'save'}><Save size={15} /> {editingId ? 'Update' : 'Save'} resource</button>{/if}
				</div>
				<SchemaForm {schema} bind:values {references} secrets={secretNames} />
			{:else}
				<div class="grid h-full place-items-center text-center text-sm text-[var(--al-muted)]">
					Add a provider and pick a resource type, select an existing resource to edit,<br />or import existing HCL / state.
				</div>
			{/if}
		</main>

		<aside class="flex w-[26rem] shrink-0 flex-col border-l" style="border-color:rgba(255,255,255,0.08);">
			<!-- Generated HCL — shares the graph editor's viewer for a consistent look -->
			<div class="min-h-0 flex-[3]">
				<HclView {hcl}>
					{#snippet right()}
						{#if auth.can('deploy')}
						{@const running = busy === 'plan' || busy === 'apply' || busy === 'destroy'}
						<button class={btnGhost} onclick={runPlan} disabled={!project || running}>
							{#if busy === 'plan'}<Loader2 size={14} class="animate-spin" />{:else}<Play size={14} />{/if} Plan
						</button>
						<button class={btnPrimary} onclick={() => (showApplyConfirm = true)} disabled={!project || running}>
							{#if busy === 'apply'}<Loader2 size={14} class="animate-spin" />{:else}<Rocket size={14} />{/if} Apply
						</button>
						<button
							class="{btn} border border-[var(--al-border)] text-[var(--al-danger)] hover:bg-[var(--al-danger)]/10"
							title="Destroy all deployed resources"
							onclick={() => { destroyName = ''; showDestroyConfirm = true; }}
							disabled={!project || running}
						>
							{#if busy === 'destroy'}<Loader2 size={14} class="animate-spin" />{:else}<Flame size={14} />{/if}
						</button>
						{/if}
					{/snippet}
				</HclView>
			</div>
			<!-- Plan output -->
			<div class="flex min-h-0 flex-[2] flex-col border-t" style="border-color:rgba(255,255,255,0.07);background:#0b0f18;">
				<div class="flex h-11 flex-none items-center border-b px-4" style="border-color:rgba(255,255,255,0.07);">
					<div class="font-mono" style="font-size:12.5px;font-weight:600;color:#c9d3dc;">run output</div>
					{#if planResult}<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:{accentWeak};color:{accentHex};">{planResult}</span>{/if}
					{#if planCounts}
						<span class="ml-auto flex items-center gap-2 font-mono text-[11px] font-semibold">
							<span style="color:{ACTION_COLOR.create}">+{planCounts.add}</span>
							<span style="color:{ACTION_COLOR.update}">~{planCounts.change}</span>
							<span style="color:{ACTION_COLOR.replace}">±{planCounts.replace}</span>
							<span style="color:{ACTION_COLOR.delete}">−{planCounts.destroy}</span>
						</span>
					{/if}
				</div>
				{#if planChanges.length > 0}
					<div class="max-h-[45%] flex-none overflow-auto border-b py-1" style="border-color:rgba(255,255,255,0.07);">
						{#each planChanges as c (c.address)}
							<div class="flex items-center gap-2 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
								<span class="w-3 flex-none text-center font-bold" style="color:{ACTION_COLOR[c.action]}">{ACTION_SYMBOL[c.action]}</span>
								<span class="truncate" style="color:#c9d3dc;" title={c.address}>{c.address}</span>
								<span class="ml-auto flex-none text-[10px]" style="color:{ACTION_COLOR[c.action]}">{c.action}</span>
							</div>
						{/each}
					</div>
				{/if}
				<pre class="flex-1 overflow-auto whitespace-pre-wrap px-[18px] py-3 font-mono" style="font-size:11.5px;line-height:1.6;color:#8b95a5;">{planLog.join('\n') || '// run plan or apply to see output'}</pre>
			</div>
		</aside>
		{/if}
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

{#if auth.isAdmin}
	<SettingsModal bind:open={showSettings} />
{/if}

{#if project}
	<ProjectConfig
		config={scopeConfig}
		title={activeModule ? `Module "${activeModule}" — variables, locals & outputs` : 'Variables, locals & outputs'}
		bind:open={showConfig}
		saving={busy === 'config'}
		onSave={saveConfig}
	/>
	<SecretsModal
		bind:open={showSecrets}
		secrets={secretsList}
		localEnabled={secretsLocalEnabled}
		busy={busy === 'secret'}
		onAdd={addSecret}
		onDelete={deleteSecret}
	/>
	<GitPanel
		bind:open={showGit}
		projectId={project.id}
		onRestored={async (p) => { project = p; await refreshProjects(); await refreshHcl(); }}
	/>

	<!-- Apply confirmation -->
	<Modal title="Apply changes" bind:open={showApplyConfirm}>
		<p class="text-sm text-[var(--al-text)]">
			This runs <span class="mono">{engine || defaultEngine} apply</span> on
			<span class="font-semibold">{project.name}</span> and makes real changes to your infrastructure.
		</p>
		<p class="mt-2 text-xs text-[var(--al-muted)]">
			{#if planResult === 'No changes'}
				The last plan reported no changes.
			{:else if planResult === 'Changes pending'}
				The last plan reported pending changes — review the run output before applying.
			{:else}
				Tip: run a plan first to review exactly what will change.
			{/if}
		</p>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showApplyConfirm = false)}>Cancel</button>
			<button class={btnPrimary} onclick={() => { showApplyConfirm = false; runOp('apply'); }}>
				<Rocket size={14} /> Apply
			</button>
		{/snippet}
	</Modal>

	<!-- Destroy confirmation -->
	<Modal title="Destroy infrastructure" bind:open={showDestroyConfirm}>
		<p class="text-sm text-[var(--al-text)]">
			This runs <span class="mono">{engine || defaultEngine} destroy</span> and
			<span class="font-semibold text-[var(--al-danger)]">deletes every resource this project has deployed</span>.
			It cannot be undone.
		</p>
		<label for="destroy-name" class="mb-1 mt-3 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
			Type the project name ({project.name}) to confirm
		</label>
		<input
			id="destroy-name"
			class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-danger)]"
			bind:value={destroyName}
			placeholder={project.name}
		/>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showDestroyConfirm = false)}>Cancel</button>
			<button
				class="{btn} bg-[var(--al-danger)] text-white hover:brightness-110"
				disabled={destroyName !== project?.name}
				onclick={() => { showDestroyConfirm = false; runOp('destroy'); }}
			>
				<Flame size={14} /> Destroy
			</button>
		{/snippet}
	</Modal>

	<!-- New module modal -->
	<Modal title="New module" bind:open={showNewModule}>
		<label for="nm-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Module name</label>
		<input id="nm-name" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="vpc" bind:value={nmName} onkeydown={(e) => { if (e.key === 'Enter') createModule(); }} />
		<p class="mt-1.5 text-xs text-[var(--al-muted)]">Creates <span class="mono">modules/{nmName || 'name'}/</span> with its own resources, variables, and outputs.</p>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showNewModule = false)}>Cancel</button>
			<button class={btnPrimary} onclick={createModule} disabled={busy === 'module' || !nmName.trim()}>{#if busy === 'module'}<Loader2 size={14} class="animate-spin" />{/if} Create</button>
		{/snippet}
	</Modal>

	<!-- Module inputs modal -->
	{#if activeMod}
		<Modal title={`Module "${activeMod.name}" — inputs from root`} bind:open={showInputs}>
			{#if (activeMod.variables ?? []).length === 0}
				<p class="text-sm text-[var(--al-muted)]">This module has no variables yet. Add variables in the module's Variables panel, then set their values here.</p>
			{:else}
				<p class="mb-2 text-xs text-[var(--al-muted)]">Values the root passes to <span class="mono">module "{activeMod.name}"</span> (raw HCL).</p>
				<div class="space-y-2">
					{#each activeMod.variables ?? [] as v (v.name)}
						<div class="flex items-center gap-2">
							<span class="mono w-32 truncate text-xs text-[var(--al-text)]">{v.name}</span>
							<input class="mono flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder="value (HCL)" bind:value={inputDraft[v.name]} />
						</div>
					{/each}
				</div>
			{/if}
			{#snippet footer()}
				<button class={btnGhost} onclick={() => (showInputs = false)}>Cancel</button>
				<button class={btnPrimary} onclick={submitInputs} disabled={busy === 'inputs'}>{#if busy === 'inputs'}<Loader2 size={14} class="animate-spin" />{/if} Save inputs</button>
			{/snippet}
		</Modal>
	{/if}
{/if}

<!-- Import HCL modal -->
<Modal title="Import existing HCL" bind:open={showImportHcl}>
	<div class="mb-2 flex items-center gap-1.5 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-xs">
		<span class="text-[var(--al-muted)]">Importing into</span>
		<span class="mono font-medium text-[var(--al-text)]">{activeModule ? `module "${activeModule}"` : 'root module'}</span>
	</div>
	<p class="mb-2 text-xs text-[var(--al-muted)]">Select one or more <span class="mono">.tf</span> files, or paste HCL below. Resources, variables, providers and required_providers are reconstructed into the model.</p>
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
