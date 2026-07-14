import type {
	EnginesResponse,
	NormalizedSchema,
	Project,
	ProjectConfig,
	ProjectSummary,
	ProviderResponse,
	Resource,
	SecretsResponse,
	GitStatus,
	GitCommit,
	AttrValue
} from '$lib/types';

async function json<T>(res: Response): Promise<T> {
	if (!res.ok) {
		let msg = res.statusText;
		try {
			const body = await res.json();
			if (body?.error) msg = body.error;
		} catch {
			/* ignore */
		}
		throw new Error(msg);
	}
	return res.json() as Promise<T>;
}

export const api = {
	engines: () => fetch('/api/engines').then((r) => json<EnginesResponse>(r)),

	addProvider: (source: string, engine: string) =>
		fetch('/api/providers', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source, engine })
		}).then((r) => json<ProviderResponse>(r)),

	providerSchema: (source: string, engine: string) =>
		fetch(
			`/api/providers/${encodeURIComponent(source)}/schema?engine=${encodeURIComponent(engine)}`
		).then((r) => json<NormalizedSchema>(r)),

	setProviderConfig: (
		projectId: string,
		key: string,
		body: { source?: string; version?: string; config: Record<string, AttrValue> }
	) =>
		fetch(`/api/projects/${projectId}/providers/${encodeURIComponent(key)}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	resourceSchema: (source: string, type: string, engine: string) =>
		fetch(
			`/api/providers/${encodeURIComponent(source)}/resources/${encodeURIComponent(type)}/schema?engine=${encodeURIComponent(engine)}`
		).then((r) => json<NormalizedSchema>(r)),

	listProjects: () =>
		fetch('/api/projects').then((r) => json<ProjectSummary[]>(r)),

	createProject: (name: string, engine: string) =>
		fetch('/api/projects', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name, engine })
		}).then((r) => json<Project>(r)),

	getProject: (id: string) => fetch(`/api/projects/${id}`).then((r) => json<Project>(r)),

	deleteProject: (id: string) =>
		fetch(`/api/projects/${id}`, { method: 'DELETE' }).then((r) => {
			if (!r.ok && r.status !== 204) throw new Error('failed to delete project');
		}),

	setConfig: (id: string, config: ProjectConfig, module?: string) =>
		fetch(`/api/projects/${id}/config`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ ...config, module: module ?? '' })
		}).then((r) => json<Project>(r)),

	listSecrets: (id: string) =>
		fetch(`/api/projects/${id}/secrets`).then((r) => json<SecretsResponse>(r)),

	setSecret: (id: string, name: string, value: string) =>
		fetch(`/api/projects/${id}/secrets/${encodeURIComponent(name)}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ value })
		}).then((r) => {
			if (!r.ok && r.status !== 204) return r.json().then((b) => { throw new Error(b.error || 'failed'); });
		}),

	deleteSecret: (id: string, name: string) =>
		fetch(`/api/projects/${id}/secrets/${encodeURIComponent(name)}`, { method: 'DELETE' }).then((r) => {
			if (!r.ok && r.status !== 204) throw new Error('failed to delete secret');
		}),

	deleteResource: (projectId: string, rid: string, module?: string) =>
		fetch(`/api/projects/${projectId}/resources/${rid}${module ? `?module=${encodeURIComponent(module)}` : ''}`, {
			method: 'DELETE'
		}).then((r) => {
			if (!r.ok && r.status !== 204) throw new Error('failed to delete resource');
		}),

	createLocalModule: (projectId: string, name: string) =>
		fetch(`/api/projects/${projectId}/modules/local`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name })
		}).then((r) => json<Project>(r)),

	deleteLocalModule: (projectId: string, name: string) =>
		fetch(`/api/projects/${projectId}/modules/local/${encodeURIComponent(name)}`, {
			method: 'DELETE'
		}).then((r) => json<Project>(r)),

	setModuleInputs: (projectId: string, name: string, inputs: Record<string, AttrValue>) =>
		fetch(`/api/projects/${projectId}/modules/local/${encodeURIComponent(name)}/inputs`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ inputs })
		}).then((r) => json<Project>(r)),

	upsertResource: (
		projectId: string,
		body: {
			id?: string;
			type: string;
			name: string;
			providerSource?: string;
			module?: string;
			attrs: Record<string, AttrValue>;
			dependsOn?: string[];
		}
	) =>
		fetch(`/api/projects/${projectId}/resources`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Resource>(r)),

	projectHCL: (id: string, module?: string) =>
		fetch(`/api/projects/${id}/hcl${module ? `?module=${encodeURIComponent(module)}` : ''}`).then((r) => {
			if (!r.ok) throw new Error('failed to generate HCL');
			return r.text();
		}),

	importHCL: (id: string, files: Record<string, string>) =>
		fetch(`/api/projects/${id}/import/hcl`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ files })
		}).then((r) => json<ImportResult>(r)),

	importState: (id: string, stateJSON: string) =>
		fetch(`/api/projects/${id}/import/state`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ state: stateJSON })
		}).then((r) => json<ImportResult>(r)),

	graph: (id: string) => fetch(`/api/projects/${id}/graph`).then((r) => json<GraphData>(r)),

	git: {
		status: (id: string) => fetch(`/api/projects/${id}/git/status`).then((r) => json<GitStatus>(r)),
		log: (id: string) => fetch(`/api/projects/${id}/git/log`).then((r) => json<GitCommit[]>(r)),
		diff: (id: string, commit?: string) =>
			fetch(`/api/projects/${id}/git/diff${commit ? `?commit=${commit}` : ''}`).then((r) =>
				json<{ diff: string }>(r).then((d) => d.diff)
			),
		commit: (id: string, message: string) =>
			fetch(`/api/projects/${id}/git/commit`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ message })
			}).then((r) => json<{ hash: string }>(r)),
		restore: (id: string, commit: string) =>
			fetch(`/api/projects/${id}/git/restore`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ commit })
			}).then((r) => json<Project>(r)),
		setRemote: (id: string, url: string) =>
			gitAction(`/api/projects/${id}/git/remote`, { url }),
		push: (id: string, username: string, token: string) =>
			gitAction(`/api/projects/${id}/git/push`, { username, token }),
		pull: (id: string, username: string, token: string) =>
			gitAction(`/api/projects/${id}/git/pull`, { username, token })
	}
};

