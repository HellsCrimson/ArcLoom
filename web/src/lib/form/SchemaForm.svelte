<script lang="ts">
	// SchemaForm renders a whole resource schema: required fields, then optional,
	// then computed-only fields shown read-only (referenceable but not editable).
	import type { AttrValue, Field, NormalizedSchema } from '$lib/types';
	import { isReadOnly } from '$lib/types';
	import type { AttrDiff } from '$lib/api/client';
	import { ACTION_COLOR } from '$lib/editor/graph';
	import FieldRenderer from './FieldRenderer.svelte';
	import PlanDiffList from '$lib/components/PlanDiffList.svelte';

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
