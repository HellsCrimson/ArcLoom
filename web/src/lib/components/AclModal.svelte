<script lang="ts">
	import { api, type AdminUser } from '$lib/api/client';
	import Modal from './Modal.svelte';
	import { Loader2, ShieldCheck, Users } from 'lucide-svelte';

	let {
		open = $bindable(false),
		projectId,
		projectName
	}: { open: boolean; projectId: string; projectName: string } = $props();

	const PERMS = [
		{ key: 'resources', label: 'Edit' },
		{ key: 'deploy', label: 'Deploy' },
		{ key: 'git', label: 'Git' }
	];

	let users = $state<AdminUser[]>([]);
	let restricted = $state(false);
	let members = $state<Set<string>>(new Set());
	let perms = $state<Record<string, Set<string>>>({});
	let rbacAvailable = $state(true);
	let busy = $state(false);
	let err = $state('');
	let info = $state('');

	$effect(() => {
		if (open && projectId) load();
	});

	async function load() {
		err = '';
		info = '';
		try {
			const [acl, list, lic] = await Promise.all([
				api.getACL(projectId),
				api.admin.listUsers(),
				api.getLicense()
			]);
			// Admins have implicit full access and are never listed as members.
			users = list.filter((u) => !u.admin);
			rbacAvailable = lic.features.includes('rbac');
			restricted = acl.restricted;
			const m = new Set<string>();
			const p: Record<string, Set<string>> = {};
			for (const e of acl.entries) {
				m.add(e.userId);
				p[e.userId] = new Set(e.perms);
			}
			members = m;
			perms = p;
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function toggleMember(id: string) {
		const m = new Set(members);
		if (m.has(id)) m.delete(id);
		else {
			m.add(id);
			if (!perms[id]) perms = { ...perms, [id]: new Set() };
		}
		members = m;
	}

	function togglePerm(id: string, perm: string) {
		const cur = new Set(perms[id] ?? []);
		if (cur.has(perm)) cur.delete(perm);
		else cur.add(perm);
		perms = { ...perms, [id]: cur };
	}

	async function save() {
		busy = true;
		err = '';
		info = '';
		try {
			// When unrestricted, clear the ACL (empty entries).
			const entries = restricted
				? [...members].map((id) => ({ userId: id, perms: [...(perms[id] ?? [])] }))
				: [];
			const acl = await api.admin.setACL(projectId, entries);
			restricted = acl.restricted;
			info = restricted ? 'Access list saved' : 'Access restriction removed';
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm text-[var(--al-text)]';
	const btnPrimary =
		'inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40';
</script>

<Modal title="Project access" bind:open wide>
	{#if err}
		<div class="mb-2 rounded-md border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{err}</div>
	{/if}
	{#if info}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-xs text-[var(--al-muted)]">{info}</div>
	{/if}

	<p class="mb-3 text-xs text-[var(--al-muted)]">
		Control who can access <span class="mono text-[var(--al-text)]">{projectName}</span>. Admins always
		have full access. A listed user's effective permissions are the intersection of their global
		permissions and what you grant here.
	</p>

	{#if !rbacAvailable}
		<div class="mb-3 rounded-md border border-[var(--al-warn,#d29922)] bg-[var(--al-warn,#d29922)]/10 px-3 py-1.5 text-xs text-[var(--al-warn,#d29922)]">
			Per-project access lists require ArcLoom Enterprise (rbac). You can still remove an existing
			restriction below.
		</div>
	{/if}

	<label class="mb-3 flex items-center gap-2 text-sm text-[var(--al-text)]">
		<input type="checkbox" checked={restricted} onchange={() => (restricted = !restricted)} class="accent-[var(--al-accent)]" />
		<ShieldCheck size={14} /> Restrict this project to specific users
	</label>

	{#if restricted}
		<div class="overflow-hidden rounded-md border border-[var(--al-border)]">
			<div class="grid grid-cols-[1fr_auto] items-center gap-2 border-b border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				<span>User (checked = has access)</span>
				<span class="flex gap-3">{#each PERMS as p}<span class="w-12 text-center">{p.label}</span>{/each}</span>
			</div>
			{#if users.length === 0}
				<p class="px-3 py-3 text-xs text-[var(--al-muted)]"><Users size={13} class="mr-1 inline" /> No non-admin users to grant access to.</p>
			{/if}
			{#each users as u (u.id)}
				<div class="grid grid-cols-[1fr_auto] items-center gap-2 border-b border-[var(--al-border)] px-3 py-1.5 last:border-b-0">
					<label class="flex cursor-pointer items-center gap-2 text-sm text-[var(--al-text)]">
						<input type="checkbox" checked={members.has(u.id)} onchange={() => toggleMember(u.id)} class="accent-[var(--al-accent)]" />
						{u.username}
						{#if u.disabled}<span class="text-[10px] text-[var(--al-muted)]">(disabled)</span>{/if}
					</label>
					<span class="flex gap-3">
						{#each PERMS as p}
							<span class="w-12 text-center">
								<input
									type="checkbox"
									checked={perms[u.id]?.has(p.key) ?? false}
									disabled={!members.has(u.id)}
									onchange={() => togglePerm(u.id, p.key)}
									class="accent-[var(--al-accent)] disabled:opacity-30"
									title={u.permissions.includes(p.key) ? '' : `user lacks the global "${p.key}" permission — grant has no effect`}
								/>
							</span>
						{/each}
					</span>
				</div>
			{/each}
		</div>
		<p class="mt-2 text-[10px] text-[var(--al-muted)]">
			A grant only takes effect if the user also holds the matching global permission (manage those
			in Settings → Users). Unchecked users cannot open the project at all.
		</p>
	{/if}

	<div class="mt-4 flex justify-end">
		<button class={btnPrimary} onclick={save} disabled={busy || (restricted && !rbacAvailable)}>
			{#if busy}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	</div>
</Modal>
