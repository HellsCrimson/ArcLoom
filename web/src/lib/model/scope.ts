import type { Module, Project, Resource, ResourceKind } from '$lib/types';

// Pure queries over the project model: scope (root vs local module) lookups,
// plan-address resolution and the small naming/provider helpers the schema form
// and the canvas bridge both need. Nothing here touches reactive state — the
// caller passes the project in and acts on the result.

// scopeResources returns the resource list for a scope (module name or root).
export function scopeResources(project: Project | null, moduleName?: string): Resource[] {
	if (!project) return [];
	return moduleName
		? (project.localModules?.find((m) => m.name === moduleName)?.resources ?? [])
		: project.resources;
}

export function scopeDataSources(project: Project | null, moduleName?: string): Resource[] {
	if (!project) return [];
	return moduleName
		? (project.localModules?.find((m) => m.name === moduleName)?.dataSources ?? [])
		: (project.dataSources ?? []);
}

// findInScope resolves a node id to the full resource and whether it is a
// managed resource or a data source (canvas callbacks only carry the id).
export function findInScope(project: Project | null, id: string, moduleName?: string): { r: Resource; kind: ResourceKind } | null {
	const r = scopeResources(project, moduleName).find((x) => x.id === id);
	if (r) return { r, kind: 'resource' };
	const d = scopeDataSources(project, moduleName).find((x) => x.id === id);
	if (d) return { r: d, kind: 'data' };
	return null;
}

export const stripIndex = (addr: string) => addr.replace(/\[[^\]]*\]/g, '');

export type ResolvedAddress =
	| { kind: 'block'; r: Resource; resourceKind: ResourceKind; module?: string }
	| { kind: 'registryModule'; module: Module }
	| null;

// resolveAddress maps a plan/diff address to the model block it names. Instance
// indexes ("[0]", '["a"]') are stripped. Returns null when nothing matches (e.g.
// a planned delete already gone from the model).
//
// The `module.<x>` in an address is a CALL name, never a folder name — the two
// are independent now — so the call is looked up first and its source decides
// where to go: a local source resolves into that definition's scope, a registry
// one resolves to the call itself (there is no body to open).
export function resolveAddress(project: Project | null, moduleCalls: Module[], address: string): ResolvedAddress {
	if (!project) return null;
	let addr = stripIndex(address);
	let callName: string | undefined;
	const m = addr.match(/^module\.([A-Za-z0-9_-]+)\.(.+)$/);
	if (m) {
		callName = m[1];
		addr = m[2];
	}
	if (addr.startsWith('module.')) return null; // nested modules aren't in the model
	const kind: ResourceKind = addr.startsWith('data.') ? 'data' : 'resource';
	const [type, name] = addr.replace(/^data\./, '').split('.');
	if (!type || !name) return null;

	let scope: string | undefined;
	if (callName) {
		const call = moduleCalls.find((c) => c.name === callName);
		const folder = call ? localFolder(call) : '';
		if (!folder || !project.localModules?.some((lm) => lm.name === folder)) {
			return call ? { kind: 'registryModule', module: call } : null;
		}
		scope = folder;
	}
	const pool = kind === 'data' ? scopeDataSources(project, scope) : scopeResources(project, scope);
	const r = pool.find((x) => x.type === type && x.name === name);
	if (!r) return null;
	return { kind: 'block', r, resourceKind: kind, module: scope };
}

// localFolder mirrors model.Module.LocalFolder — the definition a call targets,
// or "" for a registry/remote source.
const LOCAL_SOURCE_PREFIX = './modules/';
export function localFolder(m: { source?: string }): string {
	const src = m.source ?? '';
	if (!src.startsWith(LOCAL_SOURCE_PREFIX)) return '';
	const folder = src.slice(LOCAL_SOURCE_PREFIX.length);
	return folder && !folder.includes('/') ? folder : '';
}

export function suggestName(pool: Resource[], type: string): string {
	const base = type.replace(/^[a-z0-9]+_/, '') || type;
	let n = base;
	let i = 1;
	const taken = new Set(pool.filter((r) => r.type === type).map((r) => r.name));
	while (taken.has(n)) n = `${base}_${++i}`;
	return n;
}

export function providerForType(project: Project | null, type: string, fallback: string): string {
	const key = type.split('_')[0];
	return project?.providers?.find((p) => p.key === key)?.source ?? fallback;
}

// providerVersionForSource returns the version constraint the project pins
// a provider source to, so schema fetches match the pinned version.
export function providerVersionForSource(project: Project | null, source: string): string | undefined {
	return project?.providers?.find((p) => p.source === source && p.version)?.version;
}

// referenceGroups buckets the reference picker's flat address list by what each
// address IS, so a project with many resources still yields a readable
// <optgroup> menu. The kind is derived from the address prefix rather than
// carried alongside it — same trick as secretGroups() — which keeps the
// `references: string[]` contract that every form editor is threaded with.
//
// Order is authoring order, not alphabetical: the values you parameterise a
// resource with (variables, locals) come before the things you wire it to.
const REFERENCE_GROUPS: { label: string; match: (ref: string) => boolean }[] = [
	{ label: 'Variables', match: (r) => r.startsWith('var.') },
	{ label: 'Locals', match: (r) => r.startsWith('local.') },
	{ label: 'Modules', match: (r) => r.startsWith('module.') },
	{ label: 'Data sources', match: (r) => r.startsWith('data.') },
	{ label: 'Resources', match: () => true }
];

export function referenceGroups(refs: string[]): { group: string; refs: string[] }[] {
	const buckets = new Map<string, string[]>();
	for (const ref of refs) {
		const label = REFERENCE_GROUPS.find((g) => g.match(ref))!.label;
		buckets.set(label, [...(buckets.get(label) ?? []), ref]);
	}
	return REFERENCE_GROUPS.map((g) => ({ group: g.label, refs: buckets.get(g.label) ?? [] })).filter(
		(g) => g.refs.length > 0
	);
}
