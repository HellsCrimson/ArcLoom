<script lang="ts">
	import type { AttrValue, Project, ProjectConfig } from '$lib/types';
	import Modal from './Modal.svelte';
	import { Plus, Trash2, Loader2 } from 'lucide-svelte';

	let {
		project,
		open = $bindable(false),
		saving = false,
		onSave
	}: {
		project: Project;
		open: boolean;
		saving?: boolean;
		onSave: (config: ProjectConfig) => void;
	} = $props();

	type VarRow = { name: string; type: string; def: string; description: string; sensitive: boolean };
	type LocalRow = { name: string; value: string };
	type OutRow = { name: string; value: string; description: string; sensitive: boolean };

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

	// Load the project's config whenever the modal opens.
	$effect(() => {
		if (!open) return;
		vars = (project.variables ?? []).map((v) => ({
			name: v.name,
			type: v.type ?? '',
			def: attrText(v.default),
			description: v.description ?? '',
			sensitive: !!v.sensitive
		}));
		locals = (project.locals ?? []).map((l) => ({ name: l.name, value: attrText(l.value) }));
		outputs = (project.outputs ?? []).map((o) => ({
			name: o.name,
			value: attrText(o.value),
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
					default: textToAttr(v.def),
					description: v.description.trim() || undefined,
					sensitive: v.sensitive || undefined
				})),
			locals: locals
				.filter((l) => l.name.trim() && l.value.trim())
				.map((l) => ({ name: l.name.trim(), value: textToAttr(l.value)! })),
			outputs: outputs
				.filter((o) => o.name.trim() && o.value.trim())
				.map((o) => ({
					name: o.name.trim(),
					value: textToAttr(o.value)!,
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

<Modal title="Variables, locals & outputs" bind:open>
	<div class="space-y-5">
		<!-- Variables -->
		<section>
			<div class={label}>Variables</div>
			<div class="space-y-1.5">
				{#each vars as v, i (i)}
					<div class="flex items-center gap-1.5">
						<input class={mono + ' w-28'} placeholder="name" bind:value={v.name} />
						<input class={mono + ' w-24'} placeholder="type" bind:value={v.type} />
						<input class={mono + ' flex-1'} placeholder="default (HCL)" bind:value={v.def} />
						<label class="flex items-center gap-1 text-[10px] text-[var(--al-muted)]" title="sensitive">
							<input type="checkbox" class="h-3 w-3 accent-[var(--al-accent)]" bind:checked={v.sensitive} /> sens
						</label>
						<button class={del} onclick={() => (vars = vars.filter((_, j) => j !== i))} aria-label="Remove variable"><Trash2 size={14} /></button>
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (vars = [...vars, { name: '', type: 'string', def: '', description: '', sensitive: false }])}><Plus size={12} /> Add variable</button>
		</section>

		<!-- Locals -->
		<section>
			<div class={label}>Locals</div>
			<div class="space-y-1.5">
				{#each locals as l, i (i)}
					<div class="flex items-center gap-1.5">
						<input class={mono + ' w-28'} placeholder="name" bind:value={l.name} />
						<input class={mono + ' flex-1'} placeholder="value (HCL, e.g. &quot;prod&quot; or random_pet.x.id)" bind:value={l.value} />
						<button class={del} onclick={() => (locals = locals.filter((_, j) => j !== i))} aria-label="Remove local"><Trash2 size={14} /></button>
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (locals = [...locals, { name: '', value: '' }])}><Plus size={12} /> Add local</button>
		</section>

		<!-- Outputs -->
		<section>
			<div class={label}>Outputs</div>
			<div class="space-y-1.5">
				{#each outputs as o, i (i)}
					<div class="flex items-center gap-1.5">
						<input class={mono + ' w-28'} placeholder="name" bind:value={o.name} />
						<input class={mono + ' flex-1'} placeholder="value (HCL)" bind:value={o.value} />
						<label class="flex items-center gap-1 text-[10px] text-[var(--al-muted)]" title="sensitive">
							<input type="checkbox" class="h-3 w-3 accent-[var(--al-accent)]" bind:checked={o.sensitive} /> sens
						</label>
						<button class={del} onclick={() => (outputs = outputs.filter((_, j) => j !== i))} aria-label="Remove output"><Trash2 size={14} /></button>
					</div>
				{/each}
			</div>
			<button class={addBtn + ' mt-1.5'} onclick={() => (outputs = [...outputs, { name: '', value: '', description: '', sensitive: false }])}><Plus size={12} /> Add output</button>
		</section>
	</div>

	{#snippet footer()}
		<button class={btn + ' border border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]'} onclick={() => (open = false)}>Cancel</button>
		<button class={btn + ' bg-[var(--al-accent)] text-white hover:brightness-110'} onclick={save} disabled={saving}>
			{#if saving}<Loader2 size={14} class="animate-spin" />{/if} Save
		</button>
	{/snippet}
</Modal>