async function gitAction(url: string, body: unknown): Promise<void> {
	const r = await fetch(url, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});
	if (!r.ok && r.status !== 204) {
		let msg = r.statusText;
		try {
			const b = await r.json();
			if (b?.error) msg = b.error;
		} catch {
			/* ignore */
		}
		throw new Error(msg);
	}
}

export interface ImportResult {
	addedResources: number;
	addedProviders: number;
	warnings?: string[];
}

export interface GraphData {
	nodes: { id: string; type: string }[];
	edges: { from: string; to: string }[];
	dot: string;
}

// PlanEvent types streamed over SSE.
export type PlanEvent =
	| { kind: 'log'; line: string }
	| { kind: 'result'; hasChanges: boolean }
	| { kind: 'error'; error: string }
	| { kind: 'done' };

// streamPlan opens an SSE connection and invokes onEvent for each event.
// Returns a function that closes the stream.
export function streamPlan(projectId: string, onEvent: (e: PlanEvent) => void): () => void {
	const es = new EventSource(`/api/projects/${projectId}/plan`);

	es.addEventListener('log', (ev) => {
		const d = JSON.parse((ev as MessageEvent).data);
		onEvent({ kind: 'log', line: d.line });
	});
	es.addEventListener('result', (ev) => {
		const d = JSON.parse((ev as MessageEvent).data);
		onEvent({ kind: 'result', hasChanges: d.hasChanges });
	});
	es.addEventListener('error', (ev) => {
		// Distinguish a server "error" event (has data) from a transport error.
		const data = (ev as MessageEvent).data;
		if (data) {
			try {
				const d = JSON.parse(data);
				onEvent({ kind: 'error', error: d.error ?? 'plan failed' });
			} catch {
				onEvent({ kind: 'error', error: 'plan failed' });
			}
		}
		es.close();
	});
	es.addEventListener('done', () => {
		onEvent({ kind: 'done' });
		es.close();
	});

	return () => es.close();
}
