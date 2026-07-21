<script lang="ts">
	import { auth } from '$lib/stores/auth.svelte';
	import { theme } from '$lib/stores/theme.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { UserRound, ChevronDown, LogOut, KeyRound, Monitor, Sun, Moon } from 'lucide-svelte';

	const themeBtn = (active: boolean) =>
		`flex-1 inline-flex items-center justify-center rounded p-1 ${active ? 'bg-[var(--al-accent-weak)] text-[var(--al-accent)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}`;

	let open = $state(false);
	let showChange = $state(false);

	let current = $state('');
	let next = $state('');
	let confirm = $state('');
	let busy = $state(false);
	let err = $state('');
	let ok = $state('');

	function openChange() {
		open = false;
		current = '';
		next = '';
		confirm = '';
		err = '';
		ok = '';
		showChange = true;
	}

	async function submitChange() {
		err = '';
		ok = '';
		if (next !== confirm) {
			err = 'New passwords do not match';
			return;
		}
		busy = true;
		try {
			const msg = await auth.changePassword(current, next);
			if (msg) {
				err = msg;
				return;
			}
			// Success: sessions were revoked; the layout gate will show the login
			// screen. Close the modal.
			ok = 'Password changed — please sign in again.';
			showChange = false;
		} finally {
			busy = false;
		}
	}

	const field =
		'w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] px-3 py-2 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
</script>

<div class="relative">
	<button
		class="inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs font-medium text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
		onclick={(e) => {
			e.stopPropagation();
			open = !open;
		}}
	>
		<UserRound size={14} />
		{auth.user?.username ?? 'account'}
		<ChevronDown size={13} />
	</button>
	{#if open}
		<div
			class="absolute right-0 top-full z-20 mt-1 w-44 overflow-hidden rounded-md border border-[var(--al-border)] bg-[var(--al-panel)] shadow-xl"
		>
			<div class="border-b border-[var(--al-border)] px-2 py-1.5">
				<div class="mb-1 px-1 text-[10px] font-semibold uppercase tracking-wide text-[var(--al-muted)]">Theme</div>
				<div class="flex items-center gap-1">
					<button class={themeBtn(theme.pref === 'system')} onclick={() => theme.set('system')} title="Match system">
						<Monitor size={14} />
					</button>
					<button class={themeBtn(theme.pref === 'light')} onclick={() => theme.set('light')} title="Light">
						<Sun size={14} />
					</button>
					<button class={themeBtn(theme.pref === 'dark')} onclick={() => theme.set('dark')} title="Dark">
						<Moon size={14} />
					</button>
				</div>
			</div>
			<button
				class="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
				onclick={openChange}
			>
				<KeyRound size={13} /> Change password
			</button>
			<button
				class="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
				onclick={() => {
					open = false;
					auth.logout();
				}}
			>
				<LogOut size={13} /> Log out
			</button>
		</div>
	{/if}
</div>

<Modal title="Change password" bind:open={showChange}>
	<div class="space-y-3">
		<div class="space-y-1">
			<label for="pw-current" class="block text-xs font-medium text-[var(--al-muted)]">Current password</label>
			<input id="pw-current" type="password" class={field} bind:value={current} autocomplete="current-password" />
		</div>
		<div class="space-y-1">
			<label for="pw-new" class="block text-xs font-medium text-[var(--al-muted)]">New password</label>
			<input id="pw-new" type="password" class={field} bind:value={next} autocomplete="new-password" />
		</div>
		<div class="space-y-1">
			<label for="pw-confirm" class="block text-xs font-medium text-[var(--al-muted)]">Confirm new password</label>
			<input id="pw-confirm" type="password" class={field} bind:value={confirm} autocomplete="new-password" />
		</div>
		{#if err}<p class="text-sm text-[var(--al-danger)]">{err}</p>{/if}
	</div>
	{#snippet footer()}
		<button
			class="inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-3 py-1.5 text-sm text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
			onclick={() => (showChange = false)}
		>
			Cancel
		</button>
		<button
			class="inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40"
			disabled={busy || !current || !next}
			onclick={submitChange}
		>
			Change password
		</button>
	{/snippet}
</Modal>

{#if ok}
	<div class="border-b border-[var(--al-border)] bg-[var(--al-panel-2)] px-4 py-1.5 text-xs text-[var(--al-muted)]">{ok}</div>
{/if}
