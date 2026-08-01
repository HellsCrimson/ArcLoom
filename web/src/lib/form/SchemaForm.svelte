<script lang="ts">
	// SchemaForm renders a whole resource schema: required fields, then optional,
	// then computed-only fields shown read-only (referenceable but not editable).
	import type { AttrValue, Field, NormalizedSchema } from '$lib/types';
	import { isReadOnly } from '$lib/types';
	import type { AttrDiff } from '$lib/api/client';
	import { ACTION_COLOR } from '$lib/editor/graph';
	import FieldRenderer from './FieldRenderer.svelte';
	import PlanDiffList from '$lib/components/PlanDiffList.svelte';
	import { buildAttrs, cleanAttr } from './attrs';
	import { parseHclAttrs, renderHclAttrs, toHclText } from '$lib/model/hcltext';
	import { AlertTriangle, Code2, Trash2 } from 'lucide-svelte';

	let {
		schema,
		values = $bindable(),
		references = [],
		secrets = [],
		onCreateSecret,
		planDiffs = []
	}: {
		schema: NormalizedSchema;
		values: Record<string, AttrValue>;
		references?: string[];
		secrets?: string[];
		/** Store a new secret and refresh the list; absent = creating is unavailable. */
		onCreateSecret?: (name: string, value: string) => Promise<void>;
		/** Attribute diffs from the last plan for THIS resource — rendered inline on
		 *  the fields they touch, so a pending change is visible at the field
		 *  instead of only in the run log. */
		planDiffs?: AttrDiff[];
	} = $props();

	const required = $derived(schema.fields.filter((f) => f.required));
	const optional = $derived(schema.fields.filter((f) => f.optional && !f.required));
	const readonly = $derived(schema.fields.filter((f) => isReadOnly(f)));

	// Diffs are grouped by their FIRST path segment, which is the top-level field
	// name ("tags.Name" → "tags"), so a nested change annotates the field that
	// contains it without threading paths through the recursive renderers.
	const diffsByField = $derived.by(() => {
		const m = new Map<string, AttrDiff[]>();
		for (const d of planDiffs) {
			const root = d.path.split(/[.[]/)[0];
			const list = m.get(root);
			if (list) list.push(d);
			else m.set(root, [d]);
		}
		return m;
	});
	// A diff on the field itself renders as "was X" next to the input; nested ones
	// (a map entry, a block attribute) render as a small path-addressed list.
	const fieldDiffs = (name: string): AttrDiff[] => diffsByField.get(name) ?? [];

	// Ensure every editable field has an AttrValue entry so bindings are stable.
	$effect(() => {
		for (const f of schema.fields) {
			if (!isReadOnly(f) && !values[f.name]) {
				values[f.name] = { mode: 'literal' };
			}
		}
	});

	// --- Attributes the schema does not declare -----------------------------
	// The form renders fields FROM the schema, and the save writes back the whole
	// `values` map, so an attribute with no matching field is invisible and
	// permanent: it survives every save with no way to reach it. That happens
	// whenever the block's schema stops matching its content — a provider whose
	// source was wrong (the block then holds the other provider's attributes), a
	// provider upgrade that removed an argument, an import of a config written
	// for a different version. Naming them, and letting them be removed, is the
	// difference between "the plan fails and I cannot fix it here" and a click.
	const knownNames = $derived(new Set(schema.fields.map((f) => f.name)));
	const unknown = $derived(
		Object.keys(values)
			.filter((k) => !knownNames.has(k))
			// An empty slot is just an untouched field, not a stale attribute:
			// cleanAttr drops it on save, so it would be noise to report.
			.filter((k) => cleanAttr(values[k]) !== undefined)
			.sort()
	);

	function removeAttr(name: string) {
		const next = { ...values };
		delete next[name];
		values = next;
	}

	// --- Raw editing (advanced) ---------------------------------------------
	// The deliberate escape hatch: edit the block's attributes as text. It is the
	// only way to fix a value whose widget cannot express it, and the only way to
	// see everything the block actually holds. Applied explicitly, never live —
	// a half-typed line must not wipe the config.
	let rawOpen = $state(false);
	let rawText = $state('');
	let rawError = $state('');

	function openRaw() {
		rawText = renderHclAttrs(buildAttrs(values));
		rawError = '';
		rawOpen = true;
	}
	function applyRaw() {
		const parsed = parseHclAttrs(rawText);
		if (!parsed) {
			rawError = 'Could not parse — every line must read `name = value` (HCL). Nothing was changed.';
			return;
		}
		values = parsed;
		rawError = '';
		rawOpen = false;
	}
</script>

{#snippet section(title: string, fields: Field[])}
	{#if fields.length > 0}
		<div class="mb-5">
			<h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
				{title}
			</h3>
			<div class="space-y-4">
				{#each fields as f (f.name)}
					{@const diffs = fieldDiffs(f.name)}
					<div>
						<div class="mb-1 flex items-center gap-2">
							<span class="text-sm font-medium text-[var(--al-text)]">{f.name}</span>
							{#if f.required}<span class="text-[var(--al-danger)]">*</span>{/if}
							<span class="mono text-[10px] text-[var(--al-muted)]">{f.type}</span>
							{#if f.sensitive}
								<span class="rounded bg-[var(--al-accent-weak)] px-1 text-[10px] text-[var(--al-accent)]"
									>sensitive</span
								>
							{/if}
							{#if diffs.length > 0}
								{@const forces = diffs.some((d) => d.forcesReplace)}
								<span
									class="rounded px-1 text-[10px] font-semibold"
									style="background:{forces
										? ACTION_COLOR.replace
										: ACTION_COLOR.update}26;color:{forces
										? ACTION_COLOR.replace
										: ACTION_COLOR.update};"
									title="The last plan changes this attribute{forces
										? ' — and this change forces the resource to be replaced'
										: ''}"
								>
									{forces ? '± forces replace' : '~ plan changes this'}
								</span>
							{/if}
						</div>
						{#if f.description}
							<p class="mb-1.5 text-xs leading-snug text-[var(--al-muted)]">{f.description}</p>
						{/if}
						{#if diffs.length > 0}
							<div
								class="mb-1.5 rounded border px-2 py-1"
								style="border-color:{ACTION_COLOR.update}44;background:{ACTION_COLOR.update}0f;"
							>
								<PlanDiffList {diffs} compact />
							</div>
						{/if}
						{#if values[f.name]}
							<FieldRenderer field={f} bind:value={values[f.name]} {references} {secrets} {onCreateSecret} />
						{/if}
					</div>
				{/each}
			</div>
		</div>
	{/if}
{/snippet}

{#if unknown.length > 0}
	<div class="mb-5 rounded-md border px-3 py-2" style="border-color:var(--al-warn);background:color-mix(in srgb, var(--al-warn) 8%, transparent);">
		<div class="mb-1.5 flex items-center gap-2 text-xs font-semibold text-[var(--al-warn)]">
			<AlertTriangle size={13} />
			{unknown.length} attribute{unknown.length === 1 ? '' : 's'} this schema does not declare
		</div>
		<p class="mb-2 text-[11px] text-[var(--al-muted)]">
			They are saved with the block and sent to the engine, which will reject them — but they have
			no field here, so nothing else can remove them. This usually means the block was written
			against a different provider or an older version of it.
		</p>
		<div class="space-y-1">
			{#each unknown as name (name)}
				<div class="flex items-start gap-2">
					<span class="mono flex-none text-xs text-[var(--al-text)]">{name}</span>
					<span class="mono min-w-0 flex-1 truncate text-[11px] text-[var(--al-muted)]" title={toHclText(values[name])}>= {toHclText(values[name])}</span>
					<button
						class="flex-none rounded p-0.5 text-[var(--al-muted)] hover:text-[var(--al-danger)]"
						title="Remove this attribute from the block"
						aria-label="Remove {name}"
						onclick={() => removeAttr(name)}
					><Trash2 size={13} /></button>
				</div>
			{/each}
		</div>
	</div>
{/if}

{@render section('Required', required)}
{@render section('Optional', optional)}

{#if readonly.length > 0}
	<div class="mb-2">
		<h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]">
			Computed (read-only)
		</h3>
		<div class="flex flex-wrap gap-1.5">
			{#each readonly as f (f.name)}
				{@const diffs = fieldDiffs(f.name)}
				<span
					class="mono rounded border bg-[var(--al-panel)] px-2 py-0.5 text-[11px]"
					style={diffs.length > 0
						? `border-color:${ACTION_COLOR.update}66;color:${ACTION_COLOR.update};`
						: 'border-color:var(--al-border);color:var(--al-muted);'}
					title={diffs.length > 0
						? diffs.map((d) => `${d.path}: ${d.before ?? ''} → ${d.after ?? ''}`).join('\n')
						: f.description}
				>
					{diffs.length > 0 ? '~ ' : ''}{f.name}{f.sensitive ? ' 🔒' : ''}
				</span>
			{/each}
		</div>
	</div>
{/if}

<!--
  Raw attributes — the advanced/dangerous escape hatch. Everything the block
  holds, as HCL text: the widgets cannot express every value a config can carry,
  and an attribute the schema does not declare has no widget at all. Applying
  REPLACES the block's attributes with exactly what is in the box, which is the
  point — it is how something unreachable gets removed.
-->
<div class="mt-4 border-t border-[var(--al-border)] pt-3">
	{#if !rawOpen}
		<button
			class="inline-flex items-center gap-1.5 text-[11px] text-[var(--al-muted)] hover:text-[var(--al-accent)]"
			onclick={openRaw}
		><Code2 size={12} /> Edit attributes as raw HCL (advanced)</button>
	{:else}
		<div class="mb-1.5 flex items-center justify-between">
			<span class="text-xs font-semibold uppercase tracking-wide text-[var(--al-warn)]">Raw attributes — advanced</span>
			<span class="text-[11px] text-[var(--al-muted)]">Applying replaces every attribute of this block.</span>
		</div>
		<textarea
			class="mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] p-2.5 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]"
			rows="10"
			spellcheck="false"
			bind:value={rawText}
		></textarea>
		{#if rawError}
			<p class="mt-1 text-[11px] text-[var(--al-danger)]">{rawError}</p>
		{:else}
			<p class="mt-1 text-[11px] text-[var(--al-muted)]">
				One <span class="mono">name = value</span> per line; a nested object or list may span lines.
				References and expressions (<span class="mono">var.x</span>, <span class="mono">each.value</span>) are kept as written.
			</p>
		{/if}
		<div class="mt-2 flex gap-2">
			<button class="rounded-md bg-[var(--al-accent)] px-2.5 py-1 text-xs font-medium text-white hover:brightness-110" onclick={applyRaw}>Apply</button>
			<button class="rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs text-[var(--al-text)] hover:bg-[var(--al-panel-2)]" onclick={() => (rawOpen = false)}>Cancel</button>
		</div>
	{/if}
</div>
