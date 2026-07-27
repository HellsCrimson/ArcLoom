import type { Module, Project } from '$lib/types';
import type { PaletteItem } from '$lib/components/CommandPalette.svelte';
import type { ToolItem } from '$lib/components/ToolbarMenu.svelte';
import {
	Boxes, Database, FileDown, GitBranch, History, KeyRound, Layers, LayoutDashboard, ShieldCheck, Variable as VariableIcon, DollarSign, Bell
} from 'lucide-svelte';

// The command surface: the Ctrl+K palette, the top-bar actions and the global
// keyboard shortcuts are three renderings of ONE set of commands, so they live
// side by side here instead of drifting apart across the route file.
//
// The two catalogs are deliberately NOT merged — three asymmetries are real
// behaviour: the palette's editor command opens the canvas while the bar's
// toggles it (showEditorPane vs toggleEditor); the palette's import commands
// clear the import banner while the bar's overflow rows do not (openImportHcl
// vs showImportHcl, the in-bar Import button clears it itself); and Ctrl+Enter
// only calls preventDefault when the plan guard passes, so canPlan is a thunk.

/** Which permission gates the caller's user passes (data, never an auth import). */
export interface CommandPerms {
	resources: boolean;
	deploy: boolean;
	git: boolean;
	admin: boolean;
}

/** Everything a command can do, as lazy arrows owned by the route. */
export interface CommandActions {
	showEditorPane(): void;
	toggleEditor(): void;
	openConfig(): void;
	openSecrets(): void;
	openGit(): void;
	openRuns(): void;
	openStatePanel(): void;
	plan(): void;
	validate(): void;
	lint(): void;
	openApply(): void;
	openDestroy(): void;
	openCost(): void;
	openPolicy(): void;
	openNotify(): void;
	openTerraform(): void;
	openNewFile(): void;
	openEnvironments(): void;
	openImportHcl(): void;
	openImportState(): void;
	showImportHcl(): void;
	showImportState(): void;
	openImportMenu(): void;
	newProject(): void;
	openSettings(): void;
	openAcl(): void;
	jumpToAddress(addr: string): void;
	openRegistryModule(m: Module): void;
}

// Command palette entries: permission-filtered commands plus every resource
// across all scopes (global search; selecting one opens its form).
export function buildPaletteItems(a: {
	project: Project | null;
	registryModules: Module[];
	perms: CommandPerms;
	actions: CommandActions;
}): PaletteItem[] {
	const { project, registryModules, perms, actions } = a;
	const items: PaletteItem[] = [];
	const resItem = (addr: string): PaletteItem => ({
		id: `res:${addr}`,
		label: addr,
		group: 'resources',
		mono: true,
		run: () => actions.jumpToAddress(addr)
	});
	if (project) {
		items.push({ id: 'cmd:editor', label: 'Open visual editor', group: 'commands', run: actions.showEditorPane });
		items.push({ id: 'cmd:vars', label: 'Variables, locals & outputs', group: 'commands', run: actions.openConfig });
		items.push({ id: 'cmd:secrets', label: 'Secrets', group: 'commands', run: actions.openSecrets });
		if (perms.git) items.push({ id: 'cmd:git', label: 'Git', group: 'commands', run: actions.openGit });
		items.push({ id: 'cmd:runs', label: 'Run history', group: 'commands', run: actions.openRuns });
		items.push({ id: 'cmd:state', label: 'State & drift', group: 'commands', run: actions.openStatePanel });
		if (perms.deploy) {
			items.push({ id: 'cmd:plan', label: 'Plan', hint: 'Ctrl+Enter', group: 'commands', run: actions.plan });
			items.push({ id: 'cmd:validate', label: 'Validate', group: 'commands', run: actions.validate });
			items.push({ id: 'cmd:lint', label: 'Lint (tflint)', group: 'commands', run: actions.lint });
			items.push({ id: 'cmd:apply', label: 'Apply…', group: 'commands', run: actions.openApply });
			items.push({ id: 'cmd:destroy', label: 'Destroy…', group: 'commands', run: actions.openDestroy });
		}
		if (perms.resources) {
			items.push({ id: 'cmd:cost', label: 'Cost estimation', group: 'commands', run: actions.openCost });
			items.push({ id: 'cmd:policy', label: 'Policy as code', group: 'commands', run: actions.openPolicy });
			items.push({ id: 'cmd:notify', label: 'Notifications', group: 'commands', run: actions.openNotify });
			items.push({ id: 'cmd:terraform', label: 'Terraform block (required_version, backend)', group: 'commands', run: actions.openTerraform });
			items.push({ id: 'cmd:environments', label: 'Environments', group: 'commands', run: actions.openEnvironments });
			items.push({ id: 'cmd:new-file', label: 'Add file (template / script)', group: 'commands', run: actions.openNewFile });
			items.push({ id: 'cmd:import-hcl', label: 'Import HCL', group: 'commands', run: actions.openImportHcl });
			items.push({ id: 'cmd:import-state', label: 'Import state', group: 'commands', run: actions.openImportState });
		}
	}
	if (perms.resources) items.push({ id: 'cmd:new-project', label: 'New project', group: 'commands', run: actions.newProject });
	if (perms.admin) items.push({ id: 'cmd:settings', label: 'Settings', group: 'commands', run: actions.openSettings });
	if (perms.admin && project) items.push({ id: 'cmd:access', label: 'Project access', group: 'commands', run: actions.openAcl });
	if (project) {
		for (const r of project.resources) items.push(resItem(`${r.type}.${r.name}`));
		for (const d of project.dataSources ?? []) items.push(resItem(`data.${d.type}.${d.name}`));
		for (const lm of project.localModules ?? []) {
			for (const r of lm.resources) items.push(resItem(`module.${lm.name}.${r.type}.${r.name}`));
			for (const d of lm.dataSources ?? []) items.push(resItem(`module.${lm.name}.data.${d.type}.${d.name}`));
		}
		for (const m of registryModules) {
			items.push({ id: `res:module.${m.name}`, label: `module.${m.name}`, hint: m.source, group: 'resources', mono: true, run: () => actions.openRegistryModule(m) });
		}
	}
	return items;
}

