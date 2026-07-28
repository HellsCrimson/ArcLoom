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

// StateImportBlockedError signals that a state import was refused because the
// target workspace already tracks resources (replacing it would orphan them).
export class StateImportBlockedError extends Error {
	tracked: number;
	constructor(message: string, tracked: number) {
		super(message);
		this.tracked = tracked;
	}
}

// CommitPushedError signals that undoing the last commit was refused because
// origin already has it — rewriting it would diverge from the remote.
export class CommitPushedError extends Error {}

/** A local module definition still has calls using it (409 from the delete). */
export class ModuleInUseError extends Error {}

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
		// version: omit to leave the pin alone, '' to unpin, 'x' to pin.
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

	// Approves the plan the server stashed for this environment. It takes no
	// summary: the counts on the record come from that plan, so an approver who
	// never ran it (the reason approve is its own permission) records the truth.
	approve: (id: string, env: string) =>
		fetch(`/api/projects/${id}/approve${env ? `?env=${encodeURIComponent(env)}` : ''}`, {
			method: 'POST'
		}).then((r) => json<Approval>(r)),

	setEnvironments: (id: string, environments: Environment[]) =>
		fetch(`/api/projects/${id}/environments`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ environments })
		}).then((r) => json<Environment[]>(r)),

	// setTerraform saves the terraform block's own settings (required_version).
	// Generated-HCL only — unlike the backend it needs no re-init.
	setTerraform: (id: string, requiredVersion: string) =>
		fetch(`/api/projects/${id}/terraform`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ requiredVersion })
		}).then((r) => json<Project>(r)),

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

	getPolicy: (id: string) =>
		fetch(`/api/projects/${id}/policy`).then((r) => json<{ config: PolicyConfig }>(r)),

	setPolicy: (id: string, config: PolicyConfig) =>
		fetch(`/api/projects/${id}/policy`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config)
		}).then((r) => json<{ config: PolicyConfig }>(r)),

	instanceInfo: () => fetch('/api/admin/instance').then((r) => json<InstanceInfo>(r)),

	getNotify: (id: string) =>
		fetch(`/api/projects/${id}/notify`).then((r) => json<{ config: NotifyConfig }>(r)),

	setNotify: (id: string, config: NotifyUpdate) =>
		fetch(`/api/projects/${id}/notify`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(config)
		}).then((r) => json<{ config: NotifyConfig }>(r)),

	// testNotify delivers a test event through the stored webhook; the error
	// message carries the delivery failure (502) when the webhook rejects it.
	testNotify: (id: string) =>
		fetch(`/api/projects/${id}/notify/test`, { method: 'POST' }).then((r) => json<{ ok: boolean }>(r)),

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

	// A module DEFINITION (the modules/<name>/ folder). `call: false` defines it
	// without using it; the default also adds a first call, so define-and-use
	// stays one step.
	createLocalModule: (projectId: string, name: string, call = true) =>
		fetch(`/api/projects/${projectId}/modules/local`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ name, call })
		}).then((r) => json<Project>(r)),

	// 409s while a call still uses the definition; force also drops the calls.
	deleteLocalModule: async (projectId: string, name: string, force = false) => {
		const r = await fetch(
			`/api/projects/${projectId}/modules/local/${encodeURIComponent(name)}${force ? '?force=true' : ''}`,
			{ method: 'DELETE' }
		);
		if (r.status === 409) {
			const b = await r.json().catch(() => ({}) as { error?: string });
			throw new ModuleInUseError(b.error ?? 'module is still used by a module call');
		}
		return json<Project>(r);
	},

	// A module CALL — one endpoint for both kinds of source (local
	// "./modules/x" and registry). Replaces the whole call, so config/count/
	// forEach must always be resent; `rename` moves an existing call.
	setModuleCall: (
		projectId: string,
		body: {
			name: string;
			rename?: string;
			source: string;
			version?: string;
			config: Record<string, AttrValue>;
			count?: string;
			forEach?: string;
		}
	) =>
		fetch(`/api/projects/${projectId}/modules/call`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	deleteModuleCall: (projectId: string, name: string) =>
		fetch(`/api/projects/${projectId}/modules/call/${encodeURIComponent(name)}`, {
			method: 'DELETE'
		}).then((r) => json<Project>(r)),

	// Companion files (templates/scripts reached via file()/templatefile()).
	// Upsert keys on `path`; pass oldPath to rename in a single update.
	setFile: (projectId: string, body: { path: string; oldPath?: string; content: string }) =>
		fetch(`/api/projects/${projectId}/files`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		}).then((r) => json<Project>(r)),

	deleteFile: (projectId: string, path: string) =>
		fetch(`/api/projects/${projectId}/files?path=${encodeURIComponent(path)}`, {
			method: 'DELETE'
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

	// importState rebuilds the config from a state file AND adopts the state into
	// the environment's workspace. The server refuses (409) when that workspace
	// already tracks resources — replacing it would orphan them — unless force.
	importState: async (id: string, stateJSON: string, env = '', force = false) => {
		const q = new URLSearchParams();
		if (env) q.set('env', env);
		if (force) q.set('force', 'true');
		const qs = q.toString();
		const res = await fetch(`/api/projects/${id}/import/state${qs ? `?${qs}` : ''}`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ state: stateJSON })
		});
		if (res.status === 409) {
			const body = await res.json().catch(() => ({}) as { error?: string; state?: number });
			throw new StateImportBlockedError(body.error ?? 'this project already has state', body.state ?? 0);
		}
		return json<ImportResult>(res);
	},

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
		getSecretStore: () =>
			fetch('/api/admin/settings/secret-store', { credentials: 'same-origin' }).then((r) =>
				json<SecretStoreSettings>(r)
			),
		setSecretStore: (body: SecretStoreUpdate) =>
			fetch('/api/admin/settings/secret-store', {
				method: 'PUT',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((r) => json<SecretStoreSettings>(r)),
		// Probes a candidate configuration without saving it.
		testSecretStore: (body: SecretStoreUpdate) =>
			fetch('/api/admin/settings/secret-store/test', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(body)
			}).then((r) => json<{ status: string; target: string }>(r)),
		// What the master key seals and what it can still open (counts + field
		// names only; never a value).
		getSecretKey: () =>
			fetch('/api/admin/settings/secret-key', { credentials: 'same-origin' }).then((r) =>
				json<SecretKeyStatus>(r)
			),
		// Re-keys both encrypted stores. The current key is required so a rotation
		// is never started on a guess.
		rotateSecretKey: (currentKey: string, newKey: string) =>
			fetch('/api/admin/settings/secret-key/rotate', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ currentKey, newKey })
			}).then((r) => json<RotateKeyResult>(r)),
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
		// Drops the newest commit, keeping its files as uncommitted changes.
		// Throws CommitPushedError (409) when origin already has the commit,
		// unless force is passed — the caller escalates its confirmation.
		undoCommit: async (id: string, force = false): Promise<GitCommit> => {
			const r = await fetch(`/api/projects/${id}/git/undo-commit`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ force })
			});
			if (r.status === 409) {
				const b = await r.json().catch(() => ({}) as { error?: string });
				throw new CommitPushedError(b.error ?? 'the last commit is already pushed');
			}
			return json<GitCommit>(r);
		},
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

