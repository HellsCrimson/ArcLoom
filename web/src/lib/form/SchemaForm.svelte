<script lang="ts">
	// SchemaForm renders a whole resource schema: required fields, then optional,
	// then computed-only fields shown read-only (referenceable but not editable).
	import type { AttrValue, Field, NormalizedSchema } from '$lib/types';
	import { isReadOnly } from '$lib/types';
	import FieldRenderer from './FieldRenderer.svelte';

	let {
		schema,
		values = $bindable(),
		references = []
	}: {
		schema: NormalizedSchema;
		values: Record<string, AttrValue>;
		references?: string[];
	} = $props();

	const required = $derived(schema.fields.filter((f) => f.required));
	const optional = $derived(schema.fields.filter((f) => f.optional && !f.required));
	const readonly = $derived(schema.fields.filter((f) => isReadOnly(f)));

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
						</div>
						{#if f.description}
							<p class="mb-1.5 text-xs leading-snug text-[var(--al-muted)]">{f.description}</p>
						{/if}
						{#if values[f.name]}
							<FieldRenderer field={f} bind:value={values[f.name]} {references} />
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
				<span
					class="mono rounded border border-[var(--al-border)] bg-[var(--al-panel)] px-2 py-0.5 text-[11px] text-[var(--al-muted)]"
					title={f.description}
				>
					{f.name}{f.sensitive ? ' 🔒' : ''}
				</span>
			{/each}
		</div>
	</div>
{/if}
