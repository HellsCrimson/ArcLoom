<script lang="ts">
	import { onMount } from 'svelte';
	import { api, streamPlan, streamOp, DeleteBlockedError, ModuleInUseError, type DriftInfo, type StateEntry, type Environment, type Approval, type PendingPlan } from '$lib/api/client';
	import RunsPanel from '$lib/components/RunsPanel.svelte';
	import StatePanel from '$lib/components/StatePanel.svelte';
	import type { AttrValue, DriftConfig, Engine, Module, Project, ProjectSummary, ProviderInstance, ProviderUpdate, Resource, ResourceKind } from '$lib/types';
	import ResourceFormPane from '$lib/form/ResourceFormPane.svelte';
	import NewProjectModal from '$lib/components/NewProjectModal.svelte';
	import ApplyConfirmModal from '$lib/components/ApplyConfirmModal.svelte';
	import DeleteBlockedModal from '$lib/components/DeleteBlockedModal.svelte';
	import DestroyConfirmModal from '$lib/components/DestroyConfirmModal.svelte';
	import TerraformBlockModal from '$lib/components/TerraformBlockModal.svelte';
	import NewModuleModal from '$lib/components/NewModuleModal.svelte';
	import ModuleCallModal from '$lib/components/ModuleCallModal.svelte';
	import FileModal from '$lib/components/FileModal.svelte';
	import ImportHclModal from '$lib/components/ImportHclModal.svelte';
	import ImportStateModal from '$lib/components/ImportStateModal.svelte';
	import ImportResourceModal from '$lib/components/ImportResourceModal.svelte';
	import DeployActions from '$lib/components/DeployActions.svelte';
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
	import CommandPalette from '$lib/components/CommandPalette.svelte';
	import ToolbarActions from '$lib/components/ToolbarActions.svelte';
	import ResizeHandle from '$lib/components/ResizeHandle.svelte';
	import RunOutputPane from '$lib/components/RunOutputPane.svelte';
	import StructureSidebar from '$lib/components/StructureSidebar.svelte';
	import { attrToText, buildAttrs, rowsToAttrs } from '$lib/form/attrs';
	import { findInScope, localFolder, providerForType, providerVersionForSource, resolveAddress, suggestName } from '$lib/model/scope';
	import { iconBtn, hdrBtn } from '$lib/styles';
	import { buildImportRows, buildPaletteItems, buildToolItems, globalKeydown, type CommandActions } from '$lib/commands';
	import { auth } from '$lib/stores/auth.svelte';
	import { history } from '$lib/stores/history.svelte';
	import { panels } from '$lib/stores/panelGeometry.svelte';
	import { runPane } from '$lib/stores/runPane.svelte';
	import { form } from '$lib/stores/resourceForm.svelte';
	import EditorView from '$lib/editor/EditorView.svelte';
	import HclView from '$lib/editor/HclView.svelte';
	import type { ProjectConfig as ProjectConfigT, ProjectFile, SecretMeta } from '$lib/types';
	import {
		Boxes, Plus, Trash2, FolderGit2, Settings, Undo2, Redo2, AlertTriangle, Search
	} from 'lucide-svelte';

	const LAST_PROJECT_KEY = 'arcloom.lastProject';

	let engines = $state<Engine[]>([]);
	let defaultEngine = $state('tofu');

	let projects = $state<ProjectSummary[]>([]);
	let project = $state<Project | null>(null);
	let engine = $state('');

	let hcl = $state('');
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
	// Plan → approve → apply gate (per project + active env). planPending is the
	// server's stashed plan: it is what makes Approve available to a user who did
	// not run the plan in this tab (a reload, or a different approver).
	let approvalRequired = $state(false);
	let currentApproval = $state<Approval | null>(null);
	let planPending = $state<PendingPlan | null>(null);
	// Provider upgrade suggestions, keyed by source (best-effort registry check).
	let providerUpdates = $state<Record<string, ProviderUpdate>>({});
	let bumping = $state('');
	let showImportResource = $state(false);
	let importId = $state('');
	// Backend (remote state) modal
	let showBackend = $state(false);
	let bkRequiredVersion = $state('');
	// Signature of the backend as opened, so saving only re-inits when it changed.
	let bkOpenedSig = $state('');
	let bkType = $state('');
	let bkRows = $state<{ key: string; expr: string }[]>([]);
	let bkMode = $state<'migrate' | 'reconfigure'>('migrate');
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
	// Whether a secret can be created (local encrypted store or an external one)
	// and which store that is — the Secrets modal and the inline "+ New" both
	// hinge on it.
	let secretsWritable = $state(false);
	let secretsBackend = $state('');
	const secretNames = $derived(secretsList.map((s) => s.name));

	async function loadSecrets() {
		if (!project) return;
		try {
			const res = await api.listSecrets(project.id);
			secretsList = res.secrets ?? [];
			secretsWritable = res.writable;
			secretsBackend = res.backend;
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

	// createSecretInline backs the "New" button inside a resource field: same store
	// as the Secrets modal, but it throws so the field can show the failure in
	// place (and keep the typed value) instead of it landing in the page banner.
	async function createSecretInline(name: string, value: string) {
		if (!project) throw new Error('no project selected');
		await api.setSecret(project.id, name, value);
		await loadSecrets();
		await refreshHcl();
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
	// Everything referenceable from the ACTIVE scope. Variables and locals belong
	// to whichever scope declares them — the root's were missing entirely, so
	// `var.x` could only be reached by switching to Expression and typing it.
	const references = $derived.by(() => {
		const refs: string[] = [];
		for (const r of savedResources) refs.push(`${r.type}.${r.name}.id`, `${r.type}.${r.name}.result`);
		for (const d of savedDataSources) refs.push(`data.${d.type}.${d.name}.id`);
		const scope = activeMod ?? project;
		for (const v of scope?.variables ?? []) refs.push(`var.${v.name}`);
		for (const l of scope?.locals ?? []) refs.push(`local.${l.name}`);
		if (!activeMod) {
			// From the root you can reference each module's outputs. (A module
			// cannot reference the root's, so this stays root-only.)
			for (const m of localModules) for (const o of m.outputs ?? []) refs.push(`module.${m.name}.${o.name}`);
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
			runPane.clearOverlay();
			// A model edit makes any approved plan stale: block a gated apply until
			// the user re-plans and re-approves. The plan waiting to be approved is
			// stale for the same reason, so stop offering it.
			currentApproval = null;
			planPending = null;
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
			runPane.clearOverlay();
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
		if (form.formKind === 'resource' && form.editingId) {
			const found = findInScope(project, form.editingId, activeModule ?? undefined);
			if (found) await editResource(found.r, found.kind);
			else resetFormOnly();
		} else if (form.formKind === 'provider' && form.providerKey) {
			const prov = project.providers.find(
				(p) => p.key === form.providerKey && (p.alias ?? '') === form.providerAlias
			);
			if (prov && !form.providerAliasNew) await configureProvider(prov);
			else if (!prov) resetFormOnly();
		}
	}

	// Every command the palette, the top bar and the shortcuts can run. All
	// values are arrows, so referencing a function declared further down cannot
	// hit a temporal-dead-zone error.
	const commandActions: CommandActions = {
		showEditorPane: () => { error = ''; showEditor = true; },
		toggleEditor: () => openEditor(),
		openConfig: () => (showConfig = true),
		openSecrets: () => { loadSecrets(); showSecrets = true; },
		openGit: () => (showGit = true),
		openRuns: () => (showRuns = true),
		openStatePanel: () => { loadState(); loadDrift(); showState = true; },
		plan: () => runPlan(),
		validate: () => runValidate(),
		lint: () => runLint(),
		openApply: () => (showApplyConfirm = true),
		openDestroy: () => { destroyName = ''; showDestroyConfirm = true; },
		openCost: () => (showCost = true),
		openPolicy: () => (showPolicy = true),
		openNotify: () => (showNotify = true),
		openTerraform: () => openBackend(),
		openNewFile: () => openFile(),
		openEnvironments: () => (showEnvironments = true),
		openImportHcl: () => { importMsg = ''; showImportHcl = true; },
		openImportState: () => { importMsg = ''; showImportState = true; },
		showImportHcl: () => (showImportHcl = true),
		showImportState: () => (showImportState = true),
		openImportMenu: () => (showImportMenu = true),
		newProject: () => newProject(),
		openSettings: () => (showSettings = true),
		openAcl: () => (showAcl = true),
		jumpToAddress: (addr: string) => jumpToAddress(addr),
		openModuleCall: (m: Module) => openCall(m)
	};

	// Command palette entries: permission-filtered commands plus every resource
	// across all scopes (global search; selecting one opens its form).
	const paletteItems = $derived.by(() =>
		buildPaletteItems({
			project,
			moduleCalls,
			perms: { resources: auth.can('resources'), deploy: auth.can('deploy'), git: auth.can('git'), admin: auth.isAdmin },
			actions: commandActions
		})
	);

	onMount(async () => {
		// Restore the dragged panel geometry before the first paint of the aside.
		panels.init();
		// The run pane and the resource form used to be this component's own $state,
		// so they died with it. They are singletons now, and +layout unmounts this
		// page on logout — without an explicit reset the next user to sign in in the
		// same tab would see the previous session's streamed output and provider
		// source. Resetting on mount restores the old per-mount lifetime exactly.
		runPane.resetSession();
		form.resetSession();
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
			if (project.providers?.length) form.providerSource = project.providers[0].source;
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
		runPane.changes = [];
		runPane.counts = null;
		runPane.cost = null;
		runPane.result = '';
		if (showState) loadState();
		loadApproval();
	}

	// loadApproval fetches the approval-gate state for the active environment.
	async function loadApproval() {
		if (!project) {
			approvalRequired = false;
			currentApproval = null;
			planPending = null;
			return;
		}
		try {
			const st = await api.getApproval(project.id, activeEnv);
			approvalRequired = st.required;
			currentApproval = st.approval;
			planPending = st.planPending;
		} catch {
			approvalRequired = false;
			currentApproval = null;
			planPending = null;
		}
	}

	// approvePlan signs off the plan the server stashed for this environment so a
	// gated apply can run it. Requires the approve permission (server-enforced);
	// the summary on the record comes from that plan, not from this tab.
	async function approvePlan() {
		if (!project) return;
		error = '';
		try {
			currentApproval = await api.approve(project.id, activeEnv);
			planPending = null;
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
		form.resetAll();
		hcl = '';
		runPane.reset();
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
		form.resetForScope();
		await refreshHcl();
	}

	// ---- local modules ----
	let showNewModule = $state(false);
	let nmName = $state('');

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

	// Deleting a DEFINITION is refused while a call still uses it (deleting only
	// the folder would leave a module block pointing at nothing). The 409 names
	// the calls, so the confirm can offer to take them along.
	async function deleteModule(name: string, force = false) {
		if (!project) return;
		if (!force && !confirm(`Delete module "${name}" and all its resources?`)) return;
		try {
			project = await api.deleteLocalModule(project.id, name, force);
			await refreshProjects();
			if (activeModule === name) await selectScope(null);
			else await refreshHcl();
		} catch (err) {
			if (err instanceof ModuleInUseError) {
				if (confirm(`${err.message}.\n\nDelete the module AND those calls?`)) {
					await deleteModule(name, true);
					return;
				}
				return;
			}
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
			if (form.editingId === rid) resetFormOnly();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	function resetFormOnly() {
		form.resetForm();
	}

	// ---- companion files (templates/scripts for file()/templatefile()) ----
	// Scope-independent: a path under modules/<name>/ targets that module, so
	// these are NOT filtered by activeModule.
	const files = $derived(project?.files ?? []);
	let showFile = $state(false);
	let fileEditing = $state<string | null>(null); // original path when editing
	let filePath = $state('');
	let fileContent = $state('');

	function openFile(f?: ProjectFile) {
		fileEditing = f?.path ?? null;
		filePath = f?.path ?? '';
		fileContent = f?.content ?? '';
		showFile = true;
	}

	async function saveFile() {
		if (!project || !filePath.trim()) return;
		busy = 'file';
		error = '';
		try {
			// oldPath renames server-side in one update, so a rename can never
			// leave the previous path behind as a stale copy.
			project = await api.setFile(project.id, {
				path: filePath.trim(),
				oldPath: fileEditing && fileEditing !== filePath.trim() ? fileEditing : undefined,
				content: fileContent
			});
			await refreshHcl();
			showFile = false;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function deleteFile(path: string) {
		if (!project) return;
		if (!confirm(`Remove file "${path}" from the project?`)) return;
		try {
			project = await api.deleteFile(project.id, path);
			await refreshHcl();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// ---- module calls (registry AND local: one block type, one editor) ----
	const moduleCalls = $derived(project?.modules ?? []);
	let showRegModule = $state(false);
	let rmEditing = $state<string | null>(null); // original name when editing
	let rmName = $state('');
	let rmSource = $state('');
	let rmVersion = $state('');
	let rmRows = $state<{ key: string; expr: string }[]>([]);
	let rmCount = $state('');
	let rmForEach = $state('');

	// The argument rows a call starts with. For a local source they are seeded
	// from the definition's DECLARED variables (what the old module-inputs modal
	// did) so you get a labelled form instead of guessing key names; existing
	// values win, and any extra key already on the call is kept rather than
	// silently dropped.
	function callRows(m: Module | undefined, source: string): { key: string; expr: string }[] {
		const existing = m?.config ? Object.entries(m.config).map(([key, av]) => ({ key, expr: attrToText(av) })) : [];
		const folder = localFolder({ source });
		const declared = folder ? (project?.localModules?.find((lm) => lm.name === folder)?.variables ?? []) : [];
		if (declared.length === 0) return existing;
		const byKey = new Map(existing.map((r) => [r.key, r]));
		const rows = declared.map((v) => byKey.get(v.name) ?? { key: v.name, expr: '' });
		for (const r of existing) if (!declared.some((v) => v.name === r.key)) rows.push(r);
		return rows;
	}

	// openCall edits an existing call, or starts a new one — `presetSource` is
	// how "use this module" on a definition opens the editor pre-aimed at it.
	function openCall(m?: Module, presetSource?: string) {
		rmEditing = m?.name ?? null;
		rmName = m?.name ?? '';
		rmSource = m?.source ?? presetSource ?? '';
		rmVersion = m?.version ?? '';
		rmRows = callRows(m, rmSource);
		rmCount = m?.count ?? '';
		rmForEach = m?.forEach ?? '';
		if (!m && presetSource) {
			// Suggest a free name so a second use of a module is one click away.
			const folder = localFolder({ source: presetSource });
			const taken = new Set(moduleCalls.map((c) => c.name));
			let n = folder || 'module';
			let i = 1;
			while (taken.has(n)) n = `${folder || 'module'}_${++i}`;
			rmName = n;
		}
		showRegModule = true;
	}

	async function saveCall() {
		if (!project || !rmName.trim() || !rmSource.trim()) return;
		busy = 'regmodule';
		error = '';
		try {
			project = await api.setModuleCall(project.id, {
				name: rmName.trim(),
				// The server moves the call rather than making a second one.
				rename: rmEditing ?? undefined,
				source: rmSource.trim(),
				version: rmVersion.trim() || undefined,
				config: rowsToAttrs(rmRows),
				count: rmCount.trim(),
				forEach: rmForEach.trim()
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

	async function deleteCall(name: string) {
		if (!project) return;
		if (!confirm(`Remove the call module."${name}" from the project?`)) return;
		try {
			project = await api.deleteModuleCall(project.id, name);
			await refreshProjects();
			await refreshHcl();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	async function addProvider() {
		error = '';
		busy = 'provider';
		form.schema = null;
		form.selectedType = '';
		try {
			const res = await api.addProvider(form.providerSource, engine, providerVersionForSource(project, form.providerSource));
			form.resourceTypes = res.resourceTypes;
			form.dataSourceTypes = res.dataSourceTypes ?? [];
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function pickType(t: string, kind: ResourceKind = 'resource') {
		error = '';
		busy = 'schema';
		form.beginNew(t, kind, suggestName(kind === 'data' ? savedDataSources : savedResources, t));
		try {
			const ver = providerVersionForSource(project, form.providerSource);
			form.schema = kind === 'data'
				? await api.dataSourceSchema(form.providerSource, t, engine, ver)
				: await api.resourceSchema(form.providerSource, t, engine, ver);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function editResource(r: Resource, kind: ResourceKind = 'resource') {
		error = '';
		busy = 'schema';
		form.beginEdit(r, kind);
		const src = providerForType(project, r.type, form.providerSource);
		try {
			const ver = providerVersionForSource(project, src);
			form.schema = kind === 'data'
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
		form.beginProvider(prov);
		try {
			form.schema = await api.providerSchema(prov.source, engine, prov.version);
			form.values = JSON.parse(JSON.stringify(prov.config ?? {}));
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
		form.providerAlias = '';
		form.providerAliasNew = true;
		form.values = {};
	}

	async function deleteProviderInstance(prov: ProviderInstance) {
		if (!project) return;
		const addr = prov.alias ? `${prov.key}.${prov.alias}` : prov.key;
		if (!confirm(`Remove provider instance "${addr}" from the project?`)) return;
		error = '';
		try {
			project = await api.deleteProviderInstance(project.id, prov.key, prov.alias);
			await refreshHcl();
			if (form.formKind === 'provider' && form.providerKey === prov.key && form.providerAlias === (prov.alias ?? '')) resetFormOnly();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	async function saveResource() {
		if (!form.schema || !project) return;
		error = '';
		busy = 'save';
		try {
			await api.upsertResource(project.id, {
				id: form.editingId ?? undefined,
				kind: form.kind,
				type: form.selectedType,
				name: form.name,
				providerSource: form.providerSource,
				module: activeModule ?? undefined,
				attrs: buildAttrs(form.values),
				dependsOn: form.dependsOn,
				count: form.count.trim() || undefined,
				forEach: form.forEach.trim() || undefined,
				provider: form.provider || undefined
			});
			project = await api.getProject(project.id);
			await refreshProjects();
			await refreshHcl();
			const scope = activeModule ? project.localModules?.find((m) => m.name === activeModule) : project;
			const scopeRes = (form.kind === 'data' ? scope?.dataSources : scope?.resources) ?? [];
			form.editingId = form.editingId ?? scopeRes.find((r) => r.name === form.name && r.type === form.selectedType)?.id ?? null;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
		}
	}

	async function saveProviderConfig() {
		if (!project) return;
		if (form.providerAliasNew && !form.providerAlias.trim()) {
			error = 'alias is required for an additional provider instance';
			return;
		}
		error = '';
		busy = 'save';
		try {
			const alias = form.providerAlias.trim();
			const before = providers.find((p) => p.key === form.providerKey && (p.alias ?? '') === alias);
			const version = form.providerFormVersion.trim();
			project = await api.setProviderConfig(project.id, form.providerKey, {
				alias: alias || undefined,
				// The form's own source, NOT form.providerSource — that one is the
				// palette selector and the sidebar's add box binds to it.
				source: form.providerFormSource.trim(),
				// Always sent (never undefined) so clearing the box unpins.
				version,
				config: buildAttrs(form.values)
			});
			form.providerAlias = alias;
			form.providerAliasNew = false;
			await refreshProjects();
			await refreshHcl();
			// A different pin can mean a different schema (the schema cache keys on
			// the version constraint), so reload it rather than leave stale fields.
			if (before && (before.version ?? '') !== version) {
				const updated = (project.providers ?? []).find(
					(p) => p.key === form.providerKey && (p.alias ?? '') === alias
				);
				if (updated) await configureProvider(updated);
			}
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
			runPane.changes = [];
			runPane.counts = null;
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
		const res = await api.addProvider(source, engine, providerVersionForSource(project, source));
		return { resourceTypes: res.resourceTypes, dataSourceTypes: res.dataSourceTypes ?? [] };
	}

	// Palette "add": leave the editor and open the schema form for a new
	// resource or data source.
	async function editorAddType(type: string, source: string, kind: ResourceKind = 'resource') {
		showEditor = false;
		form.providerSource = source;
		await pickType(type, kind);
	}

	// Clicking a node opens it in the schema form (in the node's own scope). The
	// canvas passes a shallow node, so we resolve the full resource (with attrs).
	function editorEditResource(r: Resource, moduleName?: string) {
		const found = findInScope(project, r.id, moduleName);
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
		const found = resolveAddress(project, moduleCalls, address);
		if (!found) return false;
		if (found.kind === 'registryModule') {
			openCall(found.module);
			return true;
		}
		activeModule = found.module ?? null;
		showEditor = false;
		editResource(found.r, found.resourceKind);
		return true;
	}

	async function editorDeleteResource(id: string, moduleName?: string) {
		if (!project) return;
		try {
			await api.deleteResource(project.id, id, moduleName, findInScope(project, id, moduleName)?.kind);
			await refreshProject();
		} catch (err) {
			error = (err as Error).message;
		}
	}

	// Re-save an existing resource or data source with a changed field (name or
	// depends_on), preserving its type/attrs, within its scope.
	async function resaveResource(id: string, patch: { name?: string; dependsOn?: string[] }, moduleName?: string) {
		if (!project) return;
		const found = findInScope(project, id, moduleName);
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
		runPane.reset();
		busy = 'plan';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') runPane.append(ev.line);
			else if (ev.kind === 'result') {
				runPane.result = ev.hasChanges ? 'Changes pending' : 'No changes';
				runPane.changes = ev.changes ?? [];
				runPane.dismissedAddress = null; // a fresh plan re-shows the form banner
				runPane.counts = ev.hasChanges
					? { add: ev.add ?? 0, change: ev.change ?? 0, destroy: ev.destroy ?? 0, replace: ev.replace ?? 0 }
					: null;
				runPane.cost = ev.cost ?? null;
				runPane.policy = ev.policy ?? null;
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
		runPane.reset();
		busy = 'validate';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') runPane.append(ev.line);
			else if (ev.kind === 'result') {
				runPane.result = ev.valid
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
		runPane.reset();
		busy = 'lint';
		streamPlan(project.id, (ev) => {
			if (ev.kind === 'log') runPane.append(ev.line);
			else if (ev.kind === 'result') {
				runPane.lint = ev.lint ?? null;
				if (ev.available === false) runPane.result = 'tflint not installed';
				else if ((ev.errorCount ?? 0) > 0) runPane.result = `${ev.errorCount} lint error(s)`;
				else if ((ev.warningCount ?? 0) > 0) runPane.result = `${ev.warningCount} lint warning(s)`;
				else runPane.result = 'No lint issues';
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
		op:
			| 'import'
			| 'state-rm'
			| 'state-mv'
			| 'init-backend'
			| 'accept-drift'
			| 'taint'
			| 'untaint'
			| 'force-unlock',
		params: Record<string, string>
	) {
		if (!project) return;
		error = '';
		runPane.reset();
		busy = op;
		streamOp(project.id, op, (ev) => {
			if (ev.kind === 'log') runPane.append(ev.line);
			else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
				loadState();
			} else if (ev.kind === 'done') {
				runPane.result =
					op === 'import' ? 'Imported into state'
					: op === 'state-rm' ? 'Removed from state'
					: op === 'state-mv' ? 'State address moved'
					: op === 'accept-drift' ? 'Drift absorbed into state'
					: op === 'taint' ? 'Marked for replacement'
					: op === 'untaint' ? 'Tainted mark cleared'
					: op === 'force-unlock' ? 'State lock released'
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
		bkRequiredVersion = project?.requiredVersion ?? '';
		bkMode = 'migrate';
		bkOpenedSig = backendSig();
		showState = false;
		showBackend = true;
	}

	// backendSig fingerprints the edited backend so an unchanged one is not
	// re-initialized just because the terraform block's version changed.
	function backendSig(): string {
		const rows = bkRows
			.map((r) => `${r.key.trim()}=${r.expr.trim()}`)
			.filter((r) => r !== '=')
			.sort();
		return `${bkType.trim()}|${rows.join('|')}`;
	}

	// Saves the terraform block: required_version (plain config) and, when it
	// actually changed, the state backend — which (with deploy permission) is then
	// re-initialized so the change takes effect, migrating the existing state or
	// reconfiguring per the chosen mode.
	async function saveBackend() {
		if (!project) return;
		busy = 'backend';
		error = '';
		try {
			if (bkRequiredVersion.trim() !== (project.requiredVersion ?? '')) {
				project = await api.setTerraform(project.id, bkRequiredVersion.trim());
			}
			const backendChanged = backendSig() !== bkOpenedSig;
			if (backendChanged) {
				const config = rowsToAttrs(bkRows);
				project = await api.setBackend(project.id, { type: bkType.trim(), config });
			}
			await refreshHcl();
			showBackend = false;
			busy = '';
			if (backendChanged && auth.can('deploy')) {
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
		`${activeModule ? `module.${activeModule}.` : ''}${form.selectedType}.${form.name}`
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
		runPane.reset();
		busy = op;
		streamOp(project.id, op, (ev) => {
			if (ev.kind === 'log') runPane.append(ev.line);
			else if (ev.kind === 'result') runPane.outputs = ev.outputs ?? [];
			else if (ev.kind === 'error') {
				error = ev.error;
				busy = '';
			} else if (ev.kind === 'done') {
				runPane.result = op === 'apply' ? 'Applied' : 'Destroyed';
				busy = '';
				if (op === 'apply' && approvalRequired) loadApproval(); // approval consumed
			}
		}, undefined, activeEnv);
	}

	// --- Responsive top bar ---
	// The bar used to render every action at full size whatever the window width,
	// so on a narrow window it pushed past the viewport and produced a
	// document-level horizontal scrollbar. The actions are now described as data
	// and fitted into the width the flex layout actually leaves them: labels drop
	// first (icons keep the row usable), then the lowest-priority actions move
	// into the "More" menu. Nothing is ever unreachable.
	let toolsWidth = $state(0);
	let showToolMenu = $state(false);

	// Actions in priority order — earlier stays in the bar longer.
	const toolItems = $derived.by(() =>
		buildToolItems({
			hasProject: !!project,
			configCount,
			secretCount: secretsList.length,
			editorActive: showEditor,
			perms: { resources: auth.can('resources'), deploy: auth.can('deploy'), git: auth.can('git'), admin: auth.isAdmin },
			actions: commandActions
		})
	);

	const importItems = buildImportRows(commandActions);

</script>

<svelte:window
	onclick={() => { showImportMenu = false; showToolMenu = false; }}
	onkeydown={(e) => globalKeydown(e, {
		togglePalette: () => (showPalette = !showPalette),
		canPlan: () => !!project && auth.can('deploy') && busy === '',
		plan: runPlan,
		undo,
		redo
	})}
/>

<CommandPalette bind:open={showPalette} items={paletteItems} />

<div class="flex h-screen flex-col">
	<!-- No overflow-hidden anywhere on this bar: it would clip the Import and More
	     dropdowns, which hang below the header. Horizontal overflow — the bug this
	     replaces — is prevented structurally instead: the action row is fitted to
	     its measured width, and flex-wrap moves the fixed-size groups to a second
	     line once they cannot share one (roughly below 1000px). -->
	<header class="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-[var(--al-border)] bg-[var(--al-panel)] px-4 py-2">
		<div class="flex flex-none items-center gap-2 font-semibold">
			<Boxes size={18} class="text-[var(--al-accent)]" /> <span class="hidden sm:inline">ArcLoom</span>
		</div>
		<div class="flex flex-none items-center gap-1.5">
			<FolderGit2 size={15} class="hidden shrink-0 text-[var(--al-muted)] sm:block" />
			<select
				class="max-w-[7rem] rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm sm:max-w-[9rem] lg:max-w-[16rem]"
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
					<AlertTriangle size={12} />
					<span class="hidden md:inline">{currentDrift.error ? 'drift check failed' : 'drift'}</span>
				</button>
			{/if}
		</div>

		{#if project}
			<!-- Context, not actions: undo/redo and the active environment are
			     fixed-size and always available, so they stay out of the measured
			     action row. -->
			<div class="flex flex-none items-center gap-1.5 border-l border-[var(--al-border)] pl-3">
				{#if auth.can('resources')}
					<button class="{iconBtn} disabled:pointer-events-none disabled:opacity-30" title="Undo (Ctrl+Z)" aria-label="Undo" onclick={undo} disabled={!history.canUndo || busy !== ''}><Undo2 size={15} /></button>
					<button class="{iconBtn} disabled:pointer-events-none disabled:opacity-30" title="Redo (Ctrl+Shift+Z)" aria-label="Redo" onclick={redo} disabled={!history.canRedo || busy !== ''}><Redo2 size={15} /></button>
				{/if}
				{#if environments.length > 0}
					<div class="flex items-center gap-1" title="Active deployment environment (scopes plan / apply / state)">
						<Boxes size={14} class="hidden text-[var(--al-muted)] lg:block" />
						<select
							class="max-w-[7rem] rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]"
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
			</div>

			<!-- The measured action row. flex-1 + min-w-0 means clientWidth IS the
			     space the other groups left over, so the fit needs no estimate of
			     their widths. -->
			<ToolbarActions
				items={toolItems}
				importRows={importItems}
				bind:measuredWidth={toolsWidth}
				bind:menuOpen={showToolMenu}
				bind:importMenuOpen={showImportMenu}
				onImportOpen={() => (importMsg = '')}
			/>
		{/if}

		<div class="ml-auto flex flex-none items-center gap-2 text-sm">
			<button class={hdrBtn} onclick={() => (showPalette = true)} title="Search commands and resources (Ctrl+K)">
				<Search size={13} /> <span class="hidden text-[10px] text-[var(--al-muted)] xl:inline">Ctrl K</span>
			</button>
			<!-- Which engine is informational; it is the first thing to go. -->
			<span class="hidden text-xs text-[var(--al-muted)] 2xl:inline">engine</span>
			<span class="mono hidden rounded border border-[var(--al-border)] px-2 py-0.5 text-xs text-[var(--al-muted)] lg:inline">{engine || defaultEngine}</span>
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
				planChanges={runPane.changes}
				planning={busy === 'plan'}
				onPlan={auth.can('deploy') ? runPlan : undefined}
				loadTypes={editorLoadTypes}
				onAddType={editorAddType}
				onEditResource={editorEditResource}
				onDeleteResource={editorDeleteResource}
				onRenameResource={(id, name, mod) => resaveResource(id, { name }, mod)}
				onSetDependsOn={(id, deps, mod) => resaveResource(id, { dependsOn: deps }, mod)}
				onEditModuleCall={(name) => { showEditor = false; openCall(moduleCalls.find((m) => m.name === name)); }}
				onClose={() => (showEditor = false)}
			/>
		{:else}
		<StructureSidebar
			width={panels.sidebarWidth}
			{project}
			{providers}
			{providerUpdates}
			{bumping}
			{localModules}
			{moduleCalls}
			{files}
			{activeModule}
			addingProvider={busy === 'provider'}
			canResources={auth.can('resources')}
			onAddProvider={addProvider}
			onBumpProvider={bumpProvider}
			onAddAlias={addProviderAlias}
			onDeleteInstance={deleteProviderInstance}
			onConfigureProvider={configureProvider}
			onPickType={pickType}
			onSelectScope={selectScope}
			onEditResource={editResource}
			onDeleteResource={deleteScopedResource}
			onNewModule={() => { nmName = ''; showNewModule = true; }}
			onDeleteModule={deleteModule}
			onOpenCall={openCall}
			onDeleteCall={deleteCall}
			onOpenFile={openFile}
			onDeleteFile={deleteFile}
		/>
		<ResizeHandle
			orientation="vertical"
			label="Resize the structure panel"
			onResize={(dx) => panels.setSidebarWidth(panels.sidebarWidth + dx)}
			onEnd={() => panels.persistSidebar()}
		/>

		<ResourceFormPane
			hasProject={!!project}
			loadingSchema={busy === 'schema'}
			saving={busy === 'save'}
			anyBusy={busy !== ''}
			{activeModule}
			{references}
			secrets={secretNames}
			onCreateSecret={secretsWritable && auth.can('resources') ? createSecretInline : undefined}
			{providers}
			canResources={auth.can('resources')}
			canDeploy={auth.can('deploy')}
			onSaveResource={saveResource}
			onSaveProvider={saveProviderConfig}
			onImportResource={() => { importId = ''; showImportResource = true; }}
			onNewProject={newProject}
		/>

		<ResizeHandle
			orientation="vertical"
			label="Resize the generated-HCL panel"
			onResize={(dx) => panels.setHclWidth(panels.hclWidth - dx)}
			onEnd={() => panels.persistHcl()}
		/>
		<aside
			class="flex shrink-0 flex-col border-l"
			style="width:{panels.hclWidth}px;border-color:var(--al-border);"
			bind:clientHeight={panels.asideHeight}
		>
			<!-- Generated HCL — shares the graph editor's viewer for a consistent look -->
			<div class="min-h-0 flex-1">
				<HclView {hcl}>
					{#snippet actions()}
						<!-- approve is its own permission: a user who may sign a plan off but
						     not run one still needs this row (it renders only the Approve button). -->
						{#if auth.can('deploy') || auth.can('approve')}
							<DeployActions
								{busy}
								hasProject={!!project}
								{approvalRequired}
								{approvalReady}
								canDeploy={auth.can('deploy')}
								canApprove={auth.can('approve')}
								hasPlan={runPane.counts !== null || planPending !== null}
								onValidate={runValidate}
								onLint={runLint}
								onPlan={runPlan}
								onApprove={approvePlan}
								onApply={() => (showApplyConfirm = true)}
								onDestroy={() => { destroyName = ''; showDestroyConfirm = true; }}
							/>
						{/if}
					{/snippet}
				</HclView>
			</div>
			<ResizeHandle
				orientation="horizontal"
				label="Resize the run-output pane"
				onResize={(dy) => panels.setRunHeight(panels.runHeight - dy)}
				onEnd={() => panels.persistRun()}
			/>
			<!-- Plan output -->
			<RunOutputPane
				height={panels.runHeight}
				hasProject={!!project}
				{approvalRequired}
				approval={currentApproval}
				onToggleGate={auth.can('resources') ? toggleApprovalGate : undefined}
				onJump={jumpToAddress}
			/>
		</aside>
		{/if}
	</div>
</div>

<!-- New project modal -->
<NewProjectModal bind:open={showNewProject} bind:name={npName} bind:engine={npEngine} {engines} saving={busy === 'project'} onCreate={createProjectConfirmed} />

{#if auth.isAdmin}
	<SettingsModal bind:open={showSettings} />
{/if}

{#if project}
	<ProjectConfig
		config={scopeConfig}
		title={activeModule ? `Module "${activeModule}" — variables, locals & outputs` : 'Variables, locals & outputs'}
		bind:open={showConfig}
		saving={busy === 'config'}
		{references}
		secrets={secretNames}
		onCreateSecret={secretsWritable && auth.can('resources') ? createSecretInline : undefined}
		onSave={saveConfig}
	/>
	<SecretsModal
		bind:open={showSecrets}
		secrets={secretsList}
		writable={secretsWritable}
		backend={secretsBackend}
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
		busy={busy === 'import' || busy === 'state-rm' || busy === 'state-mv' || busy === 'init-backend' || busy === 'force-unlock'}
		canDeploy={auth.can('deploy')}
		backendType={project.backend?.type ?? ''}
		drift={driftInfo}
		onSaveDrift={auth.can('deploy') ? saveDrift : undefined}
		onConfigureBackend={auth.can('resources') ? openBackend : undefined}
		onRefresh={() => { loadState(); loadDrift(); }}
		onRm={(address) => runStateOp('state-rm', { address })}
		onMv={(from, to) => runStateOp('state-mv', { from, to })}
		onAcceptDrift={auth.can('deploy') ? () => runStateOp('accept-drift', {}) : undefined}
		onTaint={(address) => runStateOp('taint', { address })}
		onUntaint={(address) => runStateOp('untaint', { address })}
		onForceUnlock={auth.can('deploy') ? (lockId) => runStateOp('force-unlock', { lockId }) : undefined}
	/>

	<!-- terraform block: required_version + state backend -->
	<TerraformBlockModal
		bind:open={showBackend}
		bind:requiredVersion={bkRequiredVersion}
		bind:type={bkType}
		bind:rows={bkRows}
		bind:mode={bkMode}
		canDeploy={auth.can('deploy')}
		saving={busy === 'backend'}
		onSave={saveBackend}
	/>

	<!-- Import a live resource into state -->
	<ImportResourceModal bind:open={showImportResource} bind:id={importId} address={importAddress} onImport={runImport} />

	<!-- Apply confirmation -->
	<ApplyConfirmModal
		bind:open={showApplyConfirm}
		engine={engine || defaultEngine}
		projectName={project.name}
		planResult={runPane.result}
		onApply={() => { showApplyConfirm = false; runOp('apply'); }}
	/>

	<!-- Delete blocked: project still has deployed resources -->
	<DeleteBlockedModal
		bind:open={showDeleteBlocked}
		bind:name={deleteBlockedName}
		projectName={project.name}
		deployedCount={deleteBlockedCount}
		onDelete={forceRemoveProject}
	/>

	<!-- Destroy confirmation -->
	<DestroyConfirmModal
		bind:open={showDestroyConfirm}
		bind:name={destroyName}
		engine={engine || defaultEngine}
		projectName={project.name}
		onDestroy={() => { showDestroyConfirm = false; runOp('destroy'); }}
	/>

	<!-- New module modal -->
	<NewModuleModal bind:open={showNewModule} bind:name={nmName} saving={busy === 'module'} onCreate={createModule} />

	<!-- Companion file modal (add / edit) -->
	<FileModal
		bind:open={showFile}
		editing={fileEditing}
		bind:path={filePath}
		bind:content={fileContent}
		saving={busy === 'file'}
		onSave={saveFile}
	/>

	<!-- Registry module modal (add / edit) -->
	<ModuleCallModal
		bind:open={showRegModule}
		editing={rmEditing}
		bind:name={rmName}
		bind:source={rmSource}
		bind:version={rmVersion}
		bind:rows={rmRows}
		bind:count={rmCount}
		bind:forEach={rmForEach}
		saving={busy === 'regmodule'}
		onSave={saveCall}
	/>

{/if}

<!-- Import HCL modal -->
<ImportHclModal
	bind:open={showImportHcl}
	bind:text={importHclText}
	bind:files={importHclFiles}
	scopeLabel={activeModule ? `module "${activeModule}"` : 'root module'}
	saving={busy === 'import'}
	onFiles={(l) => readFiles(l, 'hcl')}
	onImport={doImportHcl}
/>

<!-- Import state modal -->
<ImportStateModal
	bind:open={showImportState}
	bind:text={importStateText}
	bind:replace={replaceState}
	envName={activeEnv}
	saving={busy === 'import'}
	onFiles={(l) => readFiles(l, 'state')}
	onImport={doImportState}
/>
