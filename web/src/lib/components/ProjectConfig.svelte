<script lang="ts">
	import type { AttrValue, ProjectConfig } from '$lib/types';
	import Modal from './Modal.svelte';
	import DynamicEditor from '$lib/form/DynamicEditor.svelte';
	import { rawToDynamic } from '$lib/form/defaults';
	import { cleanAttr } from '$lib/form/attrs';
	import { Plus, Trash2, Loader2, Braces, Code2 } from 'lucide-svelte';

	let {
		config,
		title = 'Variables, locals & outputs',
		open = $bindable(false),
		saving = false,
		references = [],
		secrets = [],
		onCreateSecret,
		onSave
	}: {
		config: ProjectConfig;
		title?: string;
		open: boolean;
		saving?: boolean;
		references?: string[];
		secrets?: string[];
		onCreateSecret?: (name: string, value: string) => Promise<void>;
		onSave: (config: ProjectConfig) => void;
	} = $props();

	// A default/value is edited EITHER as raw HCL text or structurally. Both
	// representations are kept per row so toggling back restores what was typed —
	// the same rule the resource form's mode toggle follows.
	type ValueRow = { text: string; struct: AttrValue; structured: boolean };
	type VarRow = { name: string; type: string; def: ValueRow; description: string; sensitive: boolean };
	type LocalRow = { name: string; value: ValueRow };
	type OutRow = { name: string; value: ValueRow; description: string; sensitive: boolean };

	let vars = $state<VarRow[]>([]);
	let locals = $state<LocalRow[]>([]);
	let outputs = $state<OutRow[]>([]);

	// Render an AttrValue as editable HCL text.
	function attrText(av?: AttrValue): string {
		if (!av) return '';
		if (av.mode === 'expression') return av.expr ?? '';
		if (av.mode === 'reference') return av.ref ?? '';
		if (av.mode === 'literal') return JSON.stringify(av.literal ?? '');
		return '';
	}
	// Text -> AttrValue (raw HCL expression); undefined when blank.
	function textToAttr(text: string): AttrValue | undefined {
		const t = text.trim();
		return t ? { mode: 'expression', expr: t } : undefined;
	}

	const isObj = (x: unknown) => typeof x === 'object' && x !== null && !Array.isArray(x);

	// A value opens in the structural editor when it IS a structure — an already
	// structural AttrValue, a container literal (how hclimport stores an all-literal
	// `{ alpha = { … } }`, which used to render as one line of JSON), or a secret
	// (whose text form is '' — rendering it as text silently wiped it on save).
	// Scalars stay in the one-line HCL box: the common `default = "prod"` should
	// not cost a kind selector.
	function wantsStructured(av?: AttrValue): boolean {
		if (!av) return false;
		if (av.mode === 'secret') return true;
		if (av.entries !== undefined || av.items !== undefined) return true;
		return av.mode === 'literal' && (Array.isArray(av.literal) || isObj(av.literal));
	}

	// Build both representations up front so the per-row toggle is lossless.
	function toRow(av?: AttrValue): ValueRow {
		const structured = wantsStructured(av);
		return {
			text: structured ? '' : attrText(av),
			struct: structured ? structFrom(av!) : { mode: 'literal', entries: [] },
			structured
		};
	}
	// rawToDynamic lifts a container literal into entries/items; an AttrValue that
	// is already structural (or a secret) is taken as-is.
	function structFrom(av: AttrValue): AttrValue {
		if (av.mode === 'literal' && av.entries === undefined && av.items === undefined) {
			return rawToDynamic(av.literal);
		}
		return JSON.parse(JSON.stringify(av));
	}
	function rowToAttr(row: ValueRow): AttrValue | undefined {
		return row.structured ? cleanAttr(row.struct) : textToAttr(row.text);
	}
	const rowFilled = (row: ValueRow) => rowToAttr(row) !== undefined;

	// Toggling keeps the other representation untouched, so a mis-click is undone
	// by clicking back rather than by retyping.
	function toggleStructured(row: ValueRow) {
		row.structured = !row.structured;
	}

	// Load the scope's config whenever the modal opens.
	$effect(() => {
		if (!open) return;
		vars = (config.variables ?? []).map((v) => ({
			name: v.name,
			type: v.type ?? '',
			def: toRow(v.default),
			description: v.description ?? '',
			sensitive: !!v.sensitive
		}));
		locals = (config.locals ?? []).map((l) => ({ name: l.name, value: toRow(l.value) }));
		outputs = (config.outputs ?? []).map((o) => ({
			name: o.name,
			value: toRow(o.value),
			description: o.description ?? '',
			sensitive: !!o.sensitive
		}));
	});

	function save() {
		const config: ProjectConfig = {
			variables: vars
				.filter((v) => v.name.trim())
				.map((v) => ({
					name: v.name.trim(),
					type: v.type.trim() || undefined,
					default: rowToAttr(v.def),
					description: v.description.trim() || undefined,
					sensitive: v.sensitive || undefined
				})),
			locals: locals
				.filter((l) => l.name.trim() && rowFilled(l.value))
				.map((l) => ({ name: l.name.trim(), value: rowToAttr(l.value)! })),
			outputs: outputs
				.filter((o) => o.name.trim() && rowFilled(o.value))
				.map((o) => ({
					name: o.name.trim(),
					value: rowToAttr(o.value)!,
					description: o.description.trim() || undefined,
					sensitive: o.sensitive || undefined
				}))
		};
		onSave(config);
	}

	const inp =
		'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2 py-1 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
	const mono = 'mono ' + inp;
	const addBtn =
		'inline-flex items-center gap-1 rounded-md border border-[var(--al-border)] px-2 py-1 text-xs text-[var(--al-muted)] hover:text-[var(--al-text)]';
	const del = 'shrink-0 rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-danger)]';
	const label = 'mb-1.5 text-xs font-semibold uppercase tracking-wide text-[var(--al-muted)]';
	const btn = 'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-40';
