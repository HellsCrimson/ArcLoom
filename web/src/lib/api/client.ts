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
	GitBranch,
	GitCredMeta,
	AuditEntry,
	ProjectACL,
	AttrValue,
	DriftConfig,
	DriftStatus,
	ProviderUpdate
} from '$lib/types';
import { auth } from '$lib/stores/auth.svelte';

// on401 clears the auth store so the layout gate returns to the login screen
// when a session expires or is revoked mid-use.
function on401(status: number) {
	if (status === 401) auth.clear();
}

// DeleteBlockedError signals that a project delete was refused because its
// persisted state still tracks deployed resources.
export class DeleteBlockedError extends Error {
	deployed: number;
	constructor(message: string, deployed: number) {
		super(message);
		this.deployed = deployed;
	}
}

async function json<T>(res: Response): Promise<T> {
	if (!res.ok) {
		on401(res.status);
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

	addProvider: (source: string, engine: string, version?: string) =>
		fetch('/api/providers', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ source, engine, version: version || undefined })
		}).then((r) => json<ProviderResponse>(r)),

	providerSchema: (source: string, engine: string, version?: string) =>
		fetch(
			`/api/providers/${encodeURIComponent(source)}/schema?engine=${encodeURIComponent(engine)}${version ? `&version=${encodeURIComponent(version)}` : ''}`
		).then((r) => json<NormalizedSchema>(r)),

	setProviderConfig: (
		projectId: string,
		key: string,
		body: { alias?: string; source?: string; version?: string; config: Record<string, AttrValue> }
	) =>
		fetch(`/api/projects/${projectId}/providers/${encodeURIComponent(key)}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	providerUpdates: (projectId: string) =>
		fetch(`/api/projects/${projectId}/provider-updates`).then((r) => json<ProviderUpdate[]>(r)),

	deleteProviderInstance: (projectId: string, key: string, alias?: string) =>
		fetch(
			`/api/projects/${projectId}/providers/${encodeURIComponent(key)}${alias ? `?alias=${encodeURIComponent(alias)}` : ''}`,
			{ method: 'DELETE' }
		).then((r) => json<Project>(r)),

	resourceSchema: (source: string, type: string, engine: string, version?: string) =>
		fetch(
			`/api/providers/${encodeURIComponent(source)}/resources/${encodeURIComponent(type)}/schema?engine=${encodeURIComponent(engine)}${version ? `&version=${encodeURIComponent(version)}` : ''}`
		).then((r) => json<NormalizedSchema>(r)),

	dataSourceSchema: (source: string, type: string, engine: string, version?: string) =>
		fetch(
			`/api/providers/${encodeURIComponent(source)}/datasources/${encodeURIComponent(type)}/schema?engine=${encodeURIComponent(engine)}${version ? `&version=${encodeURIComponent(version)}` : ''}`
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

	// replaceProject swaps the whole editable model (undo/redo restore). The
	// server preserves identity fields and the state backend.
	replaceProject: (id: string, body: Project) =>
		fetch(`/api/projects/${id}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	listRuns: (id: string) => fetch(`/api/projects/${id}/runs`).then((r) => json<RunRecord[]>(r)),

	stateList: (id: string, env = '') =>
		fetch(`/api/projects/${id}/state${env ? `?env=${encodeURIComponent(env)}` : ''}`).then((r) =>
			json<StateEntry[]>(r)
		),

	getEnvironments: (id: string) =>
		fetch(`/api/projects/${id}/environments`).then((r) => json<Environment[]>(r)),

	getApproval: (id: string, env = '') =>
		fetch(`/api/projects/${id}/approval${env ? `?env=${encodeURIComponent(env)}` : ''}`).then((r) =>
			json<ApprovalState>(r)
		),

	setApprovalPolicy: (id: string, required: boolean) =>
		fetch(`/api/projects/${id}/approval`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ required })
		}).then((r) => json<ApprovalState>(r)),

	approve: (id: string, env: string, summary: { add: number; change: number; destroy: number; replace: number }) =>
		fetch(`/api/projects/${id}/approve${env ? `?env=${encodeURIComponent(env)}` : ''}`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(summary)
		}).then((r) => json<Approval>(r)),

	setEnvironments: (id: string, environments: Environment[]) =>
		fetch(`/api/projects/${id}/environments`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ environments })
		}).then((r) => json<Environment[]>(r)),

	setBackend: (id: string, body: { type: string; config: Record<string, AttrValue> }) =>
		fetch(`/api/projects/${id}/backend`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	getRun: (id: string, rid: string) =>
		fetch(`/api/projects/${id}/runs/${rid}`).then((r) => json<RunRecord>(r)),

	getDrift: (id: string) =>
		fetch(`/api/projects/${id}/drift`).then((r) => json<DriftInfo>(r)),

	setDrift: (id: string, config: DriftConfig) =>
		fetch(`/api/projects/${id}/drift`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config)
		}).then((r) => json<{ config: DriftConfig }>(r)),

	getCost: (id: string) =>
		fetch(`/api/projects/${id}/cost`).then((r) => json<{ config: CostConfig }>(r)),

	setCost: (id: string, config: CostConfig) =>
		fetch(`/api/projects/${id}/cost`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config)
		}).then((r) => json<{ config: CostConfig }>(r)),

	deleteProject: async (id: string, force = false) => {
		const res = await fetch(`/api/projects/${id}${force ? '?force=true' : ''}`, { method: 'DELETE' });
		if (res.status === 409) {
			// The project still has deployed resources; the caller decides
			// whether to destroy first or force the delete.
			const body = await res.json().catch(() => ({}) as { error?: string; deployed?: number });
			throw new DeleteBlockedError(body.error ?? 'project has deployed resources', body.deployed ?? 0);
		}
		if (!res.ok && res.status !== 204) {
			on401(res.status);
			throw new Error('failed to delete project');
		}
	},

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

	deleteResource: (projectId: string, rid: string, module?: string, kind?: 'resource' | 'data') => {
		const q = new URLSearchParams();
		if (module) q.set('module', module);
		if (kind === 'data') q.set('kind', 'data');
		const qs = q.toString();
		return fetch(`/api/projects/${projectId}/resources/${rid}${qs ? `?${qs}` : ''}`, {
			method: 'DELETE'
		}).then((r) => {
			if (!r.ok && r.status !== 204) throw new Error('failed to delete resource');
		});
	},

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

	setRegistryModule: (
		projectId: string,
		body: { name: string; source: string; version?: string; config: Record<string, AttrValue> }
	) =>
		fetch(`/api/projects/${projectId}/modules/registry`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	deleteRegistryModule: (projectId: string, name: string) =>
		fetch(`/api/projects/${projectId}/modules/registry/${encodeURIComponent(name)}`, {
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
			kind?: 'resource' | 'data';
			type: string;
			name: string;
			providerSource?: string;
			module?: string;
			attrs: Record<string, AttrValue>;
			dependsOn?: string[];
			count?: string;
			forEach?: string;
			provider?: string;
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

	importHCL: (id: string, files: Record<string, string>, module?: string) =>
		fetch(`/api/projects/${id}/import/hcl`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ files, module: module ?? '' })
		}).then((r) => json<ImportResult>(r)),

	importState: (id: string, stateJSON: string) =>
		fetch(`/api/projects/${id}/import/state`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ state: stateJSON })
		}).then((r) => json<ImportResult>(r)),

	graph: (id: string) => fetch(`/api/projects/${id}/graph`).then((r) => json<GraphData>(r)),

	admin: {
		listUsers: () => fetch('/api/admin/users', { credentials: 'same-origin' }).then((r) => json<AdminUser[]>(r)),
		createUser: (body: {
			username: string;
			password: string;
			email?: string;
			admin?: boolean;
			permissions?: string[];
		}) =>
			fetch('/api/admin/users', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((r) => json<AdminUser>(r)),
		updateUser: (
			id: string,
			body: { email?: string; admin?: boolean; permissions?: string[]; disabled?: boolean }
		) =>
			fetch(`/api/admin/users/${id}`, {
				method: 'PUT',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((r) => json<AdminUser>(r)),
		resetPassword: (id: string, newPassword: string) =>
			gitAction(`/api/admin/users/${id}/password`, { newPassword }),
		deleteUser: (id: string) =>
			fetch(`/api/admin/users/${id}`, { method: 'DELETE', credentials: 'same-origin' }).then((r) => {
				if (!r.ok && r.status !== 204) return r.json().then((b) => { throw new Error(b.error || 'failed'); });
			}),
		getSSO: () => fetch('/api/admin/settings/sso', { credentials: 'same-origin' }).then((r) => json<SSOSettings>(r)),
		setSSO: (body: SSOUpdate) =>
			fetch('/api/admin/settings/sso', {
				method: 'PUT',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((r) => json<SSOSettings>(r)),
		setLicense: (token: string) =>
			fetch('/api/admin/settings/license', {
				method: 'PUT',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ token })
			}).then((r) => json<LicenseStatus>(r)),
		listAudit: (params?: { project?: string; user?: string; limit?: number }) => {
			const q = new URLSearchParams();
			if (params?.project) q.set('project', params.project);
			if (params?.user) q.set('user', params.user);
			if (params?.limit) q.set('limit', String(params.limit));
			const qs = q.toString();
			return fetch(`/api/admin/audit${qs ? `?${qs}` : ''}`, { credentials: 'same-origin' }).then((r) =>
				json<AuditEntry[]>(r)
			);
		},
		setACL: (projectId: string, entries: { userId: string; perms: string[] }[]) =>
			fetch(`/api/projects/${projectId}/acl`, {
				method: 'PUT',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ entries })
			}).then((r) => json<ProjectACL>(r))
	},

	getACL: (projectId: string) =>
		fetch(`/api/projects/${projectId}/acl`, { credentials: 'same-origin' }).then((r) => json<ProjectACL>(r)),

	getLicense: () => fetch('/api/license', { credentials: 'same-origin' }).then((r) => json<LicenseStatus>(r)),

	git: {
		status: (id: string) => fetch(`/api/projects/${id}/git/status`).then((r) => json<GitStatus>(r)),
		log: (id: string) => fetch(`/api/projects/${id}/git/log`).then((r) => json<GitCommit[]>(r)),
		diff: (id: string, commit?: string) =>
			fetch(`/api/projects/${id}/git/diff${commit ? `?commit=${commit}` : ''}`).then((r) =>
				json<{ diff: string }>(r).then((d) => d.diff)
			),
		commit: (id: string, message: string, paths?: string[]) =>
			fetch(`/api/projects/${id}/git/commit`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(paths && paths.length ? { message, paths } : { message })
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
			gitAction(`/api/projects/${id}/git/pull`, { username, token }),
		branches: (id: string) =>
			fetch(`/api/projects/${id}/git/branches`).then((r) => json<GitBranch[]>(r)),
		createBranch: (id: string, name: string) =>
			gitAction(`/api/projects/${id}/git/branch`, { name }),
		deleteBranch: (id: string, name: string) =>
			gitAction(`/api/projects/${id}/git/branch?name=${encodeURIComponent(name)}`, undefined, 'DELETE'),
		checkout: (id: string, name: string) =>
			fetch(`/api/projects/${id}/git/checkout`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name })
			}).then((r) => json<Project>(r)),
		merge: (id: string, name: string) =>
			fetch(`/api/projects/${id}/git/merge`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name })
			}).then((r) => json<Project>(r)),
		credentials: (id: string) =>
			fetch(`/api/projects/${id}/git/credentials`).then((r) => json<GitCredMeta>(r)),
		setCredentials: (
			id: string,
			body: { authType: string; username?: string; token?: string; sshKey?: string; passphrase?: string }
		) =>
			fetch(`/api/projects/${id}/git/credentials`, {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((r) => json<GitCredMeta>(r)),
		deleteCredentials: (id: string) =>
			gitAction(`/api/projects/${id}/git/credentials`, undefined, 'DELETE')
	}
};