// GroupRole maps an IdP group to ArcLoom access for provisioned SSO users.
// `admin` implies every permission, so `perms` is ignored alongside it.
export interface GroupRole {
	group: string;
	admin?: boolean;
	perms?: string[];
}

export interface SSOSettings {
	enabled: boolean;
	issuer: string;
	clientId: string;
	redirectUrl: string;
	scopes: string;
	groupsClaim: string;
	groupRoles: GroupRole[];
	syncGroupsOnLogin: boolean;
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
	groupsClaim?: string;
	groupRoles?: GroupRole[];
	syncGroupsOnLogin?: boolean;
}

// SecretStoreSettings is the instance-wide secret-backend selection. The token is
// never returned — only whether one is on file, and from where.
export interface SecretStoreSettings {
	kind: 'local' | 'vault';
	address: string;
	mount: string;
	prefix: string;
	namespace: string;
	insecureSkipVerify: boolean;
	tokenSet: boolean;
	tokenFromEnv: boolean;
	/** The backend actually serving secrets right now ("local" | "vault" | ""). */
	active: string;
	/** Whether ARCLOOM_SECRET_KEY is set, i.e. whether "local" is usable. */
	localEnabled: boolean;
	/** Whether an external store may be enabled (enterprise feature). */
	licensed: boolean;
	/** Secrets copied by the last save, when migration was requested. */
	migrated?: number;
}

