<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import { api, type AdminUser, type SSOSettings } from '$lib/api/client';
	import { auth } from '$lib/stores/auth.svelte';
	import { Loader2, Trash2, KeyRound, UserPlus, ShieldCheck } from 'lucide-svelte';

	let { open = $bindable(false) }: { open: boolean } = $props();

	type Tab = 'users' | 'sso';
	let tab = $state<Tab>('users');
	let error = $state('');
	let notice = $state('');

	const PERMS: { key: string; label: string }[] = [
		{ key: 'resources', label: 'Edit resources' },
		{ key: 'deploy', label: 'Deploy (plan)' },
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
				scopes: sso.scopes
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

	// Load per-tab when the modal opens or the tab changes.
	$effect(() => {
		if (!open) return;
		if (tab === 'users' && users.length === 0) loadUsers();
		if (tab === 'sso' && sso === null) loadSSO();
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
	{/if}
</Modal>
