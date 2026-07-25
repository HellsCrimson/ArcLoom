<script lang="ts">
	import { api, type NotifyKind } from '$lib/api/client';
	import Modal from './Modal.svelte';
	import { Loader2, Bell, Send } from 'lucide-svelte';

	let {
		open = $bindable(false),
		projectId
	}: { open: boolean; projectId: string } = $props();

	// Destinations, in the order they appear in the select. `hint` explains which
	// URL to paste (each service mints it somewhere different) and `placeholder`
	// shows its shape.
	const DESTINATIONS: {
		value: NotifyKind;
		label: string;
		placeholder: string;
		hint: string;
	}[] = [
		{
			value: 'slack',
			label: 'Slack / Mattermost / Rocket.Chat',
			placeholder: 'https://hooks.slack.com/services/…',
			hint: 'An incoming-webhook URL. Slack: Apps → Incoming Webhooks. Mattermost and Rocket.Chat accept the same payload.'
		},
		{
			value: 'discord',
			label: 'Discord',
			placeholder: 'https://discord.com/api/webhooks/…',
			hint: 'Channel → Edit Channel → Integrations → Webhooks → Copy Webhook URL.'
		},
		{
			value: 'teams',
			label: 'Microsoft Teams',
			placeholder: 'https://prod-….logic.azure.com/workflows/…',
			hint: 'Teams channel → Workflows → "Post to a channel when a webhook request is received". ArcLoom posts an Adaptive Card (Office 365 connectors are retired).'
		},
		{
			value: 'matrix',
			label: 'Matrix',
			placeholder: 'https://matrix.example.org/_matrix/client/v3/rooms/!room:server/send/m.room.message?access_token=…',
			hint: 'The client-server send endpoint for the room, with an access token. Using a bridge (hookshot) instead? Pick Slack or Webhook.'
		},
		{
			value: 'ntfy',
			label: 'ntfy',
			placeholder: 'https://ntfy.sh/my-topic',
			hint: 'Your topic URL. The summary becomes the notification title; failures are sent at high priority.'
		},
		{
			value: 'fluxer',
			label: 'Fluxer',
			placeholder: 'https://api.fluxer.app/v1/webhooks/{id}/{token}',
			hint: 'A channel webhook URL — Fluxer mirrors the Discord webhook API, so the same message shape is used.'
		},
		{
			value: 'webhook',
			label: 'Custom webhook (raw JSON event)',
			placeholder: 'https://example.com/hooks/arcloom',
			hint: 'Any endpoint that accepts a JSON POST; it receives the raw event (type, level, project, env, op, user, summary, detail, at).'
		}
	];

	let enabled = $state(false);
	let kind = $state<NotifyKind>('webhook'); // replaced by the loaded config
	let url = $state(''); // only ever holds a NEW url; the stored one is never sent back
	let urlSet = $state(false);
	let urlHint = $state('');
	let storable = $state(true);
	let onApplyFailure = $state(true);
	let onApplySuccess = $state(false);
	let onDrift = $state(true);
	let busy = $state(false);
	let testing = $state(false);
	let err = $state('');
	let info = $state('');

	$effect(() => {
		if (open && projectId) load();
	});

	async function load() {
		err = '';
		info = '';
		url = '';
		try {
			const res = await api.getNotify(projectId);
			apply(res.config);
		} catch (e) {
			err = (e as Error).message;
		}
	}

	function apply(c: Awaited<ReturnType<typeof api.getNotify>>['config']) {
		enabled = c.enabled;
		kind = c.kind;
		urlSet = c.urlSet;
		urlHint = c.urlHint ?? '';
		storable = c.storable;
		onApplyFailure = c.onApplyFailure;
		onApplySuccess = c.onApplySuccess;
		onDrift = c.onDrift;
	}

	async function save() {
		busy = true;
		err = '';
		info = '';
		try {
			const res = await api.setNotify(projectId, {
				enabled,
				kind,
				url: url.trim(),
				onApplyFailure,
				onApplySuccess,
				onDrift
			});
			apply(res.config);
			url = '';
			info = 'Notification settings saved';
		} catch (e) {
			err = (e as Error).message;
		} finally {
			busy = false;
		}
	}

	async function test() {
		testing = true;
		err = '';
		info = '';
		try {
			await api.testNotify(projectId);
			info = 'Test notification delivered';
		} catch (e) {
			err = (e as Error).message;
		} finally {
			testing = false;
		}
	}

	const destination = $derived(DESTINATIONS.find((d) => d.value === kind) ?? DESTINATIONS[0]);

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-sm text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const btnPrimary =
		'inline-flex items-center gap-1.5 rounded-md bg-[var(--al-accent)] px-3 py-1.5 text-sm font-medium text-white hover:brightness-110 disabled:opacity-40';
	const btnGhost =
		'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1.5 text-sm text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40';