export interface SecretStoreUpdate {
	kind: 'local' | 'vault';
	address?: string;
	mount?: string;
	prefix?: string;
	namespace?: string;
	token?: string;
	insecureSkipVerify?: boolean;
	migrate?: boolean;
}

/** One at-rest encrypted settings field and whether the current key opens it. */
export interface EncryptedField {
	field: string;
	readable: boolean;
}

/** What the master key (ARCLOOM_SECRET_KEY) seals, and what it currently opens. */
export interface SecretKeyStatus {
	keySet: boolean;
	secretFiles: number;
	secretValues: number;
	/** Project ids whose encrypted secret file no available key opens. */
	unreadableSecrets?: string[];
	settingsFields: EncryptedField[];
	unreadableFields: number;
	/** External secret backend, when active — its VALUES are not sealed with this key. */
	externalStore?: string;
	/** True while a plan/apply/drift run is in flight; rotation refuses to start. */
	busy: boolean;
}

/** One store's half of a rotation (secret files / settings fields). */
export interface RotateReport {
	items: number;
	rekeyed: number;
	verified: number;
	values: number;
	unreadable?: string[];
	complete: boolean;
}

export interface RotateKeyResult {
	secrets: RotateReport;
	settings: RotateReport;
	complete: boolean;
	warnings?: string[];
	status: SecretKeyStatus;
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
	/** State resources recognized as ones the model already had (matched, not duplicated). */
	matchedResources?: number;
	/** How many state resources were installed as the project's own state. */
	adoptedState?: number;
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

// AttrDiff is one attribute a planned change touches. `path` is relative to the
// resource ("tags.Name", "network_interface[0].subnet_id"); `before`/`after` are
// pre-rendered by the server (strings quoted, sensitive values redacted).
export interface AttrDiff {
	path: string;
	action: 'add' | 'update' | 'remove';
	before?: string;
	after?: string;
	/** The value is only known after apply. */
	unknown?: boolean;
	/** Redacted — the provider marked the attribute sensitive. */
	sensitive?: boolean;
	/** This attribute is what forces the resource to be replaced. */
	forcesReplace?: boolean;
}

// PlanChange is one resource's pending action from the structured plan.
export interface PlanChange {
	address: string;
	moduleAddress?: string;
	mode: 'managed' | 'data';
	type: string;
	name: string;
	action: 'create' | 'update' | 'replace' | 'delete' | 'read';
	/** Attributes this change touches; server-side populated for update/replace. */
	diffs?: AttrDiff[];
	/** The attribute list hit the size cap — the run log holds the rest. */
	diffsTruncated?: boolean;
}

export interface PlanSummary {
	hasChanges: boolean;
	changes: PlanChange[] | null;
	add: number;
	change: number;
	destroy: number;
	replace: number;
	cost?: CostSummary;
	policy?: PolicySummary;
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

// PolicyViolation is one rule a policy check reported against the plan.
export interface PolicyViolation {
	severity: 'failure' | 'warning';
	message: string;
	namespace?: string;
	rule?: string;
}

// PolicySummary is a conftest evaluation of a plan. available=false means no
// conftest binary on the server; error is set when the check could not run
// (broken Rego) — an enforcing gate treats both as a blocked apply.
export interface PolicySummary {
	available: boolean;
	checked: boolean;
	violations: PolicyViolation[];
	failureCount: number;
	warningCount: number;
	successes: number;
	error?: string;
}

// PolicyDoc is one Rego document evaluated against the plan JSON.
export interface PolicyDoc {
	name: string;
	rego: string;
}

// PolicyConfig is a project's policy-as-code config: enabled annotates plans,
// enforce blocks applies that fail, failOnWarn promotes warnings to blocking.
export interface PolicyConfig {
	enabled: boolean;
	enforce: boolean;
	failOnWarn: boolean;
	docs: PolicyDoc[];
}

// InstanceInfo describes the running server: what it was built from, which
// engines and optional tools it resolved, where it stores things, and which
// switches are on. Admin-only; it never carries secrets.
export interface InstanceInfo {
	version: string;
	commit?: string;
	buildDate?: string;
	go: string;
	platform: string;
	startedAt: string;
	uptimeSeconds: number;
	goroutines: number;
	heapMB: number;
	defaultEngine: string;
	engines: { name: string; path: string; version?: string; error?: string; default: boolean }[];
	tools: { name: string; feature: string; available: boolean; path?: string; version?: string }[];
	schemaFormat: number;
	paths: Record<string, string>;
	authEnabled: boolean;
	cookieSecure: boolean;
	authProviders: string[];
	ssoEnabled: boolean;
	secretStore: boolean;
	logLevel: string;
	logFormat: string;
	licenseTier: string;
	licenseFeatures?: string[];
	projects: number;
	users: number;
}

// NotifyKind is the destination a project's events are rendered for.
export type NotifyKind = 'webhook' | 'slack' | 'discord' | 'fluxer' | 'teams' | 'matrix' | 'ntfy';

// NotifyConfig is a project's outbound notification config as the server
// reports it: the webhook URL is never returned (it is a credential), only
// whether one is stored (urlSet) plus a masked hint. storable=false means the
// server has no ARCLOOM_SECRET_KEY to encrypt a URL with.
export interface NotifyConfig {
	enabled: boolean;
	kind: NotifyKind;
	urlSet: boolean;
	urlHint?: string;
	onApplyFailure: boolean;
	onApplySuccess: boolean;
	onDrift: boolean;
	storable: boolean;
}

// NotifyUpdate is the editable shape; an empty url keeps the stored one.
export interface NotifyUpdate {
	enabled: boolean;
	kind: NotifyKind;
	url: string;
	onApplyFailure: boolean;
	onApplySuccess: boolean;
	onDrift: boolean;
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

// PendingPlan is a plan waiting to be approved: the artifact the last plan for
// this environment produced, kept outside the CLI workspace (which the next
// operation wipes) so it stays approvable, plus what it would do.
export interface PendingPlan {
	projectId: string;
	env?: string;
	runId?: string;
	add: number;
	change: number;
	destroy: number;
	replace: number;
	plannedBy?: string;
	plannedAt: string;
}

// ApprovalState is the gate status for a project + environment.
export interface ApprovalState {
	required: boolean;
	approval: Approval | null;
	planPending: PendingPlan | null;
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
	/** Instance addresses the engine considers damaged — exactly what untaint takes. */
	tainted?: string[];
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
	op:
		| 'plan'
		| 'apply'
		| 'destroy'
		| 'import'
		| 'state-rm'
		| 'state-mv'
		| 'init-backend'
		| 'accept-drift'
		| 'taint'
		| 'untaint'
		| 'force-unlock'
		| 'validate'
		| 'lint'
		| 'drift';
	env?: string;
	user: string;
	startedAt: string;
	finishedAt: string;
	status: 'success' | 'error';
	error?: string;
	plan?: PlanSummary;
	lint?: LintSummary;
	policy?: PolicySummary;
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
			lint: d.lint,
			policy: d.policy
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
export type StreamableOp =
	| 'apply'
	| 'destroy'
	| 'import'
	| 'state-rm'
	| 'state-mv'
	| 'init-backend'
	| 'accept-drift'
	| 'taint'
	| 'untaint'
	| 'force-unlock';

const OP_PATH: Record<StreamableOp, string> = {
	apply: 'apply',
	destroy: 'destroy',
	'import': 'state/import',
	'state-rm': 'state/rm',
	'state-mv': 'state/mv',
	'init-backend': 'state/init-backend',
	'accept-drift': 'state/accept-drift',
	taint: 'state/taint',
	untaint: 'state/untaint',
	'force-unlock': 'state/force-unlock'
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
