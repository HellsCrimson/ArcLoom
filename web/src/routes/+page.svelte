<script lang="ts">
	import { onMount } from 'svelte';
	import { api, streamPlan, streamOp, DeleteBlockedError, type DriftInfo, type PlanChange, type RunOutput, type StateEntry, type CostSummary, type LintSummary, type PolicySummary, type Environment, type Approval } from '$lib/api/client';
	import RunsPanel from '$lib/components/RunsPanel.svelte';
	import StatePanel from '$lib/components/StatePanel.svelte';
	import type { AttrValue, DriftConfig, Engine, Module, NormalizedSchema, Project, ProjectSummary, ProviderInstance, ProviderUpdate, Resource, ResourceKind } from '$lib/types';
	import SchemaForm from '$lib/form/SchemaForm.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import ProjectConfig from '$lib/components/ProjectConfig.svelte';
	import SecretsModal from '$lib/components/SecretsModal.svelte';
	import GitPanel from '$lib/components/GitPanel.svelte';
	import AclModal from '$lib/components/AclModal.svelte';
	import CostPanel from '$lib/components/CostPanel.svelte';
	import PolicyPanel from '$lib/components/PolicyPanel.svelte';
	import NotifyPanel from '$lib/components/NotifyPanel.svelte';
	import EnvironmentsPanel from '$lib/components/EnvironmentsPanel.svelte';
	import UserMenu from '$lib/components/UserMenu.svelte';
	import SettingsModal from '$lib/components/SettingsModal.svelte';
	import CommandPalette, { type PaletteItem } from '$lib/components/CommandPalette.svelte';
	import ResizeHandle from '$lib/components/ResizeHandle.svelte';
	import { auth } from '$lib/stores/auth.svelte';
	import { history } from '$lib/stores/history.svelte';
	import EditorView from '$lib/editor/EditorView.svelte';
	import HclView from '$lib/editor/HclView.svelte';
	import { ACTION_COLOR, ACTION_SYMBOL } from '$lib/editor/graph';
	import type { ProjectConfig as ProjectConfigT, SecretMeta } from '$lib/types';
	import {
		Play, Save, Loader2, Boxes, Plus, Trash2, FolderGit2, Folder, Settings, FileDown, Database, LayoutDashboard, ChevronDown, KeyRound, GitBranch, Variable as VariableIcon, Rocket, Flame, History, Package, Layers, HardDriveDownload, Undo2, Redo2, ShieldCheck, AlertTriangle, Search, ArrowUp, Ruler, DollarSign, CheckCircle2, Lock, LockOpen, Bell
	} from 'lucide-svelte';

	const LAST_PROJECT_KEY = 'arcloom.lastProject';
	// Right-panel geometry: the HCL/run-output column's width and how much of it
	// the run output takes. Both are user-dragged and remembered per browser.
	const HCL_WIDTH_KEY = 'arcloom.hclWidth';
	const RUN_HEIGHT_KEY = 'arcloom.runHeight';
	const HCL_MIN_WIDTH = 320;
	const RUN_MIN_HEIGHT = 88;

	let hclWidth = $state(416); // 26rem, the previous fixed width
	let runHeight = $state(280);
	let asideHeight = $state(0); // measured, so the run pane can't be dragged past it

	function clampHclWidth(px: number) {
		const max = Math.max(HCL_MIN_WIDTH, window.innerWidth - 520);
		hclWidth = Math.round(Math.min(Math.max(px, HCL_MIN_WIDTH), max));
	}

	function clampRunHeight(px: number) {
		const max = Math.max(RUN_MIN_HEIGHT, (asideHeight || window.innerHeight) - 160);
		runHeight = Math.round(Math.min(Math.max(px, RUN_MIN_HEIGHT), max));
	}

	let engines = $state<Engine[]>([]);
	let defaultEngine = $state('tofu');

	let projects = $state<ProjectSummary[]>([]);
	let project = $state<Project | null>(null);
	let engine = $state('');

	let providerSource = $state('');
	let resourceTypes = $state<string[]>([]);
	let dataSourceTypes = $state<string[]>([]);
	let selectedType = $state('');
	let schema = $state<NormalizedSchema | null>(null);
	// Whether the schema form edits a managed resource or a data source.
	let resourceKind = $state<ResourceKind>('resource');

	// Center form can edit either a resource or a provider config block.
	let formKind = $state<'resource' | 'provider'>('resource');
	let resourceName = $state('example');
	let editingId = $state<string | null>(null);
	let editingProviderKey = $state('');
	// Alias of the provider instance being edited; editable only while creating
	// a new aliased instance (providerAliasNew).
	let editingProviderAlias = $state('');
	let providerAliasNew = $state(false);
	let values = $state<Record<string, AttrValue>>({});
	// count / for_each meta-args (raw HCL expressions, mutually exclusive), and
	// the edited resource's depends_on (managed on the canvas, preserved here).
	let resourceCount = $state('');
	let resourceForEach = $state('');
	let resourceDependsOn = $state<string[] | undefined>(undefined);
	// provider meta-arg ("aws.euwest"); '' = the type's default provider.
	let resourceProvider = $state('');

	let hcl = $state('');
	let planLog = $state<string[]>([]);
	let planResult = $state<string>('');
	let planChanges = $state<PlanChange[]>([]);
	let planCounts = $state<{ add: number; change: number; destroy: number; replace: number } | null>(null);
	let planCost = $state<CostSummary | null>(null);
	let planLint = $state<LintSummary | null>(null);
	let planPolicy = $state<PolicySummary | null>(null);
	let applyOutputs = $state<RunOutput[]>([]);
	let showApplyConfirm = $state(false);
	let showDestroyConfirm = $state(false);
	let destroyName = $state('');
	let showRuns = $state(false);
	// State panel + resource import
	let showState = $state(false);
	let stateEntries = $state<StateEntry[]>([]);
	let stateLoading = $state(false);
	let driftInfo = $state<DriftInfo | null>(null);
	// Deployment environments (dev/staging/prod) + the currently selected one
	// (''=default workspace). Loaded per project; scopes deploy/state ops.
	let environments = $state<Environment[]>([]);
	let activeEnv = $state('');
	let showEnvironments = $state(false);
	// Plan → approve → apply gate (per project + active env).
	let approvalRequired = $state(false);
	let currentApproval = $state<Approval | null>(null);
	// Provider upgrade suggestions, keyed by source (best-effort registry check).
	let providerUpdates = $state<Record<string, ProviderUpdate>>({});
	let bumping = $state('');
	let showImportResource = $state(false);
	let importId = $state('');
	// Backend (remote state) modal
	let showBackend = $state(false);
	let bkType = $state('');
	let bkRows = $state<{ key: string; expr: string }[]>([]);
	let bkMode = $state<'migrate' | 'reconfigure'>('migrate');
	const BACKEND_TYPES = ['local', 's3', 'gcs', 'azurerm', 'http', 'pg', 'consul', 'kubernetes', 'oss', 'cos', 'remote'];
	let showDeleteBlocked = $state(false);
	let deleteBlockedCount = $state(0);
	let deleteBlockedName = $state('');

	let busy = $state<string>('');
	let error = $state('');

	// Import + graph UI
	let showImportHcl = $state(false);
	let importHclText = $state('');
	let importHclFiles = $state<Record<string, string>>({});
	let showImportState = $state(false);
	let importStateText = $state('');
	// replaceState allows a state import to overwrite a workspace that already
	// tracks resources (the server refuses otherwise — it would orphan them).
	let replaceState = $state(false);
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
	// Per-project access list (admin)
	let showAcl = $state(false);
	let showCost = $state(false);
	let showPolicy = $state(false);
	let showNotify = $state(false);

	// Secrets
	let showSecrets = $state(false);
	let showPalette = $state(false);
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
	const savedDataSources = $derived(activeMod ? (activeMod.dataSources ?? []) : (project?.dataSources ?? []));
	const providers = $derived(project?.providers ?? []);
	// A stable fingerprint of provider sources+pins; when it changes (project
	// switch, add/edit/import/undo, version bump) we re-run the registry check.
	const providerSig = $derived(providers.map((p) => `${p.source}@${p.version ?? ''}`).sort().join(','));
	$effect(() => {
		providerSig; // track
		if (project) void loadProviderUpdates();
	});
	// Instances of the provider the form's resource type belongs to; a provider
	// picker is shown when the source has more than one instance (aliases).
	const formProviderInstances = $derived(
		providers.filter((p) => p.key === selectedType.split('_')[0])
	);
	const references = $derived.by(() => {
		const refs: string[] = [];
		for (const r of savedResources) refs.push(`${r.type}.${r.name}.id`, `${r.type}.${r.name}.result`);
		for (const d of savedDataSources) refs.push(`data.${d.type}.${d.name}.id`);
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

	// Undo/redo: JSON.stringify deep-reads the project, so this effect re-runs
	// on any model change (every mutation path reassigns or mutates `project`).
	// The store dedupes identical snapshots and resets when the id changes. A
	// genuine edit also invalidates the plan overlay — the canvas badges and
	// diff list describe a model that no longer exists.
	$effect(() => {
		if (!project) {
			history.clear();
			return;
		}
		if (history.observe(project.id, JSON.stringify(project)) === 'recorded') {
			planChanges = [];
			planCounts = null;
			planCost = null;
			planLint = null;
			planPolicy = null;
			// A model edit makes any approved plan stale: block a gated apply until
			// the user re-plans and re-approves.
			currentApproval = null;
		}
	});

	async function undo() {
		if (busy || !project || !auth.can('resources')) return;
		await restoreSnapshot(history.undo(), () => history.redo());
	}

	async function redo() {
		if (busy || !project || !auth.can('resources')) return;
		await restoreSnapshot(history.redo(), () => history.undo());
	}

	// restoreSnapshot replaces the whole model with a history snapshot; revert
	// un-moves the history pointer if the server call fails.
	async function restoreSnapshot(snap: string | null, revert: () => void) {
		if (!snap || !project) return;
		busy = 'restore';
		error = '';
		history.beginApply();
		let restored = false;
		try {
			project = await api.replaceProject(project.id, JSON.parse(snap));
			// The restored model may invalidate what is on screen.
			if (activeModule && !project.localModules?.some((m) => m.name === activeModule)) {
				activeModule = null;
			}
			planChanges = [];
			planCounts = null;
			planCost = null;
			planLint = null;
			planPolicy = null;
			await refreshProjects();
			await refreshHcl();
			restored = true;
		} catch (err) {
			revert();
			error = (err as Error).message;
		} finally {
			history.endApply();
			busy = '';
		}
		if (!restored) return;
		// Re-open (or close) the form so it can't show pre-restore values.
		if (formKind === 'resource' && editingId) {
			const found = findInScope(editingId, activeModule ?? undefined);
			if (found) await editResource(found.r, found.kind);
			else resetFormOnly();
		} else if (formKind === 'provider' && editingProviderKey) {
			const prov = project.providers.find(
				(p) => p.key === editingProviderKey && (p.alias ?? '') === editingProviderAlias
			);
			if (prov && !providerAliasNew) await configureProvider(prov);
			else if (!prov) resetFormOnly();
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
		const k = e.key.toLowerCase();
		// Ctrl+K opens the command palette from anywhere, including inputs.
		if (k === 'k') {
			e.preventDefault();
			showPalette = !showPalette;
			return;
		}
		const t = e.target as HTMLElement | null;
		// Leave native undo alone inside text inputs.
		if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)) return;
		if (k === 'enter') {
			// Ctrl+Enter runs a plan (matches the palette hint).
			if (project && auth.can('deploy') && busy === '') {
				e.preventDefault();
				runPlan();
			}
		} else if (k === 'z' && !e.shiftKey) {
			e.preventDefault();
			undo();
		} else if ((k === 'z' && e.shiftKey) || k === 'y') {
			e.preventDefault();
			redo();
		}
	}

	// Command palette entries: permission-filtered commands plus every resource
	// across all scopes (global search; selecting one opens its form).
	const paletteItems = $derived.by<PaletteItem[]>(() => {
		const items: PaletteItem[] = [];
		const resItem = (addr: string): PaletteItem => ({
			id: `res:${addr}`,
			label: addr,
			group: 'resources',
			mono: true,
			run: () => jumpToAddress(addr)
		});
		if (project) {
			items.push({ id: 'cmd:editor', label: 'Open visual editor', group: 'commands', run: () => { error = ''; showEditor = true; } });
			items.push({ id: 'cmd:vars', label: 'Variables, locals & outputs', group: 'commands', run: () => (showConfig = true) });
			items.push({ id: 'cmd:secrets', label: 'Secrets', group: 'commands', run: () => { loadSecrets(); showSecrets = true; } });
			if (auth.can('git')) items.push({ id: 'cmd:git', label: 'Git', group: 'commands', run: () => (showGit = true) });
			items.push({ id: 'cmd:runs', label: 'Run history', group: 'commands', run: () => (showRuns = true) });
			items.push({ id: 'cmd:state', label: 'State & drift', group: 'commands', run: () => { loadState(); loadDrift(); showState = true; } });
			if (auth.can('deploy')) {
				items.push({ id: 'cmd:plan', label: 'Plan', hint: 'Ctrl+Enter', group: 'commands', run: runPlan });
				items.push({ id: 'cmd:validate', label: 'Validate', group: 'commands', run: runValidate });
				items.push({ id: 'cmd:lint', label: 'Lint (tflint)', group: 'commands', run: runLint });
				items.push({ id: 'cmd:apply', label: 'Apply…', group: 'commands', run: () => (showApplyConfirm = true) });
				items.push({ id: 'cmd:destroy', label: 'Destroy…', group: 'commands', run: () => { destroyName = ''; showDestroyConfirm = true; } });
			}
			if (auth.can('resources')) {
				items.push({ id: 'cmd:cost', label: 'Cost estimation', group: 'commands', run: () => (showCost = true) });
				items.push({ id: 'cmd:policy', label: 'Policy as code', group: 'commands', run: () => (showPolicy = true) });
				items.push({ id: 'cmd:notify', label: 'Notifications', group: 'commands', run: () => (showNotify = true) });
				items.push({ id: 'cmd:environments', label: 'Environments', group: 'commands', run: () => (showEnvironments = true) });
				items.push({ id: 'cmd:import-hcl', label: 'Import HCL', group: 'commands', run: () => { importMsg = ''; showImportHcl = true; } });
				items.push({ id: 'cmd:import-state', label: 'Import state', group: 'commands', run: () => { importMsg = ''; showImportState = true; } });
			}
		}
		if (auth.can('resources')) items.push({ id: 'cmd:new-project', label: 'New project', group: 'commands', run: newProject });
		if (auth.isAdmin) items.push({ id: 'cmd:settings', label: 'Settings', group: 'commands', run: () => (showSettings = true) });
		if (auth.isAdmin && project) items.push({ id: 'cmd:access', label: 'Project access', group: 'commands', run: () => (showAcl = true) });
		if (project) {
			for (const r of project.resources) items.push(resItem(`${r.type}.${r.name}`));
			for (const d of project.dataSources ?? []) items.push(resItem(`data.${d.type}.${d.name}`));
			for (const lm of project.localModules ?? []) {
				for (const r of lm.resources) items.push(resItem(`module.${lm.name}.${r.type}.${r.name}`));
				for (const d of lm.dataSources ?? []) items.push(resItem(`module.${lm.name}.data.${d.type}.${d.name}`));
			}
			for (const m of registryModules) {
				items.push({ id: `res:module.${m.name}`, label: `module.${m.name}`, hint: m.source, group: 'resources', mono: true, run: () => openRegModule(m) });
			}
		}
		return items;
	});

	onMount(async () => {
		// Restore the dragged panel geometry before the first paint of the aside.
		const savedWidth = Number(localStorage.getItem(HCL_WIDTH_KEY));
		if (savedWidth) clampHclWidth(savedWidth);
		const savedRun = Number(localStorage.getItem(RUN_HEIGHT_KEY));
		if (savedRun) clampRunHeight(savedRun);
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

	// Poll the project list so scheduled drift checks show up as badges without
	// a reload. Registered outside the async onMount (whose cleanup is ignored).
	onMount(() => {
		const iv = setInterval(() => {
			if (auth.user || !auth.authEnabled) refreshProjects().catch(() => {});
		}, 60_000);
		return () => clearInterval(iv);
	});

	async function selectProject(id: string) {
		error = '';
		resetEditor();
		try {
			project = await api.getProject(id);
			engine = project.engine || defaultEngine;
			localStorage.setItem(LAST_PROJECT_KEY, id);
			await refreshHcl();
			await loadSecrets();
			await loadEnvironments();
			await loadApproval();
			if (project.providers?.length) providerSource = project.providers[0].source;
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// loadEnvironments fetches the project's deployment environments and keeps the
	// active selection valid (falls back to the default workspace if it vanished).
	async function loadEnvironments() {
		if (!project) return;
		try {
			environments = await api.getEnvironments(project.id);
		} catch {
			environments = [];
		}
		if (activeEnv && !environments.some((e) => e.name === activeEnv)) activeEnv = '';
	}

	// selectEnv switches the active environment. The plan overlay and state list
	// are per-env, so clear/refresh them to avoid showing another env's diff.
	function selectEnv(name: string) {
		if (name === activeEnv) return;
		activeEnv = name;
		planChanges = [];
		planCounts = null;
		planCost = null;
		planResult = '';
		if (showState) loadState();
		loadApproval();
	}

	// loadApproval fetches the approval-gate state for the active environment.
	async function loadApproval() {
		if (!project) {
			approvalRequired = false;
			currentApproval = null;
			return;
		}
		try {
			const st = await api.getApproval(project.id, activeEnv);
			approvalRequired = st.required;
			currentApproval = st.approval;
		} catch {
			approvalRequired = false;
			currentApproval = null;
		}
	}

	// approvePlan snapshots the current saved plan as an approval so a gated apply
	// can run it. Requires the approve permission (server-enforced).
	async function approvePlan() {
		if (!project) return;
		error = '';
		try {
			const s = planCounts ?? { add: 0, change: 0, destroy: 0, replace: 0 };
			currentApproval = await api.approve(project.id, activeEnv, s);
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// toggleApprovalGate turns the per-project apply approval requirement on/off
	// (resources permission).
	async function toggleApprovalGate() {
		if (!project) return;
		error = '';
		try {
			const st = await api.setApprovalPolicy(project.id, !approvalRequired);
			approvalRequired = st.required;
			currentApproval = st.approval;
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// A pending approval for the active env means a gated apply is unblocked.
	const approvalReady = $derived(!approvalRequired || (!!currentApproval && !currentApproval.consumed));

	// loadProviderUpdates checks each provider source against the registry and
	// keys the result by source for the sidebar badge. Best-effort: any failure
	// just leaves the map empty (offline servers show no upgrade hints).
	async function loadProviderUpdates() {
		if (!project) return;
		try {
			const ups = await api.providerUpdates(project.id);
			const map: Record<string, ProviderUpdate> = {};
			for (const u of ups) map[u.source] = u;
			providerUpdates = map;
		} catch {
			providerUpdates = {};
		}
	}

	// bumpProvider pins a provider instance to the latest available version and
	// re-checks so the badge clears once the pin catches up.
	async function bumpProvider(prov: ProviderInstance, latest: string) {
		if (!project) return;
		error = '';
		bumping = prov.source;
		try {
			project = await api.setProviderConfig(project.id, prov.key, {
				alias: prov.alias || undefined,
				source: prov.source,
				version: latest,
				config: prov.config ?? {}
			});
			await refreshHcl();
			// The provider-signature $effect re-checks once the new pin lands.
		} catch (err) {
			error = (err as Error).message;
		} finally {
			bumping = '';
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

	async function afterProjectDeleted(id: string) {
		if (localStorage.getItem(LAST_PROJECT_KEY) === id) localStorage.removeItem(LAST_PROJECT_KEY);
		project = null;
		resetEditor();
		await refreshProjects();
		if (projects[0]) await selectProject(projects[0].id);
	}

	async function removeProject() {
		if (!project) return;
		if (!confirm(`Delete project "${project.name}"? This removes its folder on disk.`)) return;
		const id = project.id;
		try {
			await api.deleteProject(id);
			await afterProjectDeleted(id);
		} catch (err) {
			if (err instanceof DeleteBlockedError) {
				deleteBlockedCount = err.deployed;
				deleteBlockedName = '';
				showDeleteBlocked = true;
				return;
			}
			error = (err as Error).message;
		}
	}

	async function forceRemoveProject() {
		if (!project) return;
		const id = project.id;
		showDeleteBlocked = false;
		try {
			await api.deleteProject(id, true);
			await afterProjectDeleted(id);
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function resetEditor() {
		schema = null;
		selectedType = '';
		editingId = null;
		editingProviderKey = '';
		editingProviderAlias = '';
		providerAliasNew = false;
		formKind = 'resource';
		values = {};
		resourceName = 'example';
		hcl = '';
		planLog = [];
		planResult = '';
		planChanges = [];
		planCounts = null;
		planCost = null;
		planLint = null;
		planPolicy = null;
		applyOutputs = [];
		resourceTypes = [];
		dataSourceTypes = [];
		resourceKind = 'resource';
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

	async function deleteScopedResource(rid: string, kind: ResourceKind = 'resource') {
		if (!project) return;
		try {
			await api.deleteResource(project.id, rid, activeModule ?? undefined, kind);
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
		editingProviderKey = '';
		editingProviderAlias = '';
		providerAliasNew = false;
		formKind = 'resource';
		values = {};
	}

	// attrToText renders an AttrValue as editable raw-expression text (a
	// reference or JSON literal is also a valid HCL expression).
	function attrToText(av: AttrValue | undefined): string {
		if (!av) return '';
		return av.mode === 'expression' ? (av.expr ?? '') : av.mode === 'reference' ? (av.ref ?? '') : av.mode === 'literal' ? JSON.stringify(av.literal ?? '') : '';
	}

	// Draft values for the module-inputs modal (raw HCL text per variable).
	let inputDraft = $state<Record<string, string>>({});
	$effect(() => {
		if (showInputs && activeMod) {
			const d: Record<string, string> = {};
			for (const v of activeMod.variables ?? []) {
				d[v.name] = attrToText(activeMod.inputs?.[v.name]);
			}
			inputDraft = d;
		}
	});

	// ---- registry/external modules ----
	const registryModules = $derived(project?.modules ?? []);
	let showRegModule = $state(false);
	let rmEditing = $state<string | null>(null); // original name when editing
	let rmName = $state('');
	let rmSource = $state('');
	let rmVersion = $state('');
	let rmRows = $state<{ key: string; expr: string }[]>([]);

	function openRegModule(m?: Module) {
		rmEditing = m?.name ?? null;
		rmName = m?.name ?? '';
		rmSource = m?.source ?? '';
		rmVersion = m?.version ?? '';
		rmRows = m?.config ? Object.entries(m.config).map(([key, av]) => ({ key, expr: attrToText(av) })) : [];
		showRegModule = true;
	}

	async function saveRegModule() {
		if (!project || !rmName.trim() || !rmSource.trim()) return;
		busy = 'regmodule';
		error = '';
		try {
			const config: Record<string, AttrValue> = {};
			for (const row of rmRows) {
				const k = row.key.trim();
				const e = row.expr.trim();
				if (k && e) config[k] = { mode: 'expression', expr: e };
			}
			// Upsert keys on name, so a rename must drop the old call first.
			if (rmEditing && rmEditing !== rmName.trim()) {
				await api.deleteRegistryModule(project.id, rmEditing);
			}
			project = await api.setRegistryModule(project.id, {
				name: rmName.trim(),
				source: rmSource.trim(),
				version: rmVersion.trim() || undefined,
				config
			});
			await refreshProjects();
			await refreshHcl();
			showRegModule = false;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function deleteRegModule(name: string) {
		if (!project) return;
		if (!confirm(`Remove module "${name}" from the project?`)) return;
		try {
			project = await api.deleteRegistryModule(project.id, name);
			await refreshProjects();
			await refreshHcl();
		} catch (err) {
			error = (err as Error).message;
		}
	}

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
			const res = await api.addProvider(providerSource, engine, providerVersionForSource(providerSource));
			resourceTypes = res.resourceTypes;
			dataSourceTypes = res.dataSourceTypes ?? [];
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function pickType(t: string, kind: ResourceKind = 'resource') {
		error = '';
		busy = 'schema';
		formKind = 'resource';
		resourceKind = kind;
		selectedType = t;
		editingId = null;
		values = {};
		resourceCount = '';
		resourceForEach = '';
		resourceDependsOn = undefined;
		resourceProvider = '';
		resourceName = suggestName(t, kind);
		try {
			const ver = providerVersionForSource(providerSource);
			schema = kind === 'data'
				? await api.dataSourceSchema(providerSource, t, engine, ver)
				: await api.resourceSchema(providerSource, t, engine, ver);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function editResource(r: Resource, kind: ResourceKind = 'resource') {
		error = '';
		busy = 'schema';
		formKind = 'resource';
		resourceKind = kind;
		selectedType = r.type;
		editingId = r.id;
		resourceName = r.name;
		values = JSON.parse(JSON.stringify(r.attrs ?? {}));
		resourceCount = r.count ?? '';
		resourceForEach = r.forEach ?? '';
		resourceDependsOn = r.dependsOn;
		resourceProvider = r.provider ?? '';
		const src = providerForType(r.type);
		try {
			const ver = providerVersionForSource(src);
			schema = kind === 'data'
				? await api.dataSourceSchema(src, r.type, engine, ver)
				: await api.resourceSchema(src, r.type, engine, ver);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	// Edit a provider instance's configuration block (host, api_key, ...).
	async function configureProvider(prov: ProviderInstance) {
		error = '';
		busy = 'schema';
		formKind = 'provider';
		editingProviderKey = prov.key;
		editingProviderAlias = prov.alias ?? '';
		providerAliasNew = false;
		providerSource = prov.source;
		editingId = null;
		selectedType = '';
		try {
			schema = await api.providerSchema(prov.source, engine, prov.version);
			values = JSON.parse(JSON.stringify(prov.config ?? {}));
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	// Start a new aliased instance of an existing provider (multi-region etc.):
	// same schema form, empty config, editable alias.
	async function addProviderAlias(prov: ProviderInstance) {
		await configureProvider(prov);
		editingProviderAlias = '';
		providerAliasNew = true;
		values = {};
	}

	async function deleteProviderInstance(prov: ProviderInstance) {
		if (!project) return;
		const addr = prov.alias ? `${prov.key}.${prov.alias}` : prov.key;
		if (!confirm(`Remove provider instance "${addr}" from the project?`)) return;
		error = '';
		try {
			project = await api.deleteProviderInstance(project.id, prov.key, prov.alias);
			await refreshHcl();
			if (formKind === 'provider' && editingProviderKey === prov.key && editingProviderAlias === (prov.alias ?? '')) resetFormOnly();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function providerForType(type: string): string {
		const key = type.split('_')[0];
		return project?.providers?.find((p) => p.key === key)?.source ?? providerSource;
	}

	// providerVersionForSource returns the version constraint the project pins
	// a provider source to, so schema fetches match the pinned version.
	function providerVersionForSource(source: string): string | undefined {
		return project?.providers?.find((p) => p.source === source && p.version)?.version;
	}

	function suggestName(type: string, kind: ResourceKind = 'resource'): string {
		const base = type.replace(/^[a-z0-9]+_/, '') || type;
		let n = base;
		let i = 1;
		const pool = kind === 'data' ? savedDataSources : savedResources;
		const taken = new Set(pool.filter((r) => r.type === type).map((r) => r.name));
		while (taken.has(n)) n = `${base}_${++i}`;
		return n;
	}

	// cleanAttr prunes an AttrValue to what should be sent: unset scalars, empty
	// collections, and incomplete references/secrets/expressions collapse to
	// undefined so they're dropped. Recurses into structural collections so a
	// nested reference/secret is preserved but an empty nested slot is not.
	function cleanAttr(v: AttrValue): AttrValue | undefined {
		if (v.mode === 'reference') return v.ref ? { mode: 'reference', ref: v.ref } : undefined;
		if (v.mode === 'expression') return v.expr ? { mode: 'expression', expr: v.expr } : undefined;
		if (v.mode === 'secret') return v.secretRef ? { mode: 'secret', secretRef: v.secretRef } : undefined;
		// literal
		if (v.items !== undefined) {
			const items = v.items.map(cleanAttr).filter((x): x is AttrValue => x !== undefined);
			return items.length ? { mode: 'literal', items } : undefined;
		}
		if (v.entries !== undefined) {
			const entries = v.entries
				.map((e) => ({ key: e.key.trim(), value: cleanAttr(e.value) }))
				.filter((e): e is { key: string; value: AttrValue } => e.key !== '' && e.value !== undefined);
			return entries.length ? { mode: 'literal', entries } : undefined;
		}
		const lit = v.literal;
		if (lit === undefined || lit === null || lit === '') return undefined;
		return { mode: 'literal', literal: lit };
	}

	function buildAttrs(): Record<string, AttrValue> {
		const out: Record<string, AttrValue> = {};
		for (const [name, v] of Object.entries(values)) {
			const cleaned = cleanAttr(v);
			if (cleaned) out[name] = cleaned;
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
				kind: resourceKind,
				type: selectedType,
				name: resourceName,
				providerSource,
				module: activeModule ?? undefined,
				attrs: buildAttrs(),
				dependsOn: resourceDependsOn,
				count: resourceCount.trim() || undefined,
				forEach: resourceForEach.trim() || undefined,
				provider: resourceProvider || undefined
			});
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			const scope = activeModule ? project.localModules?.find((m) => m.name === activeModule) : project;
			const scopeRes = (resourceKind === 'data' ? scope?.dataSources : scope?.resources) ?? [];
			editingId = editingId ?? scopeRes.find((r) => r.name === resourceName && r.type === selectedType)?.id ?? null;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function saveProviderConfig() {
		if (!project) return;
		if (providerAliasNew && !editingProviderAlias.trim()) {
			error = 'alias is required for an additional provider instance';
			return;
		}
		error = '';
		busy = 'save';
		try {
			project = await api.setProviderConfig(project.id, editingProviderKey, {
				alias: editingProviderAlias.trim() || undefined,
				source: providerSource,
				config: buildAttrs()
			});
			editingProviderAlias = editingProviderAlias.trim();
			providerAliasNew = false;
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
				res.addedDataSources ? `${res.addedDataSources} data sources` : '',
				res.addedVariables ? `${res.addedVariables} variables` : '',
				res.addedLocals ? `${res.addedLocals} locals` : '',
				res.addedOutputs ? `${res.addedOutputs} outputs` : '',
				res.addedLocalModules ? `${res.addedLocalModules} local modules` : '',
				res.addedModules ? `${res.addedModules} registry modules` : '',
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
			const res = await api.importState(project.id, importStateText, activeEnv, replaceState);
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			// The import also adopted the state, so the State panel / plan overlay
			// must not keep showing the pre-import picture.
			await loadState();
			planChanges = [];
			planCounts = null;
			const parts = [`${res.addedResources} resources`];
			// Matched = the state described resources the model already had (the usual
			// "import HCL, then import its state" flow), so nothing was duplicated.
			if (res.matchedResources) parts.push(`${res.matchedResources} matched existing`);
			if (res.adoptedState) parts.push(`state adopted (${res.adoptedState} tracked)`);
			if (res.warnings?.length) parts.push(`${res.warnings.length} warning(s)`);
			importMsg = `Imported ${parts.join(' · ')}`;
			showImportState = false;
			importStateText = '';
			replaceState = false;
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

	// Returns a provider's resource and data source types for the editor palette.
	async function editorLoadTypes(source: string): Promise<{ resourceTypes: string[]; dataSourceTypes: string[] }> {
		const res = await api.addProvider(source, engine, providerVersionForSource(source));
		return { resourceTypes: res.resourceTypes, dataSourceTypes: res.dataSourceTypes ?? [] };
	}

	// Palette "add": leave the editor and open the schema form for a new
	// resource or data source.
	async function editorAddType(type: string, source: string, kind: ResourceKind = 'resource') {
		showEditor = false;
		providerSource = source;
		await pickType(type, kind);
	}

	// scopeResources returns the resource list for a scope (module name or root).
	function scopeResources(moduleName?: string): Resource[] {
		if (!project) return [];
		return moduleName
			? (project.localModules?.find((m) => m.name === moduleName)?.resources ?? [])
			: project.resources;
	}

	function scopeDataSources(moduleName?: string): Resource[] {
		if (!project) return [];
		return moduleName
			? (project.localModules?.find((m) => m.name === moduleName)?.dataSources ?? [])
			: (project.dataSources ?? []);
	}

	// findInScope resolves a node id to the full resource and whether it is a
	// managed resource or a data source (canvas callbacks only carry the id).
	function findInScope(id: string, moduleName?: string): { r: Resource; kind: ResourceKind } | null {
		const r = scopeResources(moduleName).find((x) => x.id === id);
		if (r) return { r, kind: 'resource' };
		const d = scopeDataSources(moduleName).find((x) => x.id === id);
		if (d) return { r: d, kind: 'data' };
		return null;
	}

	// Clicking a node opens it in the schema form (in the node's own scope). The
	// canvas passes a shallow node, so we resolve the full resource (with attrs).
	function editorEditResource(r: Resource, moduleName?: string) {
		const found = findInScope(r.id, moduleName);
		activeModule = moduleName ?? null;
		showEditor = false;
		editResource(found?.r ?? r, found?.kind ?? 'resource');
	}

	// jumpToAddress resolves a plan/diff address to its model block and opens the
	// schema form in the right scope. Instance indexes ("[0]", '["a"]') are
	// stripped; a registry-module interior address opens the module's modal.
	// Returns false when nothing matches (e.g. a planned delete already gone
	// from the model).
	function jumpToAddress(address: string): boolean {
		if (!project) return false;
		let addr = address.replace(/\[[^\]]*\]/g, '');
		let moduleName: string | undefined;
		const m = addr.match(/^module\.([A-Za-z0-9_-]+)\.(.+)$/);
		if (m) {
			moduleName = m[1];
			addr = m[2];
		}
		if (addr.startsWith('module.')) return false; // nested modules aren't in the model
		const kind: ResourceKind = addr.startsWith('data.') ? 'data' : 'resource';
		const [type, name] = addr.replace(/^data\./, '').split('.');
		if (!type || !name) return false;
		if (moduleName && !project.localModules?.some((lm) => lm.name === moduleName)) {
			const reg = registryModules.find((rm) => rm.name === moduleName);
			if (reg) {
				openRegModule(reg);
				return true;
			}
			return false;
		}
		const pool = kind === 'data' ? scopeDataSources(moduleName) : scopeResources(moduleName);
		const r = pool.find((x) => x.type === type && x.name === name);
		if (!r) return false;
		activeModule = moduleName ?? null;
		showEditor = false;
		editResource(r, kind);
		return true;
	}

	async function editorDeleteResource(id: string, moduleName?: string) {
		if (!project) return;
		try {
			await api.deleteResource(project.id, id, moduleName, findInScope(id, moduleName)?.kind);
			await refreshProject();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// Re-save an existing resource or data source with a changed field (name or
	// depends_on), preserving its type/attrs, within its scope.
	async function resaveResource(id: string, patch: { name?: string; dependsOn?: string[] }, moduleName?: string) {
		if (!project) return;
		const found = findInScope(id, moduleName);
		if (!found) return;
		const r = found.r;
		try {
			await api.upsertResource(project.id, {
				id: r.id,
				kind: found.kind,
				type: r.type,
				name: patch.name ?? r.name,
				providerSource: project.providers.find((p) => p.key === r.type.split('_')[0])?.source,
				module: moduleName,
				attrs: r.attrs,
				dependsOn: patch.dependsOn ?? r.dependsOn,
				count: r.count,
				forEach: r.forEach,
				provider: r.provider
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
		planCost = null;
		planLint = null;
		planPolicy = null;
		applyOutputs = [];
		busy = 'plan';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'result') {
				planResult = ev.hasChanges ? 'Changes pending' : 'No changes';
				planChanges = ev.changes ?? [];
				planCounts = ev.hasChanges
					? { add: ev.add ?? 0, change: ev.change ?? 0, destroy: ev.destroy ?? 0, replace: ev.replace ?? 0 }
					: null;
				planCost = ev.cost ?? null;
				planPolicy = ev.policy ?? null;
			} else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') {
				busy = '';
				if (approvalRequired) loadApproval(); // the new plan superseded any approval
			}
		}, 'plan', activeEnv);
	}

	// runValidate streams a config validation into the run pane (diagnostics
	// arrive as log lines; the result carries the counts).
	function runValidate() {
		if (!project) return;
		error = '';
		planLog = [];
		planResult = '';
		planChanges = [];
		planCounts = null;
		planCost = null;
		planLint = null;
		planPolicy = null;
		applyOutputs = [];
		busy = 'validate';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'result') {
				planResult = ev.valid
					? `Valid${ev.warningCount ? ` · ${ev.warningCount} warning(s)` : ''}`
					: `${ev.errorCount ?? 0} validation error(s)`;
			} else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') busy = '';
		}, 'validate', activeEnv);
	}

	// runLint streams a tflint run into the run pane (issues arrive as log lines;
	// the result carries the structured findings + counts).
	function runLint() {
		if (!project) return;
		error = '';
		planLog = [];
		planResult = '';
		planChanges = [];
		planCounts = null;
		planCost = null;
		planLint = null;
		planPolicy = null;
		applyOutputs = [];
		busy = 'lint';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'result') {
				planLint = ev.lint ?? null;
				if (ev.available === false) planResult = 'tflint not installed';
				else if ((ev.errorCount ?? 0) > 0) planResult = `${ev.errorCount} lint error(s)`;
				else if ((ev.warningCount ?? 0) > 0) planResult = `${ev.warningCount} lint warning(s)`;
				else planResult = 'No lint issues';
			} else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') busy = '';
		}, 'lint', activeEnv);
	}

	async function loadState() {
		if (!project) return;
		stateLoading = true;
		try {
			stateEntries = await api.stateList(project.id, activeEnv);
		} catch {
			stateEntries = [];
		} finally {
			stateLoading = false;
		}
	}

	async function loadDrift() {
		if (!project) return;
		try {
			driftInfo = await api.getDrift(project.id);
		} catch {
			driftInfo = null;
		}
	}

	async function saveDrift(cfg: DriftConfig) {
		if (!project) return;
		error = '';
		try {
			await api.setDrift(project.id, cfg);
			await loadDrift();
			await refreshProjects(); // picker badge follows the schedule state
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// Latest scheduled drift check for the selected project (from the picker
	// list, which polls), for the header badge.
	const currentDrift = $derived(projects.find((p) => p.id === project?.id)?.drift);

	// runStateOp streams a state-surgery operation (import / rm / mv /
	// init-backend) into the run pane and refreshes the state list when it
	// settles.
	function runStateOp(
		op: 'import' | 'state-rm' | 'state-mv' | 'init-backend' | 'accept-drift',
		params: Record<string, string>
	) {
		if (!project) return;
		error = '';
		planLog = [];
		planResult = '';
		planChanges = [];
		planCounts = null;
		planCost = null;
		planLint = null;
		planPolicy = null;
		applyOutputs = [];
		busy = op;
		streamOp(project.id, op, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
				loadState();
			} else if (ev.kind === 'done') {
				planResult =
					op === 'import' ? 'Imported into state'
					: op === 'state-rm' ? 'Removed from state'
					: op === 'state-mv' ? 'State address moved'
					: op === 'accept-drift' ? 'Drift absorbed into state'
					: 'Backend initialized';
				busy = '';
				loadState();
				// Accepting drift rewrote the state the drift status described.
				if (op === 'accept-drift') {
					loadDrift();
					refreshProjects();
				}
			}
		}, params, activeEnv);
	}

	function openBackend() {
		bkType = project?.backend?.type ?? '';
		bkRows = project?.backend?.config
			? Object.entries(project.backend.config).map(([key, av]) => ({ key, expr: attrToText(av) }))
			: [];
		bkMode = 'migrate';
		showState = false;
		showBackend = true;
	}

	// Saving the backend updates the model, then (with deploy permission)
	// immediately re-initializes the workspace so the change takes effect —
	// migrating the existing state or reconfiguring, per the chosen mode.
	async function saveBackend() {
		if (!project) return;
		busy = 'backend';
		error = '';
		try {
			const config: Record<string, AttrValue> = {};
			for (const row of bkRows) {
				const k = row.key.trim();
				const e = row.expr.trim();
				if (k && e) config[k] = { mode: 'expression', expr: e };
			}
			project = await api.setBackend(project.id, { type: bkType.trim(), config });
			await refreshHcl();
			showBackend = false;
			busy = '';
			if (auth.can('deploy')) {
				runStateOp('init-backend', { migrate: bkMode === 'migrate' ? 'true' : 'false' });
			}
		} catch (err) {
			error = (err as Error).message;
			busy = '';
		}
	}

	// Address of the resource currently in the form, for `tofu import`
	// (managed resources only — data sources are read, never imported).
	const importAddress = $derived(
		`${activeModule ? `module.${activeModule}.` : ''}${selectedType}.${resourceName}`
	);

	function runImport() {
		showImportResource = false;
		runStateOp('import', { address: importAddress, id: importId.trim() });
	}

	// Apply/destroy run against the project's persisted state, so a later plan
	// reflects what is really deployed.
	function runOp(op: 'apply' | 'destroy') {
		if (!project) return;
		// Enforce the approval gate client-side too (the server also 409s), so a
		// disabled Apply gives a clear message instead of a failed request.
		if (op === 'apply' && approvalRequired && !approvalReady) {
			error = 'This project requires an approved plan before applying. Run a plan, then approve it.';
			return;
		}
		error = '';
		planLog = [];
		planResult = '';
		planChanges = [];
		planCounts = null;
		planCost = null;
		planLint = null;
		planPolicy = null;
		applyOutputs = [];
		busy = op;
		streamOp(project.id, op, (ev) => {
			if (ev.kind === 'log') planLog = [...planLog, ev.line];
			else if (ev.kind === 'result') applyOutputs = ev.outputs ?? [];
			else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') {
				planResult = op === 'apply' ? 'Applied' : 'Destroyed';
				busy = '';
				if (op === 'apply' && approvalRequired) loadApproval(); // approval consumed
			}
		}, undefined, activeEnv);
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

<svelte:window onclick={() => (showImportMenu = false)} onkeydown={handleKeydown} />

<CommandPalette bind:open={showPalette} items={paletteItems} />

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
				{#each projects as p (p.id)}<option value={p.id}>{p.name} · {p.resourceCount}{p.drift?.drifted ? ' ⚠' : ''}</option>{/each}
			</select>
			<button class={iconBtn} title="New project" onclick={newProject} aria-label="New project"><Plus size={16} /></button>
			{#if project}
				<button class={iconBtn} title="Delete project" onclick={removeProject} aria-label="Delete project"><Trash2 size={16} /></button>
			{/if}
			{#if currentDrift?.drifted || currentDrift?.error}
				<button
					class="flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium"
					style="border-color:{currentDrift.error ? '#f85149' : '#e3b341'}66;color:{currentDrift.error ? '#f85149' : '#e3b341'};background:{currentDrift.error ? '#f85149' : '#e3b341'}14;"
					title={currentDrift.error
						? `Drift check failed: ${currentDrift.error}`
						: `Drift detected at ${new Date(currentDrift.checkedAt).toLocaleString()} — open State for details`}
					onclick={() => { loadState(); loadDrift(); showState = true; }}
				>
					<AlertTriangle size={12} /> {currentDrift.error ? 'drift check failed' : 'drift'}
				</button>
			{/if}
		</div>

		{#if project}
			<div class="ml-3 flex items-center gap-1.5 border-l border-[var(--al-border)] pl-3">
				{#if auth.can('resources')}
					<button class="{iconBtn} disabled:pointer-events-none disabled:opacity-30" title="Undo (Ctrl+Z)" aria-label="Undo" onclick={undo} disabled={!history.canUndo || busy !== ''}><Undo2 size={15} /></button>
					<button class="{iconBtn} disabled:pointer-events-none disabled:opacity-30" title="Redo (Ctrl+Shift+Z)" aria-label="Redo" onclick={redo} disabled={!history.canRedo || busy !== ''}><Redo2 size={15} /></button>
				{/if}
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
				<button class={hdrBtn} onclick={() => (showRuns = true)}>
					<History size={14} /> Runs
				</button>
				<button class={hdrBtn} onclick={() => { loadState(); loadDrift(); showState = true; }}>
					<Layers size={14} /> State
				</button>
				{#if auth.can('resources')}
					<button class={hdrBtn} onclick={() => (showCost = true)} title="Cost estimation settings">
						<DollarSign size={14} /> Cost
					</button>
					<button class={hdrBtn} onclick={() => (showPolicy = true)} title="Policy as code (conftest/OPA)">
						<ShieldCheck size={14} /> Policy
					</button>
					<button class={hdrBtn} onclick={() => (showNotify = true)} title="Webhook / Slack notifications for applies and drift">
						<Bell size={14} /> Notify
					</button>
				{/if}
				{#if environments.length > 0}
					<div class="flex items-center gap-1" title="Active deployment environment (scopes plan / apply / state)">
						<Boxes size={14} class="text-[var(--al-muted)]" />
						<select
							class="rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]"
							value={activeEnv}
							onchange={(e) => selectEnv((e.currentTarget as HTMLSelectElement).value)}
						>
							<option value="">Default</option>
							{#each environments as env (env.name)}
								<option value={env.name}>{env.name}</option>
							{/each}
						</select>
					</div>
				{/if}
				{#if auth.can('resources')}
					<button class={hdrBtn} onclick={() => (showEnvironments = true)} title="Manage deployment environments">
						<Boxes size={14} /> Environments
					</button>
				{/if}
				{#if auth.isAdmin}
					<button class={hdrBtn} onclick={() => (showAcl = true)} title="Manage who can access this project">
						<ShieldCheck size={14} /> Access
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
			<button class={hdrBtn} onclick={() => (showPalette = true)} title="Search commands and resources (Ctrl+K)">
				<Search size={13} /> <span class="text-[10px] text-[var(--al-muted)]">Ctrl K</span>
			</button>
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

	<div class="flex min-h-0 flex-1 overflow-hidden">
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
				onEditRegistryModule={(name) => { showEditor = false; openRegModule(registryModules.find((m) => m.name === name)); }}
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
							{#each providers as prov (prov.key + '.' + (prov.alias ?? ''))}
								{@const addr = prov.alias ? `${prov.key}.${prov.alias}` : prov.key}
								{@const up = providerUpdates[prov.source]}
								<li class="group flex items-center gap-1">
									<span class="mono flex-1 truncate text-[11px] text-[var(--al-muted)]" title={prov.source}>
										{prov.key}{#if prov.alias}<span class="text-[var(--al-accent)]">.{prov.alias}</span>{/if}
									</span>
									{#if up?.update && up.latest}
										{#if auth.can('resources')}
											<button
												class="flex items-center gap-0.5 rounded-full border px-1.5 py-px text-[10px] font-semibold transition-colors hover:text-black disabled:opacity-50"
												style="border-color:#e3b34166;color:#e3b341;background:#e3b34114;"
												onmouseenter={(e) => (e.currentTarget.style.background = '#e3b341')}
												onmouseleave={(e) => (e.currentTarget.style.background = '#e3b34114')}
												title="Update available: {up.current ?? up.pinned ?? '?'} → {up.latest}. Click to pin {addr} to {up.latest}."
												disabled={bumping === prov.source}
												onclick={() => bumpProvider(prov, up.latest!)}
											>
												{#if bumping === prov.source}<Loader2 size={10} class="animate-spin" />{:else}<ArrowUp size={10} />{/if}{up.latest}
											</button>
										{:else}
											<span class="mono text-[10px]" style="color:#e3b341;" title="Update available: {up.current ?? up.pinned ?? '?'} → {up.latest}">↑{up.latest}</span>
										{/if}
									{/if}
									{#if auth.can('resources')}
										{#if !prov.alias}
											<button class="{iconBtn} opacity-0 group-hover:opacity-100" title="Add aliased instance of {prov.key}" aria-label="Add provider alias" onclick={() => addProviderAlias(prov)}><Plus size={13} /></button>
										{:else}
											<button class="{iconBtn} opacity-0 group-hover:opacity-100 hover:text-[var(--al-danger)]" title="Remove {addr}" aria-label="Remove provider instance" onclick={() => deleteProviderInstance(prov)}><Trash2 size={13} /></button>
										{/if}
									{/if}
									<button class={iconBtn} title="Configure {addr}" aria-label="Configure provider" onclick={() => configureProvider(prov)}><Settings size={14} /></button>
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
									<button class="mono w-full rounded px-2 py-1 text-left text-xs {selectedType === t && formKind === 'resource' && resourceKind === 'resource' && !editingId ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => pickType(t)}>{t}</button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				{#if dataSourceTypes.length > 0}
					<div>
						<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Data sources</div>
						<ul class="space-y-0.5">
							{#each dataSourceTypes as t (t)}
								<li>
									<button class="mono w-full rounded px-2 py-1 text-left text-xs {selectedType === t && formKind === 'resource' && resourceKind === 'data' && !editingId ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}" onclick={() => pickType(t, 'data')} title="Add data source {t}">{t}</button>
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
							<button class={iconBtn} title="Add registry module" aria-label="Add registry module" onclick={() => openRegModule()}><Package size={14} /></button>
							<button class={iconBtn} title="New local module" aria-label="New local module" onclick={() => { nmName = ''; showNewModule = true; }}><Plus size={14} /></button>
						</span>
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
							{#each project.dataSources ?? [] as d (d.id)}
								<li class="group flex items-center">
									<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]" style="color:{editingId === d.id ? 'var(--al-accent)' : '#56d4bc'};opacity:{editingId === d.id ? 1 : 0.85};" onclick={() => editResource(d, 'data')} title="Edit data.{d.type}.{d.name}">data.{d.type}.{d.name}</button>
									<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteScopedResource(d.id, 'data')} aria-label="Delete"><Trash2 size={11} /></button>
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
								{#each m.dataSources ?? [] as d (d.id)}
									<li class="group flex items-center">
										<button class="mono flex-1 truncate rounded px-1.5 py-0.5 text-left text-[11px]" style="color:{editingId === d.id ? 'var(--al-accent)' : '#56d4bc'};opacity:{editingId === d.id ? 1 : 0.85};" onclick={() => editResource(d, 'data')} title="Edit data.{d.type}.{d.name}">data.{d.type}.{d.name}</button>
										<button class="opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteScopedResource(d.id, 'data')} aria-label="Delete"><Trash2 size={11} /></button>
									</li>
								{/each}
								<li>
									<button class="mt-0.5 text-[10px] text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => (showInputs = true)}>⚙ module inputs</button>
								</li>
							</ul>
						{/if}
					{/each}

					<!-- registry/external module calls -->
					{#each registryModules as m (m.name)}
						<div class="group flex items-center">
							<button class="flex flex-1 items-center gap-1.5 rounded px-2 py-1 text-left text-xs text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]" onclick={() => openRegModule(m)} title="{m.source}{m.version ? ' @ ' + m.version : ''}">
								<Package size={12} style="color:#c297ff" /> module.{m.name}
							</button>
							<button class="px-1 opacity-0 group-hover:opacity-100 text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => deleteRegModule(m.name)} aria-label="Delete registry module"><Trash2 size={12} /></button>
						</div>
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
					<div class="mono text-sm text-[var(--al-muted)]">provider "<span class="text-[var(--al-accent)]">{editingProviderKey}</span>"{#if editingProviderAlias && !providerAliasNew}<span> alias "<span class="text-[var(--al-accent)]">{editingProviderAlias}</span>"</span>{/if}</div>
					{#if providerAliasNew}
						<div>
							<label for="provider-alias" class="mb-1 block text-xs text-[var(--al-muted)]" title="alias meta-argument — resources select this instance via provider = {editingProviderKey}.<alias>">alias</label>
							<input id="provider-alias" class="mono w-36 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={editingProviderAlias} placeholder="euwest" />
						</div>
						<span class="pb-1.5 text-[10px] uppercase text-[var(--al-muted)]">new instance</span>
					{/if}
					{#if auth.can('resources')}<button class={btnPrimary + ' ml-auto'} onclick={saveProviderConfig} disabled={busy === 'save' || (providerAliasNew && !editingProviderAlias.trim())}><Save size={15} /> Save provider config</button>{/if}
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
					<div>
						<label for="resource-count" class="mb-1 block text-xs text-[var(--al-muted)]" title="count meta-argument — an HCL expression, e.g. 3 or var.replicas">count</label>
						<input id="resource-count" class="mono w-28 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" bind:value={resourceCount} placeholder="3 · var.n" disabled={resourceForEach.trim() !== ''} />
					</div>
					<div>
						<label for="resource-foreach" class="mb-1 block text-xs text-[var(--al-muted)]" title="for_each meta-argument — a set or map expression, e.g. toset([&quot;a&quot;, &quot;b&quot;])">for_each</label>
						<input id="resource-foreach" class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40" bind:value={resourceForEach} placeholder="toset([…]) · var.map" disabled={resourceCount.trim() !== ''} />
					</div>
					{#if formProviderInstances.length > 1}
						<div>
							<label for="resource-provider" class="mb-1 block text-xs text-[var(--al-muted)]" title="provider meta-argument — which instance of the provider manages this {resourceKind === 'data' ? 'data source' : 'resource'}">provider</label>
							<select id="resource-provider" class="mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" bind:value={resourceProvider}>
								{#each formProviderInstances as pi (pi.alias ?? '')}
									<option value={pi.alias ? `${pi.key}.${pi.alias}` : ''}>{pi.alias ? `${pi.key}.${pi.alias}` : `${pi.key} (default)`}</option>
								{/each}
							</select>
						</div>
					{/if}
					<div class="mono text-sm text-[var(--al-muted)]">{resourceKind === 'data' ? 'data' : 'resource'} "<span class="text-[var(--al-accent)]">{selectedType}</span>" "{resourceName}"{#if editingId}<span class="ml-1 text-[10px] uppercase">editing</span>{/if}</div>
					{#if auth.can('resources')}<button class={btnPrimary + ' ml-auto'} onclick={saveResource} disabled={busy === 'save'}><Save size={15} /> {editingId ? 'Update' : 'Save'} {resourceKind === 'data' ? 'data source' : 'resource'}</button>{/if}
					{#if editingId && resourceKind === 'resource' && auth.can('deploy')}
						<button class={btnGhost} title="Adopt an existing real-world resource into state ({importAddress})" onclick={() => { importId = ''; showImportResource = true; }} disabled={busy !== ''}>
							<HardDriveDownload size={15} /> Import
						</button>
					{/if}
				</div>
				<SchemaForm {schema} bind:values {references} secrets={secretNames} />
			{:else}
				<div class="grid h-full place-items-center text-center text-sm text-[var(--al-muted)]">
					Add a provider and pick a resource type, select an existing resource to edit,<br />or import existing HCL / state.
				</div>
			{/if}
		</main>

		<ResizeHandle
			orientation="vertical"
			label="Resize the generated-HCL panel"
			onResize={(dx) => clampHclWidth(hclWidth - dx)}
			onEnd={() => localStorage.setItem(HCL_WIDTH_KEY, String(hclWidth))}
		/>
		<aside
			class="flex shrink-0 flex-col border-l"
			style="width:{hclWidth}px;border-color:var(--al-border);"
			bind:clientHeight={asideHeight}
		>
			<!-- Generated HCL — shares the graph editor's viewer for a consistent look -->
			<div class="min-h-0 flex-1">
				<HclView {hcl}>
					{#snippet actions()}
						{#if auth.can('deploy')}
						{@const running = busy === 'plan' || busy === 'validate' || busy === 'lint' || busy === 'apply' || busy === 'destroy' || busy === 'import' || busy === 'state-rm' || busy === 'state-mv'}
						<button class={btnGhost} title="Validate the generated configuration" onclick={runValidate} disabled={!project || running}>
							{#if busy === 'validate'}<Loader2 size={14} class="animate-spin" />{:else}<ShieldCheck size={14} />{/if} Validate
						</button>
						<button class={btnGhost} title="Lint the generated configuration with tflint" onclick={runLint} disabled={!project || running}>
							{#if busy === 'lint'}<Loader2 size={14} class="animate-spin" />{:else}<Ruler size={14} />{/if} Lint
						</button>
						<button class={btnGhost} onclick={runPlan} disabled={!project || running}>
							{#if busy === 'plan'}<Loader2 size={14} class="animate-spin" />{:else}<Play size={14} />{/if} Plan
						</button>
						{#if approvalRequired && auth.can('approve')}
							<button
								class={btnGhost}
								title={approvalReady ? 'This plan is approved' : 'Approve the current plan so it can be applied'}
								onclick={approvePlan}
								disabled={!project || running || approvalReady || planCounts === null}
							>
								<CheckCircle2 size={14} /> {approvalReady ? 'Approved' : 'Approve'}
							</button>
						{/if}
						<button
							class={btnPrimary}
							onclick={() => (showApplyConfirm = true)}
							disabled={!project || running || (approvalRequired && !approvalReady)}
							title={approvalRequired && !approvalReady ? 'Apply is gated: an approved plan is required' : 'Apply the plan'}
						>
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
			<ResizeHandle
				orientation="horizontal"
				label="Resize the run-output pane"
				onResize={(dy) => clampRunHeight(runHeight - dy)}
				onEnd={() => localStorage.setItem(RUN_HEIGHT_KEY, String(runHeight))}
			/>
			<!-- Plan output -->
			<div class="flex min-h-0 flex-none flex-col border-t" style="height:{runHeight}px;border-color:var(--al-code-border);background:var(--al-code-bg);">
				<div class="flex h-11 flex-none items-center border-b px-4" style="border-color:var(--al-code-border);">
					<div class="font-mono" style="font-size:12.5px;font-weight:600;color:var(--al-code-text);">run output</div>
					{#if planResult}<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:{accentWeak};color:{accentHex};">{planResult}</span>{/if}
					{#if planCost}<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:rgba(16,185,129,0.15);color:#10b981;" title="Estimated monthly cost">${planCost.totalMonthly.toFixed(2)}/mo</span>{/if}
					{#if planPolicy && planPolicy.available && !planPolicy.error}
						{#if planPolicy.failureCount > 0}
							<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:{ACTION_COLOR.delete}22;color:{ACTION_COLOR.delete};" title="Policy failures — an enforced apply is blocked">{planPolicy.failureCount} policy failure{planPolicy.failureCount === 1 ? '' : 's'}</span>
						{:else}
							<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:rgba(16,185,129,0.15);color:#10b981;" title="All policies passed">policy ok</span>
						{/if}
					{/if}
					{#if project}
						{#if auth.can('resources')}
							<button
								class="ml-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold"
								style="background:var(--al-panel-2);color:var(--al-muted);"
								title={approvalRequired ? 'Apply requires an approved plan — click to disable' : 'Require an approved plan before apply'}
								onclick={toggleApprovalGate}
							>
								{#if approvalRequired}<Lock size={11} /> Gate on{:else}<LockOpen size={11} /> Gate off{/if}
							</button>
						{:else if approvalRequired}
							<span class="ml-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:var(--al-panel-2);color:var(--al-muted);"><Lock size={11} /> Gated</span>
						{/if}
						{#if approvalRequired && currentApproval && !currentApproval.consumed}
							<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:rgba(16,185,129,0.15);color:#10b981;" title="Approved — apply enabled">✓ approved by {currentApproval.approvedBy}</span>
						{/if}
					{/if}
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
					<div class="max-h-[45%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
						{#each planChanges as c (c.address)}
							<button
								class="flex w-full items-center gap-2 px-[18px] py-0.5 text-left font-mono hover:bg-[var(--al-panel-2)]"
								style="font-size:11.5px;line-height:1.6;"
								title="{c.address} — click to open"
								onclick={() => jumpToAddress(c.address)}
							>
								<span class="w-3 flex-none text-center font-bold" style="color:{ACTION_COLOR[c.action]}">{ACTION_SYMBOL[c.action]}</span>
								<span class="truncate" style="color:var(--al-code-text);">{c.address}</span>
								<span class="ml-auto flex-none text-[10px]" style="color:{ACTION_COLOR[c.action]}">{c.action}</span>
							</button>
						{/each}
					</div>
				{/if}
				{#if planCost}
					<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
						<div class="flex items-center gap-2 px-[18px] py-0.5">
							<span class="text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">estimated cost</span>
							<span class="ml-auto font-mono text-[11px] font-semibold" style="color:var(--al-code-text);">${planCost.totalMonthly.toFixed(2)}/mo</span>
						</div>
						{#if planCost.error}
							<div class="px-[18px] py-0.5 text-[10px]" style="color:var(--al-code-comment);">Infracost unavailable ({planCost.error}) — using price overrides only</div>
						{/if}
						{#each planCost.lines as l (l.address)}
							<div class="flex items-center gap-3 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
								<span class="truncate" style="color:var(--al-code-text);" title={l.address}>{l.address}</span>
								<span class="flex-none text-[9px] uppercase tracking-wide" style="color:var(--al-code-comment);">{l.source}</span>
								<span class="ml-auto flex-none" style="color:var(--al-code-muted);">${l.monthly.toFixed(2)}/mo</span>
							</div>
						{/each}
						{#if planCost.lines.length === 0}
							<div class="px-[18px] py-0.5 text-[11px]" style="color:var(--al-code-muted);">No priced resources — add price overrides or configure Infracost.</div>
						{/if}
					</div>
				{/if}
				{#if planPolicy}
					<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
						<div class="px-[18px] py-0.5 text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">
							policy{#if !planPolicy.available} — conftest not installed{:else if planPolicy.error} — check failed{:else} · {planPolicy.failureCount} failure{planPolicy.failureCount === 1 ? '' : 's'} · {planPolicy.warningCount} warning{planPolicy.warningCount === 1 ? '' : 's'}{/if}
						</div>
						{#if planPolicy.error}
							<div class="px-[18px] py-0.5 text-[11px]" style="color:{ACTION_COLOR.delete};">{planPolicy.error}</div>
						{/if}
						{#each planPolicy.violations as v, i (i)}
							<div class="flex items-start gap-2 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
								<span class="w-14 flex-none text-[10px] uppercase" style="color:{v.severity === 'failure' ? ACTION_COLOR.delete : ACTION_COLOR.update};">{v.severity}</span>
								<span class="truncate" style="color:var(--al-code-text);" title={v.message}>{v.message}</span>
								{#if v.rule}<span class="ml-auto flex-none text-[10px]" style="color:var(--al-code-comment);">{v.rule}</span>{/if}
							</div>
						{/each}
						{#if planPolicy.available && !planPolicy.error && planPolicy.violations.length === 0}
							<div class="px-[18px] py-0.5 text-[11px]" style="color:var(--al-code-muted);">All policies passed ({planPolicy.successes} rule{planPolicy.successes === 1 ? '' : 's'}).</div>
						{/if}
					</div>
				{/if}
				{#if planLint}
					<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
						<div class="px-[18px] py-0.5 text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">
							lint{#if !planLint.available} — tflint not installed{:else} · {planLint.issues.length} issue{planLint.issues.length === 1 ? '' : 's'}{/if}
						</div>
						{#each planLint.issues as iss, i (i)}
							<div class="flex items-start gap-2 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
								<span class="w-14 flex-none text-[10px] uppercase" style="color:{iss.severity === 'error' ? ACTION_COLOR.delete : ACTION_COLOR.update};">{iss.severity}</span>
								<span class="truncate" style="color:var(--al-code-text);" title={iss.message}>{iss.message}</span>
								{#if iss.filename}<span class="ml-auto flex-none text-[10px]" style="color:var(--al-code-comment);">{iss.filename}:{iss.line}</span>{/if}
							</div>
						{/each}
						{#if planLint.available && planLint.issues.length === 0}
							<div class="px-[18px] py-0.5 text-[11px]" style="color:var(--al-code-muted);">No issues found.</div>
						{/if}
					</div>
				{/if}
				{#if applyOutputs.length > 0}
					<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
						<div class="px-[18px] py-0.5 text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">outputs</div>
						{#each applyOutputs as o (o.name)}
							<div class="flex items-center gap-3 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
								<span class="flex-none" style="color:var(--al-code-text);">{o.name}</span>
								<span class="ml-auto truncate" style="color:var(--al-code-muted);" title={o.sensitive ? '' : o.value}>{o.sensitive ? '(sensitive)' : o.value}</span>
							</div>
						{/each}
					</div>
				{/if}
				<pre class="flex-1 overflow-auto whitespace-pre-wrap px-[18px] py-3 font-mono" style="font-size:11.5px;line-height:1.6;color:var(--al-code-muted);">{planLog.join('\n') || '// run plan or apply to see output'}</pre>
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

	<RunsPanel bind:open={showRuns} projectId={project.id} />

	<AclModal bind:open={showAcl} projectId={project.id} projectName={project.name} />

	<CostPanel bind:open={showCost} projectId={project.id} />

	<PolicyPanel bind:open={showPolicy} projectId={project.id} />

	<NotifyPanel bind:open={showNotify} projectId={project.id} />

	<EnvironmentsPanel
		bind:open={showEnvironments}
		projectId={project.id}
		hasBackend={!!project.backend}
		onSaved={(envs) => {
			environments = envs;
			if (activeEnv && !envs.some((e) => e.name === activeEnv)) activeEnv = '';
		}}
	/>

	<StatePanel
		bind:open={showState}
		entries={stateEntries}
		loading={stateLoading}
		busy={busy === 'import' || busy === 'state-rm' || busy === 'state-mv' || busy === 'init-backend'}
		canDeploy={auth.can('deploy')}
		backendType={project.backend?.type ?? ''}
		drift={driftInfo}
		onSaveDrift={auth.can('deploy') ? saveDrift : undefined}
		onConfigureBackend={auth.can('resources') ? openBackend : undefined}
		onRefresh={() => { loadState(); loadDrift(); }}
		onRm={(address) => runStateOp('state-rm', { address })}
		onMv={(from, to) => runStateOp('state-mv', { from, to })}
		onAcceptDrift={auth.can('deploy') ? () => runStateOp('accept-drift', {}) : undefined}
	/>

	<!-- Backend (remote state) configuration -->
	<Modal title="State backend" bind:open={showBackend}>
		<div class="space-y-3">
			<p class="text-xs text-[var(--al-muted)]">
				Where this project's state lives. Empty type = local state in the project workspace.
				Values are raw HCL literals (no interpolation allowed by Terraform); put credentials in the
				server's environment, never here.
			</p>
			<div>
				<label for="bk-type" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Backend type</label>
				<input id="bk-type" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="s3 · gcs · http · pg · … (empty = local)" bind:value={bkType} list="bk-types" />
				<datalist id="bk-types">
					{#each BACKEND_TYPES as t (t)}<option value={t}></option>{/each}
				</datalist>
			</div>
			<div>
				<div class="mb-1 flex items-center justify-between">
					<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Configuration</span>
					<button class="text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => (bkRows = [...bkRows, { key: '', expr: '' }])}>+ add value</button>
				</div>
				{#if bkRows.length === 0}
					<p class="text-xs text-[var(--al-muted)]">No configuration values.</p>
				{/if}
				<div class="space-y-1.5">
					{#each bkRows as row, i (i)}
						<div class="flex items-center gap-2">
							<input class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder="bucket" bind:value={row.key} />
							<input class="mono min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder={'"my-bucket" · true · 5432'} bind:value={row.expr} />
							<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => (bkRows = bkRows.filter((_, j) => j !== i))} aria-label="Remove value"><Trash2 size={12} /></button>
						</div>
					{/each}
				</div>
			</div>
			{#if auth.can('deploy')}
				<div>
					<span class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">On save</span>
					<label class="flex items-start gap-2 text-sm text-[var(--al-text)]">
						<input type="radio" class="mt-1" name="bk-mode" value="migrate" checked={bkMode === 'migrate'} onchange={() => (bkMode = 'migrate')} />
						<span>Migrate existing state<span class="block text-xs text-[var(--al-muted)]">Copies the current state into the new backend (init -migrate-state).</span></span>
					</label>
					<label class="mt-1 flex items-start gap-2 text-sm text-[var(--al-text)]">
						<input type="radio" class="mt-1" name="bk-mode" value="reconfigure" checked={bkMode === 'reconfigure'} onchange={() => (bkMode = 'reconfigure')} />
						<span>Reconfigure only<span class="block text-xs text-[var(--al-muted)]">Uses whatever the new backend already holds; nothing is copied.</span></span>
					</label>
				</div>
			{:else}
				<p class="text-xs text-[var(--al-muted)]">You lack the deploy permission — the backend is saved but the workspace is re-initialized on the next deploy-user operation.</p>
			{/if}
		</div>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showBackend = false)}>Cancel</button>
			<button class={btnPrimary} onclick={saveBackend} disabled={busy === 'backend'}>
				{#if busy === 'backend'}<Loader2 size={14} class="animate-spin" />{/if} Save backend
			</button>
		{/snippet}
	</Modal>

	<!-- Import a live resource into state -->
	<Modal title="Import into state" bind:open={showImportResource}>
		<p class="mb-3 text-sm text-[var(--al-text)]">
			Adopt an existing real-world resource as
			<span class="mono text-[var(--al-accent)]">{importAddress}</span>.
			The provider reads it by ID and records it in this project's state — nothing is created or changed.
		</p>
		<label for="import-id" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Resource ID</label>
		<input
			id="import-id"
			class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
			placeholder="provider-specific ID (e.g. i-0abc123, vpc-…, a value)"
			bind:value={importId}
			onkeydown={(e) => { if (e.key === 'Enter' && importId.trim()) runImport(); }}
		/>
		<p class="mt-1.5 text-xs text-[var(--al-muted)]">Attributes that differ from the form will show as changes on the next plan.</p>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showImportResource = false)}>Cancel</button>
			<button class={btnPrimary} onclick={runImport} disabled={!importId.trim()}>
				<HardDriveDownload size={14} /> Import
			</button>
		{/snippet}
	</Modal>

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

	<!-- Delete blocked: project still has deployed resources -->
	<Modal title="Project has deployed resources" bind:open={showDeleteBlocked}>
		<p class="text-sm text-[var(--al-text)]">
			<span class="font-semibold">{project.name}</span> still tracks
			<span class="font-semibold text-[var(--al-danger)]">{deleteBlockedCount} deployed resource{deleteBlockedCount === 1 ? '' : 's'}</span>
			in its state. Deleting the project also deletes that state, and the resources would keep
			running with nothing left to manage or destroy them.
		</p>
		<p class="mt-2 text-xs text-[var(--al-muted)]">
			Recommended: cancel and run <span class="font-semibold">Destroy</span> first, then delete the project.
		</p>
		<label for="delblock-name" class="mb-1 mt-3 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
			Type the project name ({project.name}) to delete anyway
		</label>
		<input
			id="delblock-name"
			class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-danger)]"
			bind:value={deleteBlockedName}
			placeholder={project.name}
		/>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showDeleteBlocked = false)}>Cancel</button>
			<button
				class="{btn} bg-[var(--al-danger)] text-white hover:brightness-110"
				disabled={deleteBlockedName !== project?.name}
				onclick={forceRemoveProject}
			>
				<Trash2 size={14} /> Delete anyway
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

	<!-- Registry module modal (add / edit) -->
	<Modal title={rmEditing ? `Module "${rmEditing}"` : 'Add registry module'} bind:open={showRegModule}>
		<div class="space-y-3">
			<div class="flex gap-2">
				<div class="w-40">
					<label for="rm-name" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Name</label>
					<input id="rm-name" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="vpc" bind:value={rmName} />
				</div>
				<div class="min-w-0 flex-1">
					<label for="rm-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Source</label>
					<input id="rm-source" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="terraform-aws-modules/vpc/aws" bind:value={rmSource} />
				</div>
				<div class="w-28">
					<label for="rm-version" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Version</label>
					<input id="rm-version" class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]" placeholder="~> 5.0" bind:value={rmVersion} />
				</div>
			</div>
			<div>
				<div class="mb-1 flex items-center justify-between">
					<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Inputs (raw HCL)</span>
					<button class="text-xs text-[var(--al-muted)] hover:text-[var(--al-accent)]" onclick={() => (rmRows = [...rmRows, { key: '', expr: '' }])}>+ add input</button>
				</div>
				{#if rmRows.length === 0}
					<p class="text-xs text-[var(--al-muted)]">No inputs yet — the module is called with defaults.</p>
				{/if}
				<div class="space-y-1.5">
					{#each rmRows as row, i (i)}
						<div class="flex items-center gap-2">
							<input class="mono w-40 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder="name" bind:value={row.key} />
							<input class="mono min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs outline-none focus:border-[var(--al-accent)]" placeholder={'value (HCL) — e.g. "10.0.0.0/16" or var.cidr'} bind:value={row.expr} />
							<button class="text-[var(--al-muted)] hover:text-[var(--al-danger)]" onclick={() => (rmRows = rmRows.filter((_, j) => j !== i))} aria-label="Remove input"><Trash2 size={12} /></button>
						</div>
					{/each}
				</div>
			</div>
		</div>
		{#snippet footer()}
			<button class={btnGhost} onclick={() => (showRegModule = false)}>Cancel</button>
			<button class={btnPrimary} onclick={saveRegModule} disabled={busy === 'regmodule' || !rmName.trim() || !rmSource.trim()}>{#if busy === 'regmodule'}<Loader2 size={14} class="animate-spin" />{/if} {rmEditing ? 'Update' : 'Add'} module</button>
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
	<p class="mb-2 text-xs text-[var(--al-muted)]">
		Select a <span class="mono">terraform.tfstate</span> file or paste its JSON. Managed resources become
		editable configuration (computed/sensitive values are dropped so the generated config stays valid),
		<strong class="text-[var(--al-text)]">and the state itself is adopted</strong> as this project's state
		{#if activeEnv}for environment <span class="mono">{activeEnv}</span>{/if} — so a plan diffs against
		what already exists instead of proposing to create it all again.
	</p>
	<input type="file" accept=".tfstate,.json" class="mb-2 text-xs" onchange={(e) => readFiles(e.currentTarget.files, 'state')} />
	<textarea class={ta} rows="12" placeholder={'{ "version": 4, "resources": [...] }'} bind:value={importStateText}></textarea>
	<label class="mt-2 flex items-center gap-2 text-xs text-[var(--al-muted)]">
		<input type="checkbox" checked={replaceState} onchange={() => (replaceState = !replaceState)} class="accent-[var(--al-accent)]" />
		Replace the existing state (needed only when this project already tracks resources)
	</label>
	{#snippet footer()}
		<button class={btnGhost} onclick={() => (showImportState = false)}>Cancel</button>
		<button class={btnPrimary} onclick={doImportState} disabled={busy === 'import'}>
			{#if busy === 'import'}<Loader2 size={14} class="animate-spin" />{/if} Import
		</button>
	{/snippet}
</Modal>
