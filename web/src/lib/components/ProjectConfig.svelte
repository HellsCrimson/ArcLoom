<script lang="ts">
	import type { AttrValue, Field, ProjectConfig } from '$lib/types';
	import Modal from './Modal.svelte';
	import DynamicEditor from '$lib/form/DynamicEditor.svelte';
	import FieldRenderer from '$lib/form/FieldRenderer.svelte';
	import { rawToDynamic, ensureLiteralShape, isContainer } from '$lib/form/defaults';
	import { parseHclType } from '$lib/model/hcltype';
	import { parseHclCollection } from '$lib/model/hcltext';
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

	// An inline collection written as RAW HCL — `{ a = "x", b = var.y }` — is
	// stored as mode:'expression', which is how a hand-written local is kept and
	// how hclimport stores any collection whose members are not all literals (one
	// `var.x` inside is enough). It IS a structure, so it belongs in the
	// structural editor; it used to fall through every test below and stay a
	// one-line text box, which is the bug this answers.
	//
	// parseHclCollection is the gate, and it refuses more than it accepts: a
	// comprehension (`{ for k, v in … }`) is brace-shaped but structurally a
	// scalar and returns null, so it keeps its text.
	function exprCollection(av?: AttrValue): AttrValue | null {
		if (!av || av.mode !== 'expression') return null;
		return parseHclCollection(av.expr ?? '');
	}

	// A value opens in the structural editor when it IS a structure — an already
	// structural AttrValue, a container literal (how hclimport stores an all-literal
	// `{ alpha = { … } }`, which used to render as one line of JSON), a secret
	// (whose text form is '' — rendering it as text silently wiped it on save), or
	// a variable whose DECLARED TYPE is a container, so a freshly typed
	// `map(object({…}))` opens ready to take entries. Scalars stay in the one-line
	// HCL box: the common `default = "prod"` should not cost a kind selector.
	function wantsStructured(av?: AttrValue, typed?: Field | null): boolean {
		if (typed && isContainer(typed) && typed.widget !== 'dynamic') return true;
		if (!av) return false;
		if (av.mode === 'secret') return true;
		if (av.entries !== undefined || av.items !== undefined) return true;
		if (exprCollection(av)) return true;
		return av.mode === 'literal' && (Array.isArray(av.literal) || isObj(av.literal));
	}

	// Build both representations up front so the per-row toggle is lossless.
	// `type` is the variable's declared HCL type, when it has one.
	function toRow(av?: AttrValue, type?: string): ValueRow {
		const typed = parseHclType(type);
		const structured = wantsStructured(av, typed);
		return {
			// The raw text is kept even when the structural editor opens, so toggling
			// back shows what was written instead of an empty box — which is what
			// makes the per-row toggle lossless for the case above. A literal object
			// has no authored text to keep, so that one still starts blank.
			text: structured && av?.mode !== 'expression' ? '' : attrText(av),
			struct: structFrom(av, typed),
			structured
		};
	}
	// With a parsed type, the shape comes from the TYPE (ensureLiteralShape walks
	// the field, so an imported object literal is lifted into the declared
	// attributes rather than only the keys that happen to be present). Without
	// one, rawToDynamic infers it from the value.
	function structFrom(av: AttrValue | undefined, typed?: Field | null): AttrValue {
		// A parseable inline collection is seeded from the PARSE, not from the
		// expression node — otherwise the structural editor opens on an opaque
		// leaf holding the whole `{ … }` as text.
		const parsed = exprCollection(av);
		const base: AttrValue = parsed
			? parsed
			: av
				? JSON.parse(JSON.stringify(av))
				: { mode: 'literal', ...(typed ? {} : { entries: [] }) };
		if (typed) {
			ensureLiteralShape(typed, base);
			return base;
		}
		if (av && av.mode === 'literal' && av.entries === undefined && av.items === undefined) {
			return rawToDynamic(av.literal);
		}
		return base;
	}
	function rowToAttr(row: ValueRow): AttrValue | undefined {
		return row.structured ? cleanAttr(row.struct) : textToAttr(row.text);
	}
	const rowFilled = (row: ValueRow) => rowToAttr(row) !== undefined;

	// Toggling keeps the other representation untouched, so a mis-click is undone
	// by clicking back rather than by retyping. Turning it ON re-seeds the shape
	// from the CURRENT type, which is what makes editing the type box and then
	// switching produce the declared attributes rather than an empty object.
	function toggleStructured(row: ValueRow, type?: string) {
		if (!row.structured) {
			const typed = parseHclType(type);
			if (typed) ensureLiteralShape(typed, row.struct);
		}
		row.structured = !row.structured;
	}

	// Load the scope's config whenever the modal opens.
	$effect(() => {
		if (!open) return;
		vars = (config.variables ?? []).map((v) => ({
			name: v.name,
			type: v.type ?? '',
			def: toRow(v.default, v.type),
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
	// A structured row grows tall; boxing it keeps the eye on which name owns the
	// tree below it once several rows are open at once.
	const rowBox = 'rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)]/30 p-2';
	// The dialog goes xl only when something structured is open — a page of plain
	// one-line rows reads better at the ordinary width.
	const anyStructured = $derived(
		vars.some((v) => v.def.structured) || locals.some((l) => l.value.structured) || outputs.some((o) => o.value.structured)
	);
	const btn = 'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-40';
</script>

<!--
  valueEditor — one row's value, in whichever representation it is currently in.
  A structured value is rendered on its OWN line (see the `structRow` snippet):
  sharing the flex row with name/type/toggles left a nested object ~330px less
  width than the dialog had, which is what pushed deep levels off the edge. Only
  the one-line HCL box stays inline.
-->
{#snippet valueEditor(row: ValueRow, placeholder: string, refs: string[] = references, type?: string)}
	{#if !row.structured}
		<input class={mono + ' min-w-0 flex-1'} {placeholder} bind:value={row.text} />
	{:else}
		<!-- Placeholder in the header row so the name/type keep their positions. -->
		<span class="min-w-0 flex-1 pt-1.5 text-[11px] text-[var(--al-muted)]">structured value ↓</span>
	{/if}
{/snippet}

{#snippet structRow(row: ValueRow, refs: string[] = references, type?: string)}
	{#if row.structured}
		{@const typed = parseHclType(type)}
		<div class="mt-1 min-w-0">
			{#if typed}
				<!-- The declared type drives the widgets: a map(object({…})) edits as
				     keyed rows whose values already carry the declared attributes,
				     instead of DynamicEditor's shape-from-the-value guess. -->
				<FieldRenderer field={typed} bind:value={row.struct} references={refs} {secrets} {onCreateSecret} />
			{:else}
				<DynamicEditor bind:value={row.struct} references={refs} {secrets} {onCreateSecret} />
			{/if}
		</div>
	{/if}
{/snippet}

{#snippet structToggle(row: ValueRow, type?: string)}
	{@const typed = parseHclType(type)}
	<button
		class="shrink-0 rounded p-1 text-[var(--al-muted)] hover:text-[var(--al-accent)]"
		onclick={() => toggleStructured(row, type)}
		title={row.structured
			? 'Edit as raw HCL text'
			: typed
				? `Edit as a structure, using the declared type (${type})`
				: 'Edit as a structure (object / list) — for map or object values such as a for_each collection'}
		aria-label={row.structured ? 'Edit as raw HCL' : 'Edit as a structure'}
	>
		{#if row.structured}<Code2 size={13} />{:else}<Braces size={13} />{/if}
	</button>
{/snippet}

<Modal {title} bind:open xl={anyStructured}>
	<div class="space-y-5">
		<!-- Variables -->
		<section>
			<div class={label}>Variables</div>
			<div class="space-y-1.5">
				{#each vars as v, i (i)}
					<div class={v.def.structured ? rowBox : ''}>
						<div class="flex items-start gap-1.5">
							<input class={mono + ' mt-0 w-28 shrink-0'} placeholder="name" bind:value={v.name} />
							<input class={mono + ' w-40 shrink-0'} placeholder="type" bind:value={v.type} title={'HCL type, e.g. string or map(object({ name = string }))'} />
							<!-- No references: a variable default must be a constant — Terraform
							     forbids referring to any other value from it. -->
							{@render valueEditor(v.def, 'default (HCL)', [], v.type)}
							{@render structToggle(v.def, v.type)}
							<label class="flex shrink-0 items-center gap-1 pt-1.5 text-[10px] text-[var(--al-muted)]" title="sensitive">
								<input type="checkbox" class="h-3 w-3 accent-[var(--al-accent)]" bind:checked={v.sensitive} /> sens
							</label>
							<button class={del} onclick={() => (vars = vars.filter((_, j) => j !== i))} aria-label="Remove variable"><Trash2 size={14} /></button>
						</div>
						{@render structRow(v.def, [], v.type)}
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (vars = [...vars, { name: '', type: 'string', def: toRow(undefined, 'string'), description: '', sensitive: false }])}><Plus size={12} /> Add variable</button>
		</section>

		<!-- Locals -->
		<section>
			<div class={label}>Locals</div>
			<div class="space-y-1.5">
				{#each locals as l, i (i)}
					<div class={l.value.structured ? rowBox : ''}>
						<div class="flex items-start gap-1.5">
							<input class={mono + ' w-28 shrink-0'} placeholder="name" bind:value={l.name} />
							{@render valueEditor(l.value, 'value (HCL, e.g. "prod" or random_pet.x.id)')}
							{@render structToggle(l.value)}
							<button class={del} onclick={() => (locals = locals.filter((_, j) => j !== i))} aria-label="Remove local"><Trash2 size={14} /></button>
						</div>
						{@render structRow(l.value)}
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
					<div class={o.value.structured ? rowBox : ''}>
						<div class="flex items-start gap-1.5">
							<input class={mono + ' w-28 shrink-0'} placeholder="name" bind:value={o.name} />
							{@render valueEditor(o.value, 'value (HCL)')}
							{@render structToggle(o.value)}
							<label class="flex shrink-0 items-center gap-1 pt-1.5 text-[10px] text-[var(--al-muted)]" title="sensitive">
								<input type="checkbox" class="h-3 w-3 accent-[var(--al-accent)]" bind:checked={o.sensitive} /> sens
							</label>
							<button class={del} onclick={() => (outputs = outputs.filter((_, j) => j !== i))} aria-label="Remove output"><Trash2 size={14} /></button>
						</div>
						{@render structRow(o.value)}
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