</script>

<Modal title="Notifications" bind:open>
	{#if err}
		<div class="mb-2 rounded-md border border-[var(--al-danger)] bg-[var(--al-danger)]/10 px-3 py-1.5 text-xs text-[var(--al-danger)]">{err}</div>
	{/if}
	{#if info}
		<div class="mb-2 rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-3 py-1.5 text-xs text-[var(--al-muted)]">{info}</div>
	{/if}

	<p class="mb-3 text-xs text-[var(--al-muted)]">
		Post this project's operational events to a webhook, so an apply failure or a scheduled drift check
		is visible outside ArcLoom. Each destination gets the message shape it expects. Drift events only
		fire when the outcome changes, so a steadily drifted project reports once rather than every interval.
	</p>

	{#if !storable}
		<div class="mb-3 rounded-md border border-[var(--al-warn,#d29922)] bg-[var(--al-warn,#d29922)]/10 px-3 py-1.5 text-xs text-[var(--al-warn,#d29922)]">
			Set <span class="mono">ARCLOOM_SECRET_KEY</span> on the server to store a webhook URL — it is a credential and
			is encrypted at rest.
		</div>
	{/if}

	<div class="mb-3 space-y-2">
		<label class="block">
			<span class="mb-1 block text-xs text-[var(--al-muted)]">Destination</span>
			<select class="{inp} w-full" bind:value={kind}>
				{#each DESTINATIONS as d (d.value)}
					<option value={d.value}>{d.label}</option>
				{/each}
			</select>
		</label>
		<label class="block">
			<span class="mb-1 block text-xs text-[var(--al-muted)]">
				Webhook URL
				{#if urlSet}
					<span class="ml-1 rounded border border-[var(--al-border)] px-1 py-0.5 text-[10px] text-[var(--al-muted)]">
						stored{urlHint ? ` · ${urlHint}` : ''}
					</span>
				{/if}
			</span>
			<input
				class="{inp} mono w-full"
				type="url"
				bind:value={url}
				placeholder={urlSet ? 'leave blank to keep the stored URL' : destination.placeholder}
			/>
			<span class="mt-1 block text-[11px] text-[var(--al-muted)]">{destination.hint}</span>
		</label>
	</div>

	<div class="mb-4 space-y-1.5">
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={enabled} onchange={() => (enabled = !enabled)} class="accent-[var(--al-accent)]" />
			<Bell size={14} /> Send notifications for this project
		</label>
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={onApplyFailure} onchange={() => (onApplyFailure = !onApplyFailure)} class="accent-[var(--al-accent)]" disabled={!enabled} />
			Apply / destroy <span class="text-[var(--al-danger)]">failed</span>
		</label>
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={onApplySuccess} onchange={() => (onApplySuccess = !onApplySuccess)} class="accent-[var(--al-accent)]" disabled={!enabled} />
			Apply / destroy succeeded
		</label>
		<label class="flex items-center gap-2 text-sm text-[var(--al-text)]">
			<input type="checkbox" checked={onDrift} onchange={() => (onDrift = !onDrift)} class="accent-[var(--al-accent)]" disabled={!enabled} />
			Scheduled drift detected or resolved
		</label>
	</div>

	<div class="flex justify-end gap-2">
		<button class={btnGhost} onclick={test} disabled={testing || !urlSet} title={urlSet ? 'Deliver a test event now' : 'Save a webhook URL first'}>
			{#if testing}<Loader2 size={14} class="animate-spin" />{:else}<Send size={14} />{/if} Send test
		</button>
		<button class={btnPrimary} onclick={save} disabled={busy}>
			{#if busy}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	</div>
</Modal>
