import type {
	EnginesResponse,
	NormalizedSchema,
	Project,
	ProjectSummary,
	ProviderResponse,
	Resource,
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

	upsertResource: (
		projectId: string,
		body: {
			id?: string;
			type: string;
			name: string;
			providerSource?: string;
			attrs: Record<string, AttrValue>;
		}
	) =>
		fetch(`/api/projects/${projectId}/resources`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Resource>(r)),

	projectHCL: (id: string) =>
		fetch(`/api/projects/${id}/hcl`).then((r) => {
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

	graph: (id: string) => fetch(`/api/projects/${id}/graph`).then((r) => json<GraphData>(r))
};

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