// Actions in priority order — earlier stays in the bar longer.
export function buildToolItems(a: {
	hasProject: boolean;
	configCount: number;
	secretCount: number;
	editorActive: boolean;
	perms: CommandPerms;
	actions: CommandActions;
}): ToolItem[] {
	const { hasProject, configCount, secretCount, editorActive, perms, actions } = a;
	if (!hasProject) return [];
	const items: ToolItem[] = [
		{ id: 'editor', label: 'Editor', icon: LayoutDashboard, run: actions.toggleEditor, active: editorActive, priority: 0 },
		{ id: 'runs', label: 'Runs', icon: History, run: actions.openRuns, priority: 1 },
		{ id: 'state', label: 'State', icon: Layers, run: actions.openStatePanel, priority: 1 }
	];
	// Variables and Secrets are readable by anyone (the panels gate their own
	// edit actions), matching the pre-existing bar.
	items.push({
		id: 'variables', label: 'Variables', icon: VariableIcon, run: actions.openConfig,
		badge: configCount > 0 ? String(configCount) : undefined, priority: 2
	});
	items.push({
		id: 'secrets', label: 'Secrets', icon: KeyRound, run: actions.openSecrets,
		badge: secretCount > 0 ? String(secretCount) : undefined, priority: 2
	});
	if (perms.git) {
		items.push({ id: 'git', label: 'Git', icon: GitBranch, run: actions.openGit, priority: 2 });
	}
	if (perms.resources) {
		items.push({ id: 'import', label: 'Import', icon: FileDown, run: actions.openImportMenu, priority: 3 });
		items.push({ id: 'environments', label: 'Environments', icon: Boxes, run: actions.openEnvironments, title: 'Manage deployment environments', priority: 4 });
		items.push({ id: 'cost', label: 'Cost', icon: DollarSign, run: actions.openCost, title: 'Cost estimation settings', priority: 5 });
		items.push({ id: 'policy', label: 'Policy', icon: ShieldCheck, run: actions.openPolicy, title: 'Policy as code (conftest/OPA)', priority: 5 });
		items.push({ id: 'notify', label: 'Notify', icon: Bell, run: actions.openNotify, title: 'Webhook / Slack notifications for applies and drift', priority: 5 });
	}
	if (perms.admin) {
		items.push({ id: 'acl', label: 'Access', icon: ShieldCheck, run: actions.openAcl, title: 'Manage who can access this project', priority: 6 });
	}
	return items;
}

// Import keeps its submenu in the bar, but in the overflow list its two
// actions are flat rows — a menu inside a menu would be worse than either.
export function buildImportRows(actions: CommandActions): ToolItem[] {
	return [
		{ id: 'import-hcl', label: 'Import HCL', icon: FileDown, run: actions.showImportHcl, priority: 3 },
		{ id: 'import-state', label: 'Import state', icon: Database, run: actions.showImportState, priority: 3 }
	];
}

// Global shortcuts. Ctrl+K is matched BEFORE the editable-target guard so the
// palette also opens from inside an input; the undo/redo keys are not, because
// text inputs have their own native undo.
export function globalKeydown(
	e: KeyboardEvent,
	h: { togglePalette(): void; canPlan(): boolean; plan(): void; undo(): void; redo(): void }
): void {
	if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
	const k = e.key.toLowerCase();
	// Ctrl+K opens the command palette from anywhere, including inputs.
	if (k === 'k') {
		e.preventDefault();
		h.togglePalette();
		return;
	}
	const t = e.target as HTMLElement | null;
	// Leave native undo alone inside text inputs.
	if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)) return;
	if (k === 'enter') {
		// Ctrl+Enter runs a plan (matches the palette hint).
		if (h.canPlan()) {
			e.preventDefault();
			h.plan();
		}
	} else if (k === 'z' && !e.shiftKey) {
		e.preventDefault();
		h.undo();
	} else if ((k === 'z' && e.shiftKey) || k === 'y') {
		e.preventDefault();
		h.redo();
	}
}
