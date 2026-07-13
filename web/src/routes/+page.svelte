<script lang="ts">
	import { onMount } from 'svelte';
	import { api, streamPlan } from '$lib/api/client';
	import type { AttrValue, Engine, NormalizedSchema, Project, ProjectSummary } from '$lib/types';
	import SchemaForm from '$lib/form/SchemaForm.svelte';
	import { Play, Save, Loader2, Boxes, Plus, Trash2, FolderGit2 } from 'lucide-svelte';

	const LAST_PROJECT_KEY = 'arcloom.lastProject';

	// ---- app state ----
	let engines = $state<Engine[]>([]);
	let defaultEngine = $state('tofu');

	let projects = $state<ProjectSummary[]>([]);
	let project = $state<Project | null>(null); // full current project
	let engine = $state(''); // engine of current project

	let providerSource = $state('hashicorp/random');
	let resourceTypes = $state<string[]>([]);
	let selectedType = $state('');
	let schema = $state<NormalizedSchema | null>(null);

	let resourceName = $state('example');
	let editingId = $state<string | null>(null); // set when editing an existing resource
	let values = $state<Record<string, AttrValue>>({});

	let hcl = $state('');
	let planLog = $state<string[]>([]);
	let planResult = $state<string>('');

	let busy = $state<string>('');
	let error = $state('');

	const savedResources = $derived(project?.resources ?? []);
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
			// Prefill the provider box from the project's first provider, if any.
			if (project.providers?.length) providerSource = project.providers[0].source;
		} catch (err) {
			error = (err as Error).message;
		}
	}

	async function newProject() {
		const name = prompt('New project name?', 'my-infra');
		if (!name) return;
		busy = 'project';
		try {
			const p = await api.createProject(name, defaultEngine);
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

	// Start creating a brand-new resource of the given type.
	async function pickType(t: string) {
		error = '';
		busy = 'schema';
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

	// Load an existing resource into the form for editing.
	async function editResource(r: { id: string; type: string; name: string; attrs: Record<string, AttrValue> }) {
		error = '';
		busy = 'schema';
		selectedType = r.type;
		editingId = r.id;
		resourceName = r.name;
		// Plain deep-clone (JSON) rather than structuredClone: r.attrs is a Svelte
		// reactive proxy, which structuredClone cannot clone.
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
			project = await api.getProject(project.id); // reload resources + providers
			await refreshProjects();
			await refreshHcl();
			editingId = editingId ?? project.resources.find((r) => r.name === resourceName && r.type === selectedType)?.id ?? null;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			busy = '';
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

	const btn =
		'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-40';
	const btnPrimary = `${btn} bg-[var(--al-accent)] text-white hover:brightness-110`;
	const btnGhost = `${btn} border border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]`;
	const iconBtn = 'rounded-md p-1.5 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)] hover:text-[var(--al-text)]';
</script>

<div class="flex h-screen flex-col">
	<!-- Top bar -->
	<header class="flex items-center gap-3 border-b border-[var(--al-border)] bg-[var(--al-panel)] px-4 py-2">
		<div class="flex items-center gap-2 font-semibold">
			<Boxes size={18} class="text-[var(--al-accent)]" />
			ArcLoom
		</div>

		<!-- Project switcher -->
		<div class="ml-2 flex items-center gap-1.5">
			<FolderGit2 size={15} class="text-[var(--al-muted)]" />
			<select
				class="rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm"
				value={project?.id ?? ''}
				onchange={(e) => selectProject(e.currentTarget.value)}
			>
				{#if projects.length === 0}
					<option value="" disabled selected>No projects</option>
				{/if}
				{#each projects as p (p.id)}
					<option value={p.id}>{p.name} · {p.resourceCount}</option>
				{/each}
			</select>
			<button class={iconBtn} title="New project" onclick={newProject} aria-label="New project">
				<Plus size={16} />
			</button>
			{#if project}
				<button class={iconBtn} title="Delete project" onclick={removeProject} aria-label="Delete project">
					<Trash2 size={16} />
				</button>
			{/if}
		</div>

		<div class="ml-auto flex items-center gap-2 text-sm">
			<span class="text-xs text-[var(--al-muted)]">engine</span>
			<span class="mono rounded border border-[var(--al-border)] px-2 py-0.5 text-xs text-[var(--al-muted)]"
				>{engine || defaultEngine}</span
			>
		</div>
	</header>

	{#if error}
		<div class="border-b border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-4 py-1.5 text-sm text-[var(--al-danger)]">
			{error}
		</div>
	{/if}

	<div class="flex min-h-0 flex-1">
		<!-- Left: providers + resource types + project resources -->
		<aside class="w-64 shrink-0 space-y-3 overflow-y-auto border-r border-[var(--al-border)] bg-[var(--al-panel)] p-3">
			{#if project}
				<div>
					<label for="provider-source" class="mb-1 block text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]"
						>Provider</label
					>
					<div class="flex gap-1.5">
						<input
							id="provider-source"
							class="min-w-0 flex-1 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
							bind:value={providerSource}
							placeholder="hashicorp/random"
						/>
						<button class={btnGhost} onclick={addProvider} disabled={busy === 'provider'}>
							{#if busy === 'provider'}<Loader2 size={14} class="animate-spin" />{:else}Add{/if}
						</button>
					</div>
				</div>

				{#if resourceTypes.length > 0}
					<div>
						<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
							Resource types
						</div>
						<ul class="space-y-0.5">
							{#each resourceTypes as t (t)}
								<li>
									<button
										class="mono w-full rounded px-2 py-1 text-left text-xs {selectedType === t && !editingId
											? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]'
											: 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}"
										onclick={() => pickType(t)}
									>
										{t}
									</button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}

				{#if savedResources.length > 0}
					<div>
						<div class="mb-1 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
							In project
						</div>
						<ul class="space-y-0.5">
							{#each savedResources as r (r.id)}
								<li>
									<button
										class="mono w-full truncate rounded px-2 py-1 text-left text-[11px] {editingId === r.id
											? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]'
											: 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}"
										onclick={() => editResource(r)}
										title="Edit {r.type}.{r.name}"
									>
										{r.type}.{r.name}
									</button>
								</li>
							{/each}
						</ul>
					</div>
				{/if}
			{:else}
				<p class="text-xs text-[var(--al-muted)]">Create a project to begin.</p>
			{/if}
		</aside>

		<!-- Center: form -->
		<main class="min-w-0 flex-1 overflow-y-auto p-5">
			{#if !project}
				<div class="grid h-full place-items-center text-sm text-[var(--al-muted)]">
					<button class={btnPrimary} onclick={newProject}><Plus size={15} /> New project</button>
				</div>
			{:else if busy === 'schema'}
				<div class="flex items-center gap-2 text-sm text-[var(--al-muted)]">
					<Loader2 size={16} class="animate-spin" /> Loading schema…
				</div>
			{:else if schema}
				<div class="mb-4 flex items-end gap-3">
					<div>
						<label for="resource-name" class="mb-1 block text-xs text-[var(--al-muted)]"
							>resource name</label
						>
						<input
							id="resource-name"
							class="mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)]"
							bind:value={resourceName}
						/>
					</div>
					<div class="mono text-sm text-[var(--al-muted)]">
						resource "<span class="text-[var(--al-accent)]">{selectedType}</span>" "{resourceName}"
						{#if editingId}<span class="ml-1 text-[10px] uppercase">editing</span>{/if}
					</div>
					<button class={btnPrimary + ' ml-auto'} onclick={saveResource} disabled={busy === 'save'}>
						<Save size={15} /> {editingId ? 'Update' : 'Save'} resource
					</button>
				</div>
				<SchemaForm {schema} bind:values {references} />
			{:else}
				<div class="grid h-full place-items-center text-sm text-[var(--al-muted)]">
					Add a provider and pick a resource type, or select an existing resource to edit.
				</div>
			{/if}
		</main>

		<!-- Right: HCL + plan -->
		<aside class="flex w-[26rem] shrink-0 flex-col border-l border-[var(--al-border)] bg-[var(--al-panel)]">
			<div class="flex items-center justify-between border-b border-[var(--al-border)] px-3 py-2">
				<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]"
					>Generated HCL</span
				>
				<button class={btnGhost} onclick={runPlan} disabled={!project || busy === 'plan'}>
					{#if busy === 'plan'}<Loader2 size={14} class="animate-spin" />{:else}<Play size={14} />{/if}
					Plan
				</button>
			</div>
			<pre class="mono max-h-[45%] overflow-auto whitespace-pre-wrap px-3 py-2 text-[11px] text-[var(--al-text)]">{hcl ||
					'// save a resource to preview HCL'}</pre>

			<div class="border-t border-[var(--al-border)] px-3 py-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				Plan output
				{#if planResult}
					<span class="ml-2 rounded bg-[var(--al-accent-weak)] px-1.5 py-0.5 text-[10px] normal-case text-[var(--al-accent)]"
						>{planResult}</span
					>
				{/if}
			</div>
			<pre class="mono flex-1 overflow-auto whitespace-pre-wrap bg-[var(--al-bg)] px-3 py-2 text-[11px] text-[var(--al-muted)]">{planLog.join(
					'\n'
				) || '// run plan to see output'}</pre>
		</aside>
	</div>
</div>