</script>

<!--
  valueEditor — one row's value, in whichever representation it is currently in.
  Structured mode drops onto its own line: a nested object needs the full width,
  and it grows vertically, so it cannot share the flex row with the name/type.
-->
{#snippet valueEditor(row: ValueRow, placeholder: string)}
	{#if row.structured}
		<div class="min-w-0 flex-1">
			<DynamicEditor bind:value={row.struct} {references} {secrets} {onCreateSecret} />
		</div>
	{:else}
		<input class={mono + ' min-w-0 flex-1'} {placeholder} bind:value={row.text} />
	{/if}
{/snippet}

{#snippet structToggle(row: ValueRow)}
	<button
		class="shrink-0 rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-accent)]"
		onclick={() => toggleStructured(row)}
		title={row.structured
			? 'Edit as raw HCL text'
			: 'Edit as a structure (object / list) — for map or object values such as a for_each collection'}
		aria-label={row.structured ? 'Edit as raw HCL' : 'Edit as a structure'}
	>
		{#if row.structured}<Code2 size={13} />{:else}<Braces size={13} />{/if}
	</button>
{/snippet}

<Modal {title} bind:open>
	<div class="space-y-5">
		<!-- Variables -->
		<section>
			<div class={label}>Variables</div>
			<div class="space-y-1.5">
				{#each vars as v, i (i)}
					<div class="flex items-start gap-1.5">
						<input class={mono + ' mt-0 w-28 shrink-0'} placeholder="name" bind:value={v.name} />
						<input class={mono + ' w-28 shrink-0'} placeholder="type" bind:value={v.type} title={'HCL type, e.g. string or map(object({ name = string }))'} />
						{@render valueEditor(v.def, 'default (HCL)')}
						{@render structToggle(v.def)}
						<label class="flex shrink-0 items-center gap-1 pt-1.5 text-[10px] text-[var(--al-muted)]" title="sensitive">
							<input type="checkbox" class="h-3 w-3 accent-[var(--al-accent)]" bind:checked={v.sensitive} /> sens
						</label>
						<button class={del} onclick={() => (vars = vars.filter((_, j) => j !== i))} aria-label="Remove variable"><Trash2 size={14} /></button>
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (vars = [...vars, { name: '', type: 'string', def: toRow(undefined), description: '', sensitive: false }])}><Plus size={12} /> Add variable</button>
		</section>

		<!-- Locals -->
		<section>
			<div class={label}>Locals</div>
			<div class="space-y-1.5">
				{#each locals as l, i (i)}
					<div class="flex items-start gap-1.5">
						<input class={mono + ' w-28 shrink-0'} placeholder="name" bind:value={l.name} />
						{@render valueEditor(l.value, 'value (HCL, e.g. "prod" or random_pet.x.id)')}
						{@render structToggle(l.value)}
						<button class={del} onclick={() => (locals = locals.filter((_, j) => j !== i))} aria-label="Remove local"><Trash2 size={14} /></button>
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (locals = [...locals, { name: '', value: toRow(undefined) }])}><Plus size={12} /> Add local</button>
		</section>

		<!-- Outputs -->
		<section>
			<div class={label}>Outputs</div>
			<div class="space-y-1.5">
				{#each outputs as o, i (i)}
					<div class="flex items-start gap-1.5">
						<input class={mono + ' w-28 shrink-0'} placeholder="name" bind:value={o.name} />
						{@render valueEditor(o.value, 'value (HCL)')}
						{@render structToggle(o.value)}
						<label class="flex shrink-0 items-center gap-1 pt-1.5 text-[10px] text-[var(--al-muted)]" title="sensitive">
							<input type="checkbox" class="h-3 w-3 accent-[var(--al-accent)]" bind:checked={o.sensitive} /> sens
						</label>
						<button class={del} onclick={() => (outputs = outputs.filter((_, j) => j !== i))} aria-label="Remove output"><Trash2 size={14} /></button>
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (outputs = [...outputs, { name: '', value: toRow(undefined), description: '', sensitive: false }])}><Plus size={12} /> Add output</button>
		</section>
	</div>

	{#snippet footer()}
		<button class={btn + ' border border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]'} onclick={() => (open = false)}>Cancel</button>
		<button class={btn + ' bg-[var(--al-accent)] text-white hover:brightness-110'} onclick={save} disabled={saving}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	{/snippet}
</Modal>