async function gitAction(url: string, body: unknown, method = 'POST'): Promise<void> {
	const r = await fetch(url, {
		method,
		...(body === undefined
			? {}
			: { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
	});
	if (!r.ok && r.status !== 204) {
		on401(r.status);
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

export interface AdminUser {
	id: string;
	username: string;
	email?: string;
	provider: string;
	admin: boolean;
	permissions: string[];
	disabled: boolean;
	createdAt: string;
}

export interface SSOSettings {
	enabled: boolean;
	issuer: string;
	clientId: string;
	redirectUrl: string;
	scopes: string;
	hasSecret: boolean;
	secretStorable: boolean;
	active: boolean;
}

export interface SSOUpdate {
	enabled: boolean;
	issuer: string;
	clientId: string;
	clientSecret?: string;
	redirectUrl: string;
	scopes: string;
}

export interface LicenseStatus {
	tier: 'community' | 'enterprise';
	licensee?: string;
	features: string[];
	seatLimit: number; // 0 = unlimited
	seatsUsed: number;
	expiresAt?: string;
	expired: boolean;
	keyConfigured: boolean;
}

export interface ImportResult {
	addedResources: number;
	addedProviders: number;
	addedVariables?: number;
	addedLocals?: number;
	addedOutputs?: number;
	addedDataSources?: number;
	addedModules?: number;
	addedLocalModules?: number;
	warnings?: string[];
}

export interface GraphData {
	nodes: { id: string; type: string }[];
	edges: { from: string; to: string }[];
	dot: string;
}

// PlanChange is one resource's pending action from the structured plan.
export interface PlanChange {
	address: string;
	moduleAddress?: string;
	mode: 'managed' | 'data';
	type: string;
	name: string;
	action: 'create' | 'update' | 'replace' | 'delete' | 'read';
}

export interface PlanSummary {
	hasChanges: boolean;
	changes: PlanChange[] | null;
	add: number;
	change: number;
	destroy: number;
	replace: number;
	cost?: CostSummary;
}

// CostLine is one resource's estimated monthly cost.
export interface CostLine {
	address: string;
	type: string;
	monthly: number;
	source: 'override' | 'infracost';
}

// CostSummary is a plan's estimated monthly cost, merged from price overrides
// and (optionally) Infracost.
export interface CostSummary {
	currency: string;
	totalMonthly: number;
	lines: CostLine[];
	infracostUsed: boolean;
	error?: string;
}

// LintIssue is one tflint finding.
export interface LintIssue {
	rule: string;
	severity: 'error' | 'warning' | 'notice';
	message: string;
	filename?: string;
	line?: number;
}

// LintSummary is a tflint run's findings.
export interface LintSummary {
	available: boolean;
	issues: LintIssue[];
	errorCount: number;
	warningCount: number;
}

// CostOverride assigns a flat monthly price to a resource type or address.
export interface CostOverride {
	target: string;
	monthly: number;
	note?: string;
}

// CostConfig is a project's cost-estimation config.
export interface CostConfig {
	enabled: boolean;
	overrides: CostOverride[];
}

// Environment is one deployment target (dev/staging/prod): a per-env backend
// config (partial `-backend-config` values) + variable value overrides. Kept
// server-side out of HCL/Git; selecting one scopes deploy/state ops to its own
// run/envs/<name>/ workspace.
export interface Environment {
	name: string;
	backendConfig?: Record<string, string>;
	vars?: Record<string, string>;
}

// Approval records that a specific saved plan was signed off for a gated apply.
export interface Approval {
	id: string;
	projectId: string;
	env?: string;
	add: number;
	change: number;
	destroy: number;
	replace: number;
	approvedBy: string;
	approvedAt: string;
	consumed: boolean;
}

// ApprovalState is the gate status for a project + environment.
export interface ApprovalState {
	required: boolean;
	approval: Approval | null;
}

// RunOutput is one root output value captured after an apply.
export interface RunOutput {
	name: string;
	value: string; // raw JSON encoding
	sensitive: boolean;
}

// StateEntry is one resource tracked in the project's persisted state.
export interface StateEntry {
	address: string;
	mode: 'managed' | 'data';
	type: string;
	name: string;
	module?: string;
	instances?: string[];
}

// DriftInfo is a project's drift schedule plus (once a check has run) the
// latest check outcome.
export interface DriftInfo {
	config: DriftConfig;
	status?: DriftStatus;
}

// RunRecord is one recorded operation (plan/apply/destroy or state surgery).
export interface RunRecord {
	id: string;
	projectId: string;
	op: 'plan' | 'apply' | 'destroy' | 'import' | 'state-rm' | 'state-mv' | 'init-backend' | 'validate' | 'lint' | 'drift';
	env?: string;
	user: string;
	startedAt: string;
	finishedAt: string;
	status: 'success' | 'error';
	error?: string;
	plan?: PlanSummary;
	lint?: LintSummary;
	outputs?: RunOutput[];
	log?: string;
}

// PlanEvent types streamed over SSE. A plan's result carries the structured
// diff; an apply's result carries the captured outputs; a validate's result
// carries the diagnostic counts (the diagnostics themselves stream as log
// lines).
export type PlanEvent =
	| { kind: 'log'; line: string }
	| ({
			kind: 'result';
			outputs?: RunOutput[];
			valid?: boolean;
			errorCount?: number;
			warningCount?: number;
			available?: boolean;
			lint?: LintSummary;
	  } & Partial<PlanSummary>)
	| { kind: 'error'; error: string }
	| { kind: 'done' };

// streamPlan opens an SSE connection for a read-only streamed op (plan, validate
// or lint — all GETs consumed via EventSource) and invokes onEvent for each
// event. Returns a function that closes the stream.
export function streamPlan(
	projectId: string,
	onEvent: (e: PlanEvent) => void,
	op: 'plan' | 'validate' | 'lint' = 'plan',
	env = ''
): () => void {
	const q = env ? `?env=${encodeURIComponent(env)}` : '';
	const es = new EventSource(`/api/projects/${projectId}/${op}${q}`);

	es.addEventListener('log', (ev) => {
		const d = JSON.parse((ev as MessageEvent).data);
		onEvent({ kind: 'log', line: d.line });
	});
	es.addEventListener('result', (ev) => {
		const d = JSON.parse((ev as MessageEvent).data);
		onEvent({
			kind: 'result',
			hasChanges: d.hasChanges,
			changes: d.changes ?? null,
			add: d.add ?? 0,
			change: d.change ?? 0,
			destroy: d.destroy ?? 0,
			replace: d.replace ?? 0,
			cost: d.cost,
			valid: d.valid,
			errorCount: d.errorCount ?? 0,
			warningCount: d.warningCount ?? 0,
			available: d.available,
			lint: d.lint
		});
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

// StreamableOp covers every state-mutating streamed operation.
export type StreamableOp = 'apply' | 'destroy' | 'import' | 'state-rm' | 'state-mv' | 'init-backend';

const OP_PATH: Record<StreamableOp, string> = {
	apply: 'apply',
	destroy: 'destroy',
	'import': 'state/import',
	'state-rm': 'state/rm',
	'state-mv': 'state/mv',
	'init-backend': 'state/init-backend'
};

// streamOp runs a mutating operation (apply/destroy/state surgery) and streams
// its SSE response. These mutate real infrastructure or state, so they are
// POSTs consumed via fetch streaming — EventSource can only GET, and a
// state-changing GET could be triggered by a bare link. Extra parameters
// (import address/id, rm address, mv from/to) travel as query values. Returns
// a function that aborts the operation.
export function streamOp(
	projectId: string,
	op: StreamableOp,
	onEvent: (e: PlanEvent) => void,
	params?: Record<string, string>,
	env = ''
): () => void {
	const ctrl = new AbortController();
	(async () => {
		const merged = { ...(params ?? {}), ...(env ? { env } : {}) };
		const q = Object.keys(merged).length ? `?${new URLSearchParams(merged)}` : '';
		const res = await fetch(`/api/projects/${projectId}/${OP_PATH[op]}${q}`, {
			method: 'POST',
			signal: ctrl.signal
		});
		if (!res.ok || !res.body) {
			on401(res.status);
			let msg = `${op} failed (${res.status})`;
			try {
				const body = await res.json();
				if (body?.error) msg = body.error;
			} catch {
				/* ignore */
			}
			onEvent({ kind: 'error', error: msg });
			return;
		}
		// Minimal SSE parse: events are "event: <name>\ndata: <json>\n\n" blocks.
		const reader = res.body.getReader();
		const dec = new TextDecoder();
		let buf = '';
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			buf += dec.decode(value, { stream: true });
			let sep;
			while ((sep = buf.indexOf('\n\n')) >= 0) {
				const block = buf.slice(0, sep);
				buf = buf.slice(sep + 2);
				let name = '';
				let data = '';
				for (const line of block.split('\n')) {
					if (line.startsWith('event: ')) name = line.slice(7);
					else if (line.startsWith('data: ')) data = line.slice(6);
				}
				if (!name) continue;
				try {
					const d = data ? JSON.parse(data) : {};
					if (name === 'log') onEvent({ kind: 'log', line: d.line });
					else if (name === 'result') onEvent({ kind: 'result', outputs: d.outputs ?? [] });
					else if (name === 'error') onEvent({ kind: 'error', error: d.error ?? `${op} failed` });
					else if (name === 'done') onEvent({ kind: 'done' });
				} catch {
					/* skip malformed event */
				}
			}
		}
	})().catch((err) => {
		if (!ctrl.signal.aborted) onEvent({ kind: 'error', error: (err as Error).message });
	});
	return () => ctrl.abort();
}
