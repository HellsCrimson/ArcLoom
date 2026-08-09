<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import {
		api,
		type AdminUser,
		type GroupRole,
		type SSOSettings,
		type SecretStoreSettings,
		type SecretStoreUpdate,
		type InstanceInfo,
		type SecretKeyStatus,
		type RotateKeyResult
	} from '$lib/api/client';
	import type { AuditEntry } from '$lib/types';
	import { auth } from '$lib/stores/auth.svelte';
	import { Loader2, Trash2, KeyRound, UserPlus, ShieldCheck, ScrollText, Server, Check, X, Users, Plus, TriangleAlert, RefreshCw } from 'lucide-svelte';

	// --- SSO group mapping helpers ---
	// These take the null check off the markup: `sso` is only non-null inside the
	// tab's {#if}, which an inline arrow callback does not inherit.
	// toggleGroupPerm avoids bind:checked, which writes on mount and would dirty a
	// row the admin never touched (the same trap as the schema-form checkboxes).
	function toggleGroupPerm(gr: GroupRole, perm: string, on: boolean) {
		const current = gr.perms ?? [];
		gr.perms = on ? [...current, perm] : current.filter((p) => p !== perm);
	}
	function addGroupRole() {
		if (sso) sso.groupRoles = [...sso.groupRoles, { group: '', admin: false, perms: [] }];
	}
	function removeGroupRole(i: number) {
		if (sso) sso.groupRoles = sso.groupRoles.filter((_, j) => j !== i);
	}
	function setSyncGroups(on: boolean) {
		if (sso) sso.syncGroupsOnLogin = on;
	}

	let { open = $bindable(false) }: { open: boolean } = $props();

	type Tab = 'users' | 'sso' | 'secrets' | 'audit' | 'instance' | 'danger';
	let tab = $state<Tab>('users');
	let error = $state('');
	let notice = $state('');

	const PERMS: { key: string; label: string }[] = [
		{ key: 'resources', label: 'Edit resources' },
		{ key: 'deploy', label: 'Deploy (plan/apply)' },
		{ key: 'approve', label: 'Approve plans' },
		{ key: 'git', label: 'Git' }
	];

	// --- Users ---
	let users = $state<AdminUser[]>([]);
	let loadingUsers = $state(false);

	// New-user form.
	let nuName = $state('');
	let nuPass = $state('');
	let nuAdmin = $state(false);
	let nuPerms = $state<Record<string, boolean>>({});
	let creating = $state(false);

	async function loadUsers() {
		loadingUsers = true;
		error = '';
		try {
			users = await api.admin.listUsers();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loadingUsers = false;
		}
	}

	async function createUser() {
		error = '';
		notice = '';
		creating = true;
		try {
			await api.admin.createUser({
				username: nuName,
				password: nuPass,
				admin: nuAdmin,
				permissions: Object.keys(nuPerms).filter((k) => nuPerms[k])
			});
			nuName = '';
			nuPass = '';
			nuAdmin = false;
			nuPerms = {};
			notice = 'User created.';
			await loadUsers();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			creating = false;
		}
	}

	async function patchUser(u: AdminUser, body: Partial<AdminUser>) {
		error = '';
		try {
			await api.admin.updateUser(u.id, body);
			await loadUsers();
		} catch (e) {
			error = (e as Error).message;
			await loadUsers(); // revert optimistic UI
		}
	}

	function togglePerm(u: AdminUser, perm: string, on: boolean) {
		const perms = new Set(u.permissions);
		if (on) perms.add(perm);
		else perms.delete(perm);
		patchUser(u, { permissions: [...perms] });
	}

	async function resetPassword(u: AdminUser) {
		const pw = prompt(`New password for ${u.username} (min 8 chars):`);
		if (!pw) return;
		error = '';
		notice = '';
		try {
			await api.admin.resetPassword(u.id, pw);
			notice = `Password reset for ${u.username}; their sessions were revoked.`;
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function deleteUser(u: AdminUser) {
		if (!confirm(`Delete user "${u.username}"? This cannot be undone.`)) return;
		error = '';
		try {
			await api.admin.deleteUser(u.id);
			await loadUsers();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	// --- SSO ---
	let sso = $state<SSOSettings | null>(null);
	let ssoSecret = $state('');
	let savingSSO = $state(false);

	async function loadSSO() {
		error = '';
		try {
			sso = await api.admin.getSSO();
			ssoSecret = '';
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function saveSSO() {
		if (!sso) return;
		error = '';
		notice = '';
		savingSSO = true;
		try {
			sso = await api.admin.setSSO({
				enabled: sso.enabled,
				issuer: sso.issuer,
				clientId: sso.clientId,
				clientSecret: ssoSecret,
				redirectUrl: sso.redirectUrl,
				scopes: sso.scopes,
				groupsClaim: sso.groupsClaim,
				groupRoles: sso.groupRoles,
				syncGroupsOnLogin: sso.syncGroupsOnLogin
			});
			ssoSecret = '';
			notice = 'SSO settings saved and reloaded.';
			// Refresh which providers the login screen offers.
			await auth.refresh();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			savingSSO = false;
		}
	}

	// --- Secret store ---
	// Where project secrets live: the local encrypted files, or an external store.
	// The token is write-only (the server never returns it), so an empty field on
	// save means "keep the stored one".
	let secretStore = $state<SecretStoreSettings | null>(null);
	let vaultToken = $state('');
	let secretBusy = $state('');
	let migrateSecrets = $state(true);

	async function loadSecretStore() {
		error = '';
		try {
			secretStore = await api.admin.getSecretStore();
			vaultToken = '';
		} catch (e) {
			error = (e as Error).message;
		}
	}

	// The payload both Save and Test send.
	function secretStorePayload(): SecretStoreUpdate {
		const st = secretStore!;
		return {
			kind: st.kind,
			address: st.address,
			mount: st.mount,
			prefix: st.prefix,
			namespace: st.namespace,
			token: vaultToken,
			insecureSkipVerify: st.insecureSkipVerify
		};
	}

	async function testSecretStore() {
		if (!secretStore) return;
		secretBusy = 'test';
		error = '';
		notice = '';
		try {
			const res = await api.admin.testSecretStore(secretStorePayload());
			notice = `Reached ${res.target}`;
		} catch (e) {
			error = (e as Error).message;
		} finally {
			secretBusy = '';
		}
	}

	async function saveSecretStore() {
		if (!secretStore) return;
		secretBusy = 'save';
		error = '';
		notice = '';
		try {
			const toVault = secretStore.kind === 'vault';
			secretStore = await api.admin.setSecretStore({
				...secretStorePayload(),
				migrate: toVault && migrateSecrets
			});
			vaultToken = '';
			notice =
				secretStore.migrated && secretStore.migrated > 0
					? `Saved — ${secretStore.migrated} secret(s) copied into the new store`
					: 'Saved';
		} catch (e) {
			error = (e as Error).message;
		} finally {
			secretBusy = '';
		}
	}

	// --- Audit log ---
	let auditEntries = $state<AuditEntry[]>([]);

	// An agent write and a browser click are the same action by the same user;
	// the origin is the only thing that tells them apart, so it gets a colour.
	function viaClass(via?: string) {
		if (via === 'mcp') return 'bg-purple-500/15 text-purple-300';
		if (via === 'api') return 'bg-sky-500/15 text-sky-300';
		if (via === 'ui') return 'bg-[var(--al-panel-2)] text-[var(--al-muted)]';
		return 'bg-[var(--al-panel-2)] text-[var(--al-muted)]';
	}
	let loadingAudit = $state(false);
	let auditUserFilter = $state('');

	async function loadAudit() {
		loadingAudit = true;
		error = '';
		try {
			auditEntries = await api.admin.listAudit({ user: auditUserFilter.trim() || undefined, limit: 300 });
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loadingAudit = false;
		}
	}

	// --- Danger zone: master-key rotation ---
	// Re-seals both encrypted stores (the per-project secret files and the
	// settings document's encrypted fields) under a new ARCLOOM_SECRET_KEY. The
	// current key is required by the server, so this is never started on a guess.
	let secretKey = $state<SecretKeyStatus | null>(null);
	let rkCurrent = $state('');
	let rkNew = $state('');
	let rkRepeat = $state('');
	let rkConfirm = $state('');
	let rotating = $state(false);
	let rotateResult = $state<RotateKeyResult | null>(null);

	const ROTATE_WORD = 'ROTATE';
	// Anything that would make the rotation pointless or unsafe to start.
	let rotateBlocked = $derived(
		!secretKey?.keySet ||
			secretKey.busy ||
			rkNew.length < 12 ||
			rkNew !== rkRepeat ||
			rkNew === rkCurrent ||
			rkCurrent === '' ||
			rkConfirm !== ROTATE_WORD
	);

	async function loadSecretKey() {
		error = '';
		try {
			secretKey = await api.admin.getSecretKey();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function rotateSecretKey() {
		rotating = true;
		error = '';
		notice = '';
		rotateResult = null;
		try {
			rotateResult = await api.admin.rotateSecretKey(rkCurrent, rkNew);
			secretKey = rotateResult.status;
			// Keep the new key in the "current" field: a follow-up rotation (or a
			// re-run of an incomplete one) needs it, and retyping invites a typo.
			rkCurrent = rkNew;
			rkNew = '';
			rkRepeat = '';
			rkConfirm = '';
		} catch (e) {
			// Refresh first, then report: loadSecretKey clears `error` on entry, so
			// setting it before the reload would wipe the very message that explains
			// the refusal.
			await loadSecretKey();
			error = (e as Error).message;
		} finally {
			rotating = false;
		}
	}

	// --- Instance ---
	let instance = $state<InstanceInfo | null>(null);
	let loadingInstance = $state(false);

	async function loadInstance() {
		loadingInstance = true;
		error = '';
		try {
			instance = await api.instanceInfo();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loadingInstance = false;
		}
	}

	function uptime(seconds: number): string {
		const d = Math.floor(seconds / 86400);
		const h = Math.floor((seconds % 86400) / 3600);
		const m = Math.floor((seconds % 3600) / 60);
		if (d > 0) return `${d}d ${h}h`;
		if (h > 0) return `${h}h ${m}m`;
		return `${m}m ${Math.floor(seconds % 60)}s`;
	}

	// Load per-tab when the modal opens or the tab changes.
	$effect(() => {
		if (!open) return;
		if (tab === 'users' && users.length === 0) loadUsers();
		if (tab === 'sso' && sso === null) loadSSO();
		if (tab === 'secrets' && secretStore === null) loadSecretStore();
		if (tab === 'audit' && auditEntries.length === 0) loadAudit();
		if (tab === 'instance' && instance === null) loadInstance();
		if (tab === 'danger' && secretKey === null) loadSecretKey();
	});

	const field =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] px-3 py-2 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const tabBtn = (active: boolean) =>
		`rounded-md px-3 py-1.5 text-sm font-medium ${active ? 'bg-[var(--al-accent-weak)] text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}`;
</script>

<Modal title="Settings" bind:open wide>
	<div class="mb-3 flex items-center gap-1.5">
		<button class={tabBtn(tab === 'users')} onclick={() => (tab = 'users')}>Users</button>
		<button class={tabBtn(tab === 'sso')} onclick={() => (tab = 'sso')}>Single sign-on</button>
		<button class={tabBtn(tab === 'secrets')} onclick={() => (tab = 'secrets')}>Secret store</button>
		<button class={tabBtn(tab === 'audit')} onclick={() => (tab = 'audit')}>Audit log</button>
		<button class={tabBtn(tab === 'instance')} onclick={() => (tab = 'instance')}>Instance</button>
		<button
			class="ml-auto rounded-md px-3 py-1.5 text-sm font-medium {tab === 'danger'
				? 'bg-[var(--al-danger)]/15 text-[var(--al-danger)]'
				: 'text-[var(--al-muted)] hover:bg-[var(--al-panel-2)]'}"
			onclick={() => (tab = 'danger')}
		>Danger zone</button>
	</div>

	{#if error}<p class="mb-2 text-sm text-[var(--al-danger)]">{error}</p>{/if}
	{#if notice}<p class="mb-2 text-sm text-[var(--al-accent)]">{notice}</p>{/if}

	{#if tab === 'users'}
		<!-- Add user -->
		<div class="mb-4 rounded-lg border border-[var(--al-border)] bg-[var(--al-panel-2)] p-3">
			<div class="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				<UserPlus size={13} /> New user
			</div>
			<div class="grid grid-cols-2 gap-2">
				<input class={field} placeholder="username" bind:value={nuName} autocapitalize="off" spellcheck="false" />
				<input class={field} type="password" placeholder="password (min 8)" bind:value={nuPass} autocomplete="new-password" />
			</div>
			<div class="mt-2 flex flex-wrap items-center gap-3 text-xs text-[var(--al-text)]">
				<label class="flex items-center gap-1.5"><input type="checkbox" bind:checked={nuAdmin} /> Admin</label>
				{#each PERMS as p (p.key)}
					<label class="flex items-center gap-1.5 {nuAdmin ? 'opacity-40' : ''}">
						<input type="checkbox" checked={nuPerms[p.key] ?? false} disabled={nuAdmin} onchange={(e) => (nuPerms = { ...nuPerms, [p.key]: e.currentTarget.checked })} />
						{p.label}
					</label>
				{/each}
				<button
					class="ml-auto inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40"
					disabled={creating || !nuName || nuPass.length < 8}
					onclick={createUser}
				>
					{#if creating}<Loader2 size={14} class="animate-spin" />{/if} Create
				</button>
			</div>
		</div>

		{#if loadingUsers}
			<div class="flex justify-center py-6 text-[var(--al-muted)]"><Loader2 size={20} class="animate-spin" /></div>
		{:else}
			<div class="overflow-x-auto">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b border-[var(--al-border)] text-left text-xs uppercase tracking-wide text-[var(--al-muted)]">
							<th class="py-1.5 pr-2">User</th>
							<th class="px-2">Admin</th>
							{#each PERMS as p (p.key)}<th class="px-2 text-center">{p.label}</th>{/each}
							<th class="px-2">Enabled</th>
							<th class="px-2"></th>
						</tr>
					</thead>
					<tbody>
						{#each users as u (u.id)}
							<tr class="border-b border-[var(--al-border)]/50">
								<td class="py-2 pr-2">
									<div class="font-medium text-[var(--al-text)]">{u.username}</div>
									<div class="text-[10px] text-[var(--al-muted)]">{u.provider}{u.email ? ` · ${u.email}` : ''}</div>
								</td>
								<td class="px-2">
									<input type="checkbox" checked={u.admin} onchange={(e) => patchUser(u, { admin: e.currentTarget.checked })} />
								</td>
								{#each PERMS as p (p.key)}
									<td class="px-2 text-center">
										<input
											type="checkbox"
											checked={u.admin || u.permissions.includes(p.key)}
											disabled={u.admin}
											onchange={(e) => togglePerm(u, p.key, e.currentTarget.checked)}
										/>
									</td>
								{/each}
								<td class="px-2">
									<input type="checkbox" checked={!u.disabled} onchange={(e) => patchUser(u, { disabled: !e.currentTarget.checked })} />
								</td>
								<td class="px-2">
									<div class="flex items-center gap-1">
										{#if u.provider === 'local'}
											<button class="rounded p-1 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)] hover:text-[var(--al-text)]" title="Reset password" aria-label="Reset password" onclick={() => resetPassword(u)}><KeyRound size={14} /></button>
										{/if}
										<button class="rounded p-1 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)] hover:text-[var(--al-danger)]" title="Delete user" aria-label="Delete user" onclick={() => deleteUser(u)}><Trash2 size={14} /></button>
									</div>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	{:else if tab === 'sso'}
		{#if !sso}
			<div class="flex justify-center py-6 text-[var(--al-muted)]"><Loader2 size={20} class="animate-spin" /></div>
		{:else}
			<div class="space-y-3">
				<div class="flex items-center gap-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-sm">
					<ShieldCheck size={15} class={sso.active ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)]'} />
					<span class="text-[var(--al-text)]">OpenID Connect</span>
					<span class="ml-auto text-xs {sso.active ? 'text-[var(--al-accent)]' : 'text-[var(--al-muted)]'}">{sso.active ? 'Active' : 'Inactive'}</span>
				</div>

				<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
					<input type="checkbox" bind:checked={sso.enabled} /> Enable single sign-on
				</label>

				<div class="space-y-1">
					<span class="block text-xs font-medium text-[var(--al-muted)]">Issuer URL</span>
					<input class={field} bind:value={sso.issuer} placeholder="https://accounts.google.com" />
				</div>
				<div class="grid grid-cols-2 gap-2">
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">Client ID</span>
						<input class={field} bind:value={sso.clientId} />
					</div>
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">Client secret</span>
						<input class={field} type="password" bind:value={ssoSecret} placeholder={sso.hasSecret ? '•••••••• (unchanged)' : ''} />
					</div>
				</div>
				<div class="space-y-1">
					<span class="block text-xs font-medium text-[var(--al-muted)]">Redirect URL</span>
					<input class={field} bind:value={sso.redirectUrl} placeholder="https://arcloom.example.com/api/auth/sso/oidc/callback" />
					<p class="text-[10px] text-[var(--al-muted)]">Register this exact URL with your identity provider.</p>
				</div>
				<div class="space-y-1">
					<span class="block text-xs font-medium text-[var(--al-muted)]">Extra scopes</span>
					<input class={field} bind:value={sso.scopes} placeholder="email profile" />
					<p class="text-[10px] text-[var(--al-muted)]">
						<span class="mono">groups</span> is requested automatically once a group mapping exists below.
					</p>
				</div>

				<!-- Group → role mapping: what an IdP group grants a provisioned user.
				     Without a mapping SSO users are read-only, which is the default. -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-2 flex items-center gap-2">
						<Users size={13} class="text-[var(--al-muted)]" />
						<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">Group mapping</span>
						<button
							class="ml-auto inline-flex items-center gap-1 rounded border border-[var(--al-border)] px-1.5 py-0.5 text-[11px] text-[var(--al-muted)] hover:text-[var(--al-accent)]"
							onclick={addGroupRole}
						><Plus size={11} /> Add group</button>
					</div>

					<div class="mb-2 grid grid-cols-[1fr_auto] items-center gap-2">
						<div class="space-y-1">
							<span class="block text-[11px] text-[var(--al-muted)]">Groups claim</span>
							<input class={field} bind:value={sso.groupsClaim} placeholder="groups" />
						</div>
					</div>
					<p class="mb-2 text-[10px] text-[var(--al-muted)]">
						Claim carrying group membership — <span class="mono">groups</span> when blank
						(Entra ID usually needs <span class="mono">roles</span>). Keycloak group paths like
						<span class="mono">/admins</span> match the bare name.
					</p>

					{#if sso.groupRoles.length === 0}
						<p class="text-[11px] text-[var(--al-muted)]">
							No mapping — users provisioned by SSO are read-only until an admin grants access in
							the Users tab.
						</p>
					{:else}
						<div class="space-y-1.5">
							{#each sso.groupRoles as gr, i (i)}
								<div class="flex flex-wrap items-center gap-2 rounded border border-[var(--al-border)] bg-[var(--al-panel-2)] p-1.5">
									<input
										class="mono min-w-0 flex-1 rounded border border-[var(--al-border)] bg-[var(--al-bg)] px-2 py-1 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]"
										bind:value={gr.group}
										placeholder="platform-admins"
									/>
									<label class="flex flex-none items-center gap-1 text-[11px] text-[var(--al-text)]">
										<input type="checkbox" checked={gr.admin ?? false} onchange={(e) => (gr.admin = e.currentTarget.checked)} />
										admin
									</label>
									{#each PERMS as p (p.key)}
										<label class="flex flex-none items-center gap-1 text-[11px] {gr.admin ? 'opacity-40' : 'text-[var(--al-muted)]'}" title={p.label}>
											<input
												type="checkbox"
												disabled={gr.admin}
												checked={(gr.perms ?? []).includes(p.key)}
												onchange={(e) => toggleGroupPerm(gr, p.key, e.currentTarget.checked)}
											/>
											{p.key}
										</label>
									{/each}
									<button
										class="flex-none text-[var(--al-muted)] hover:text-[var(--al-danger)]"
										aria-label="Remove group"
										onclick={() => removeGroupRole(i)}
									><Trash2 size={12} /></button>
								</div>
							{/each}
						</div>
						<label class="mt-2 flex items-start gap-1.5 text-[11px] text-[var(--al-text)]">
							<input
								type="checkbox"
								checked={sso.syncGroupsOnLogin}
								onchange={(e) => setSyncGroups(e.currentTarget.checked)}
							/>
							<span>
								Re-apply on every login
								<span class="block text-[10px] text-[var(--al-muted)]">
									Makes the IdP the source of truth: it also <em>removes</em> access the mapping
									does not grant, overwriting admin/permission edits made in the Users tab.
								</span>
							</span>
						</label>
					{/if}
				</div>

				{#if !sso.secretStorable}
					<p class="text-xs text-[var(--al-danger)]">Set <span class="mono">ARCLOOM_SECRET_KEY</span> to store an encrypted client secret.</p>
				{/if}

				<div class="flex justify-end">
					<button
						class="inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40"
						disabled={savingSSO}
						onclick={saveSSO}
					>
						{#if savingSSO}<Loader2 size={14} class="animate-spin" />{/if} Save &amp; reconnect
					</button>
				</div>
			</div>
		{/if}
	{:else if tab === 'secrets'}
		{#if secretStore === null}
			<div class="flex items-center gap-2 py-6 text-sm text-[var(--al-muted)]"><Loader2 size={14} class="animate-spin" /> Loading…</div>
		{:else}
			<!-- Where secrets live. Values never enter generated HCL either way; this
			     only decides which store holds them. -->
			<div class="mb-3 flex items-center gap-2 rounded-lg border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2">
				<KeyRound size={15} class="text-[var(--al-accent)]" />
				<span class="text-sm font-semibold text-[var(--al-text)]">Secret store</span>
				<span class="ml-auto text-xs {secretStore.active ? 'text-[var(--al-accent)]' : 'text-[var(--al-danger)]'}">
					{secretStore.active ? `active: ${secretStore.active}` : 'no writable store'}
				</span>
			</div>

			<div class="space-y-3">
				<div class="flex flex-wrap items-center gap-2">
					{#each [{ id: 'local', label: 'Local (encrypted files)' }, { id: 'vault', label: 'Vault / OpenBao' }] as opt (opt.id)}
						<button
							class="rounded-md border px-3 py-1.5 text-xs font-medium {secretStore.kind === opt.id
								? 'border-[var(--al-accent)] bg-[var(--al-accent-weak)] text-[var(--al-accent)]'
								: 'border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]'}"
							onclick={() => (secretStore!.kind = opt.id as 'local' | 'vault')}
						>{opt.label}</button>
					{/each}
				</div>

				{#if secretStore.kind === 'local'}
					<p class="text-xs text-[var(--al-muted)]">
						Secrets are stored as AES-256-GCM files under the secrets directory, encrypted with the
						key derived from <span class="mono">ARCLOOM_SECRET_KEY</span>.
						{#if !secretStore.localEnabled}
							<span class="text-[var(--al-danger)]">That key is not set, so nothing can be stored locally.</span>
						{/if}
					</p>
				{:else}
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">Address</span>
						<input class={field} bind:value={secretStore.address} placeholder="https://vault.lan:8200" />
					</div>
					<div class="grid grid-cols-2 gap-3">
						<div class="space-y-1">
							<span class="block text-xs font-medium text-[var(--al-muted)]">KV v2 mount</span>
							<input class={field} bind:value={secretStore.mount} placeholder="secret" />
						</div>
						<div class="space-y-1">
							<span class="block text-xs font-medium text-[var(--al-muted)]">Path prefix</span>
							<input class={field} bind:value={secretStore.prefix} placeholder="arcloom" />
						</div>
					</div>
					<p class="text-[10px] text-[var(--al-muted)]">
						One document per project at
						<span class="mono">{secretStore.mount || 'secret'}/data/{secretStore.prefix || 'arcloom'}/&lt;project-id&gt;</span>.
						The token's policy needs read + write on that path.
					</p>
					<div class="grid grid-cols-2 gap-3">
						<div class="space-y-1">
							<span class="block text-xs font-medium text-[var(--al-muted)]">Token</span>
							<input
								class={field}
								type="password"
								bind:value={vaultToken}
								placeholder={secretStore.tokenSet
									? '•••••••• (unchanged)'
									: secretStore.tokenFromEnv
										? 'from ARCLOOM_VAULT_TOKEN'
										: ''}
							/>
						</div>
						<div class="space-y-1">
							<span class="block text-xs font-medium text-[var(--al-muted)]">Namespace</span>
							<input class={field} bind:value={secretStore.namespace} placeholder="(Vault Enterprise / OpenBao)" />
						</div>
					</div>
					<p class="text-[10px] text-[var(--al-muted)]">
						Storing a token needs <span class="mono">ARCLOOM_SECRET_KEY</span> to encrypt it at rest —
						or leave it blank and set <span class="mono">ARCLOOM_VAULT_TOKEN</span> so ArcLoom holds no
						credential on disk.
					</p>
					<label class="flex items-center gap-2 text-xs text-[var(--al-text)]">
						<input
							type="checkbox"
							checked={secretStore.insecureSkipVerify}
							onchange={(e) => (secretStore!.insecureSkipVerify = e.currentTarget.checked)}
						/>
						Skip TLS verification (self-signed certificate)
					</label>
					<label class="flex items-start gap-2 text-xs text-[var(--al-text)]">
						<input type="checkbox" checked={migrateSecrets} onchange={(e) => (migrateSecrets = e.currentTarget.checked)} />
						<span>
							Copy existing secrets into this store when saving
							<span class="block text-[10px] text-[var(--al-muted)]">
								Recommended: without it, values your resources already reference stop resolving and
								the next plan fails on a missing variable. The originals are left in place.
							</span>
						</span>
					</label>
				{/if}

				<div class="flex justify-end gap-2">
					{#if secretStore.kind === 'vault'}
						<button
							class="inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-3 py-1.5 text-sm font-medium text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40"
							disabled={secretBusy !== '' || !secretStore.address}
							onclick={testSecretStore}
						>
							{#if secretBusy === 'test'}<Loader2 size={14} class="animate-spin" />{/if} Test connection
						</button>
					{/if}
					<button
						class="inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40"
						disabled={secretBusy !== ''}
						onclick={saveSecretStore}
					>
						{#if secretBusy === 'save'}<Loader2 size={14} class="animate-spin" />{/if} Save
					</button>
				</div>
			</div>
		{/if}
	{:else if tab === 'audit'}
		<div class="mb-3 flex items-center gap-2">
			<ScrollText size={15} class="text-[var(--al-muted)]" />
			<span class="text-sm text-[var(--al-text)]">Recent changes — who did what, newest first.</span>
			<input class="{field} ml-auto max-w-40 py-1 text-xs" placeholder="filter by user" bind:value={auditUserFilter} onkeydown={(e) => { if (e.key === 'Enter') loadAudit(); }} />
			<button class="rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40" onclick={loadAudit} disabled={loadingAudit}>
				{#if loadingAudit}<Loader2 size={12} class="animate-spin" />{:else}Refresh{/if}
			</button>
		</div>
		<p class="mb-2 text-[11px] text-[var(--al-muted)]">Deploy operations (plan / apply / destroy) are recorded per project in the Runs panel.</p>
		<div class="max-h-[52vh] overflow-y-auto rounded-md border border-[var(--al-border)]">
			<table class="w-full text-xs">
				<thead class="sticky top-0 bg-[var(--al-panel-2)] text-[var(--al-muted)]">
					<tr>
						<th class="px-2.5 py-1.5 text-left font-medium">When</th>
						<th class="px-2.5 py-1.5 text-left font-medium">User</th>
						<th class="px-2.5 py-1.5 text-left font-medium">Via</th>
						<th class="px-2.5 py-1.5 text-left font-medium">Action</th>
						<th class="px-2.5 py-1.5 text-left font-medium">Detail</th>
					</tr>
				</thead>
				<tbody>
					{#each auditEntries as e (e.id)}
						<tr class="border-t border-[var(--al-border)]">
							<td class="whitespace-nowrap px-2.5 py-1 text-[var(--al-muted)]">{new Date(e.time).toLocaleString()}</td>
							<td class="px-2.5 py-1 text-[var(--al-text)]">{e.user || '—'}</td>
							<td class="whitespace-nowrap px-2.5 py-1">
								<span class="{viaClass(e.via)} rounded px-1.5 py-0.5 text-[10px]">{e.via || 'unknown'}</span>
								{#if e.keyLabel}<span class="mono ml-1 text-[10px] text-[var(--al-muted)]" title="API key">{e.keyLabel}</span>{/if}
							</td>
							<td class="px-2.5 py-1"><span class="mono rounded bg-[var(--al-accent-weak)] px-1.5 py-0.5 text-[10px] text-[var(--al-accent)]">{e.action}</span></td>
							<td class="mono truncate px-2.5 py-1 text-[10px] text-[var(--al-muted)]" style="max-width:0;">{e.detail ?? ''}</td>
						</tr>
					{/each}
					{#if auditEntries.length === 0 && !loadingAudit}
						<tr><td colspan="5" class="px-2.5 py-4 text-center text-[var(--al-muted)]">No audit entries yet.</td></tr>
					{/if}
				</tbody>
			</table>
		</div>
	{:else if tab === 'instance'}
		<div class="mb-3 flex items-center gap-2">
			<Server size={15} class="text-[var(--al-muted)]" />
			<span class="text-sm text-[var(--al-text)]">What this server is running, and what it can see.</span>
			<button class="ml-auto rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40" onclick={loadInstance} disabled={loadingInstance}>
				{#if loadingInstance}<Loader2 size={12} class="animate-spin" />{:else}Refresh{/if}
			</button>
		</div>

		{#if instance}
			{@const i = instance}
			<div class="max-h-[52vh] space-y-3 overflow-y-auto">
				<!-- Build -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">Build</div>
					<div class="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 text-xs">
						<span class="text-[var(--al-muted)]">version</span><span class="mono text-[var(--al-text)]">{i.version}</span>
						{#if i.commit}<span class="text-[var(--al-muted)]">commit</span><span class="mono text-[var(--al-text)]">{i.commit}</span>{/if}
						{#if i.buildDate}<span class="text-[var(--al-muted)]">built</span><span class="mono text-[var(--al-text)]">{i.buildDate}</span>{/if}
						<span class="text-[var(--al-muted)]">runtime</span><span class="mono text-[var(--al-text)]">{i.go} · {i.platform}</span>
						<span class="text-[var(--al-muted)]">uptime</span><span class="mono text-[var(--al-text)]">{uptime(i.uptimeSeconds)} (since {new Date(i.startedAt).toLocaleString()})</span>
						<span class="text-[var(--al-muted)]">memory</span><span class="mono text-[var(--al-text)]">{i.heapMB} MiB heap · {i.goroutines} goroutines</span>
						<span class="text-[var(--al-muted)]">content</span><span class="mono text-[var(--al-text)]">{i.projects} project(s) · {i.users} user(s)</span>
					</div>
				</div>

				<!-- Engines + tools -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">Engines &amp; tools</div>
					{#each i.engines as e (e.name)}
						<div class="flex items-center gap-2 py-0.5 text-xs">
							<span class="mono w-20 text-[var(--al-text)]">{e.name}</span>
							{#if e.error}
								<span class="text-[var(--al-danger)]">{e.error}</span>
							{:else}
								<span class="mono text-[var(--al-text)]">{e.version}</span>
							{/if}
							{#if e.default}<span class="rounded bg-[var(--al-accent-weak)] px-1.5 py-0.5 text-[10px] text-[var(--al-accent)]">default</span>{/if}
							<span class="mono ml-auto truncate text-[10px] text-[var(--al-muted)]" title={e.path}>{e.path}</span>
						</div>
					{/each}
					{#each i.tools as t (t.name)}
						<div class="flex items-center gap-2 py-0.5 text-xs">
							<span class="mono w-20 text-[var(--al-text)]">{t.name}</span>
							{#if t.available}
								<Check size={12} class="text-[var(--al-ok,#3fb950)]" />
								<span class="mono truncate text-[var(--al-muted)]">{t.version || 'installed'}</span>
							{:else}
								<X size={12} class="text-[var(--al-muted)]" />
								<span class="text-[var(--al-muted)]">not installed — {t.feature} degrades</span>
							{/if}
						</div>
					{/each}
				</div>

				<!-- Configuration -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">Configuration</div>
					<div class="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 text-xs">
						<span class="text-[var(--al-muted)]">auth</span>
						<span class="mono text-[var(--al-text)]">{i.authEnabled ? 'enabled' : 'DISABLED (open instance)'} · cookie {i.cookieSecure ? 'secure' : 'insecure (plain HTTP)'} · providers: {i.authProviders.join(', ') || 'local'}</span>
						<span class="text-[var(--al-muted)]">secret store</span>
						<span class="mono text-[var(--al-text)]">{i.secretStore ? 'local (encrypted)' : 'disabled — set ARCLOOM_SECRET_KEY'}</span>
						<span class="text-[var(--al-muted)]">logging</span><span class="mono text-[var(--al-text)]">{i.logLevel} · {i.logFormat}</span>
						<span class="text-[var(--al-muted)]">schema format</span>
						<span class="mono text-[var(--al-text)]">v{i.schemaFormat} <span class="text-[var(--al-muted)]">(cached schemas from other versions are ignored)</span></span>
					</div>
				</div>

				<!-- Paths -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">Paths</div>
					<div class="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 text-xs">
						{#each Object.entries(i.paths) as [k, v] (k)}
							<span class="text-[var(--al-muted)]">{k}</span>
							<span class="mono truncate text-[var(--al-text)]" title={v}>{v || '—'}</span>
						{/each}
					</div>
				</div>
			</div>
		{:else if !loadingInstance}
			<p class="py-4 text-center text-xs text-[var(--al-muted)]">No instance information.</p>
		{/if}
	{:else if tab === 'danger'}
		<!-- Master-key rotation. Kept out of the ordinary settings tabs because it
		     is the one operation here that can make credentials unrecoverable. -->
		{#if secretKey === null}
			<div class="flex items-center gap-2 py-6 text-sm text-[var(--al-muted)]"><Loader2 size={14} class="animate-spin" /> Loading…</div>
		{:else}
			{@const sk = secretKey}
			<div class="mb-3 flex items-center gap-2 rounded-lg border border-[var(--al-danger)]/40 bg-[var(--al-danger)]/10 px-3 py-2">
				<TriangleAlert size={15} class="text-[var(--al-danger)]" />
				<span class="text-sm font-semibold text-[var(--al-text)]">Rotate the master key</span>
				<button
					class="ml-auto rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
					onclick={loadSecretKey}
				>Refresh</button>
			</div>

			<div class="max-h-[56vh] space-y-3 overflow-y-auto pr-1">
				{#if !sk.keySet}
					<p class="rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-2 text-xs text-[var(--al-muted)]">
						<span class="mono">ARCLOOM_SECRET_KEY</span> is not configured, so nothing is encrypted with it
						and there is nothing to rotate.
					</p>
				{/if}

				<!-- What the key currently seals, and what it can still open. A field
				     that fails to decrypt used to be indistinguishable from one that
				     was never set; this is where that difference becomes visible. -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5">
					<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">What this key seals</div>
					<div class="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-xs">
						<span class="text-[var(--al-muted)]">secret files</span>
						<span class="mono text-[var(--al-text)]">
							{sk.secretFiles} project file(s) · {sk.secretValues} value(s)
							{#if sk.unreadableSecrets?.length}
								<span class="text-[var(--al-danger)]"> · {sk.unreadableSecrets.length} unreadable: {sk.unreadableSecrets.join(', ')}</span>
							{/if}
						</span>
						<span class="text-[var(--al-muted)]">settings fields</span>
						<span class="mono text-[var(--al-text)]">
							{sk.settingsFields.length} field(s)
							{#if sk.unreadableFields > 0}
								<span class="text-[var(--al-danger)]"> · {sk.unreadableFields} unreadable</span>
							{/if}
						</span>
					</div>
					{#if sk.settingsFields.length > 0}
						<div class="mt-1.5 flex flex-wrap gap-1">
							{#each sk.settingsFields as f (f.field)}
								<span
									class="mono rounded px-1.5 py-0.5 text-[10px] {f.readable
										? 'bg-[var(--al-panel-2)] text-[var(--al-muted)]'
										: 'bg-[var(--al-danger)]/15 text-[var(--al-danger)]'}"
									title={f.readable ? 'readable with the current key' : 'cannot be decrypted with the current key'}
								>{f.field}</span>
							{/each}
						</div>
					{/if}
					{#if sk.unreadableFields > 0 || sk.unreadableSecrets?.length}
						<p class="mt-1.5 text-[10px] text-[var(--al-danger)]">
							Some values do not open with the key this server is running. Rotating with the current key
							will not fix them — restore the key they were sealed with, or the backup.
						</p>
					{/if}
				</div>

				<!-- Blast radius, stated so it does not read larger than it is. -->
				<div class="rounded-md border border-[var(--al-border)] p-2.5 text-xs text-[var(--al-muted)]">
					<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide">Not affected</div>
					User passwords (hashed, not sealed with this key), login sessions, Terraform state,
					<span class="mono">ARCLOOM_SECRET_&lt;name&gt;</span> environment secrets{#if sk.externalStore},
					and the values held in <span class="mono">{sk.externalStore}</span> — only its access token is sealed here{/if}.
					<div class="mt-1.5">
						Back up the secrets directory and <span class="mono">settings.yaml</span> before rotating. With the
						server stopped you can run <span class="mono">arcloom rekey -old … -new …</span> instead, which has
						no window where the process and the environment disagree.
					</div>
				</div>

				<div class="grid grid-cols-2 gap-3">
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">Current key</span>
						<input class={field} type="password" bind:value={rkCurrent} autocomplete="off" placeholder="ARCLOOM_SECRET_KEY in use" />
					</div>
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">New key (min 12 chars)</span>
						<input class={field} type="password" bind:value={rkNew} autocomplete="new-password" />
					</div>
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">Repeat new key</span>
						<input class={field} type="password" bind:value={rkRepeat} autocomplete="new-password" />
					</div>
					<div class="space-y-1">
						<span class="block text-xs font-medium text-[var(--al-muted)]">Type {ROTATE_WORD} to confirm</span>
						<input class="{field} mono" bind:value={rkConfirm} placeholder={ROTATE_WORD} autocapitalize="characters" spellcheck="false" />
					</div>
				</div>
				{#if rkNew && rkRepeat && rkNew !== rkRepeat}
					<p class="text-xs text-[var(--al-danger)]">The two new keys do not match.</p>
				{/if}
				{#if sk.busy}
					<p class="text-xs text-[var(--al-danger)]">
						A plan, apply or drift check is running. Rotation waits for it to finish.
					</p>
				{/if}

				<div class="flex justify-end">
					<button
						class="inline-flex items-center gap-1.5 rounded-md bg-[var(--al-danger)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40"
						disabled={rotating || rotateBlocked}
						onclick={rotateSecretKey}
					>
						{#if rotating}<Loader2 size={14} class="animate-spin" />{:else}<RefreshCw size={14} />{/if} Rotate key
					</button>
				</div>

				{#if rotateResult}
					{@const rr = rotateResult}
					<div class="rounded-md border {rr.complete ? 'border-[var(--al-accent)]/50' : 'border-[var(--al-danger)]/50'} p-2.5 text-xs">
						<div class="mb-1.5 text-[11px] font-semibold uppercase tracking-wide {rr.complete ? 'text-[var(--al-accent)]' : 'text-[var(--al-danger)]'}">
							{rr.complete ? 'Rotation complete' : 'Rotation incomplete'}
						</div>
						<div class="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1">
							<span class="text-[var(--al-muted)]">secret files</span>
							<span class="mono text-[var(--al-text)]">{rr.secrets.rekeyed} re-keyed · {rr.secrets.verified} verified · {rr.secrets.values} value(s)</span>
							<span class="text-[var(--al-muted)]">settings fields</span>
							<span class="mono text-[var(--al-text)]">{rr.settings.rekeyed} re-keyed · {rr.settings.verified} verified</span>
						</div>
						{#each rr.warnings ?? [] as w (w)}
							<p class="mt-1.5 rounded bg-[var(--al-danger)]/10 px-2 py-1 text-[var(--al-danger)]">{w}</p>
						{/each}
						{#if !rr.complete}
							<p class="mt-1.5 text-[var(--al-danger)]">
								Re-run the rotation with the same keys to finish it, or restore your backup. The items
								still under the old key are listed above.
							</p>
						{/if}
					</div>
				{/if}
			</div>
		{/if}
	{/if}
</Modal>
