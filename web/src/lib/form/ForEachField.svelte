<!--
  ForEachField — the `count` / `for_each` meta-arg inputs, plus a way to AUTHOR
  the collection instead of typing HCL into a one-line box.

  The model stores a raw HCL string (the engine evaluates it, so it cannot be
  anything else). This offers three ways to produce that string:

    Variable/local  pick a declared collection            -> `var.x`
    Build           edit entries with the structural editor -> `{ a = { … } }`
    Expression      type raw HCL                            -> anything

  The mode is DERIVED from the text on open (a `var.x` opens as a pick, a `{…}`
  literal as a build, everything else as an expression) rather than stored:
  there is nowhere to store it — the model has one string — and deriving it is
  what makes a hand-written config editable here after an import.

  Build mode round-trips through parseHclCollection/toHclText. A collection the
  parser cannot read structurally (a comprehension, a function call) refuses to
  switch rather than mangling it, which is the same rule expand.ts follows.
-->
<script lang="ts">
	import type { AttrValue } from '$lib/types';
	import type { Scope } from '$lib/model/expand';
	import DynamicEditor from '$lib/form/DynamicEditor.svelte';
	import { parseHclCollection, toHclText } from '$lib/model/hcltext';
	import { refParts } from '$lib/model/expand';
	import { Braces, Code2, List } from 'lucide-svelte';

	let {
		count = $bindable(''),
		forEach = $bindable(''),
		scope,
		disabled = false
	}: {
		count?: string;
		forEach?: string;
		scope: Scope;
		disabled?: boolean;
	} = $props();

	type Mode = 'pick' | 'build' | 'expr';

	// Collections a for_each can iterate: every declared variable and local. The
	// list is not filtered by type — a variable with no declared type is still a
	// perfectly good map, and guessing would hide it.
	const candidates = $derived([
		...(scope.variables ?? []).map((v) => ({ addr: `var.${v.name}`, type: v.type ?? '' })),
		...(scope.locals ?? []).map((l) => ({ addr: `local.${l.name}`, type: '' }))
	]);

	function detect(text: string): Mode {
		const t = text.trim();
		if (!t) return candidates.length ? 'pick' : 'build';
		if (refParts(t)) return 'pick';
		return parseHclCollection(t) ? 'build' : 'expr';
	}

	// Seeded once per mount from the value the form opened with. The form branch
	// is re-created when the edited resource changes, so this re-derives then.
	let mode = $state<Mode>(detect(forEach));
	// The structural draft lives here; the text stays the single source of truth
	// on the model, rewritten on every edit.
	let draft = $state<AttrValue>(parseHclCollection(forEach) ?? { mode: 'literal', entries: [] });
	let switchError = $state('');

	function setMode(next: Mode) {
		switchError = '';
		if (next === 'build') {
			const parsed = parseHclCollection(forEach);
			if (!parsed && forEach.trim()) {
				// Refuse rather than overwrite: the value in the box may be a
				// reference or a comprehension the user wants to keep, and Build
				// cannot represent either. Say how to get there instead.
				switchError = refParts(forEach.trim())
					? `${forEach.trim()} is a reference — clear the field to build a collection here instead.`
					: 'Not a plain collection (a function or comprehension) — clear the field to build one here.';
				return;
			}
			draft = parsed ?? { mode: 'literal', entries: [] };
		}
		if (next === 'pick' && !refParts(forEach.trim())) forEach = '';
		mode = next;
	}

	// Build mode projects the draft onto the text on every edit, so the HCL
	// preview, the instance list and the save all see what the editor shows.
	// One direction only — the effect reads `draft` (deeply, via toHclText) and
	// `mode`, and writes `forEach`, which it never reads back, so there is no
	// loop. The mode guard is what keeps it from overwriting a hand-typed
	// expression while the user is in another mode.
	$effect(() => {
		if (mode !== 'build') return;
		const text = toHclText(draft);
		if (text !== forEach) forEach = text;
	});

	const inp =
		'mono rounded-md border border-[var(--al-border)] bg-[var(--al-panel-2)] px-2.5 py-1.5 text-sm outline-none focus:border-[var(--al-accent)] disabled:opacity-40';
	const tab = 'inline-flex items-center gap-1 px-2 py-0.5 text-[11px]';
</script>

<div class="flex items-end gap-3">
	<div>
		<label for="meta-count" class="mb-1 block text-xs text-[var(--al-muted)]" title="count meta-argument — an HCL expression, e.g. 3 or var.replicas">count</label>
		<input id="meta-count" class="{inp} w-28" bind:value={count} placeholder="3 · var.n" disabled={disabled || forEach.trim() !== ''} />
	</div>
	<div class="min-w-0 flex-1">
		<div class="mb-1 flex flex-wrap items-center gap-2">
			<span class="text-xs text-[var(--al-muted)]" title="for_each meta-argument — a set or map; one instance per member">for_each</span>
			{#if !disabled && count.trim() === ''}
				<div class="inline-flex overflow-hidden rounded-md border border-[var(--al-border)]">
					<button
						class="{tab} {mode === 'pick' ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}"
						onclick={() => setMode('pick')}
						title="Iterate a variable or local declared in this scope"
					><List size={11} /> Variable</button>
					<button
						class="{tab} {mode === 'build' ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}"
						onclick={() => setMode('build')}
						title="Build the collection here — rendered to HCL for you"
					><Braces size={11} /> Build</button>
					<button
						class="{tab} {mode === 'expr' ? 'bg-[var(--al-accent-weak)] text-[var(--al-text)]' : 'text-[var(--al-muted)] hover:text-[var(--al-text)]'}"
						onclick={() => setMode('expr')}
						title="Write the expression yourself"
					><Code2 size={11} /> Expression</button>
				</div>
			{/if}
		</div>

		{#if mode === 'pick' && count.trim() === ''}
			<select class="{inp} w-full" bind:value={forEach} disabled={disabled || count.trim() !== ''}>
				<option value="">Select a variable or local…</option>
				{#each candidates as c (c.addr)}
					<option value={c.addr}>{c.addr}{c.type ? ` · ${c.type}` : ''}</option>
				{/each}
			</select>
			{#if candidates.length === 0}
				<p class="mt-1 text-[11px] text-[var(--al-muted)]">
					No variables or locals in this scope yet — declare one in Variables, or switch to Build.
				</p>
			{/if}
		{:else if mode === 'build' && count.trim() === ''}
			<div class="rounded-md border border-[var(--al-border)] p-2">
				<DynamicEditor bind:value={draft} />
				<p class="mt-1 text-[11px] text-[var(--al-muted)]">
					Written into the config as <span class="mono">{forEach.trim() ? forEach.replace(/\s+/g, ' ').slice(0, 90) : '(empty)'}</span>
				</p>
			</div>
		{:else}
			<input class="{inp} w-full" bind:value={forEach} placeholder={'toset(["a", "b"]) · var.map · { for … }'} disabled={disabled || count.trim() !== ''} />
		{/if}
		{#if switchError}
			<p class="mt-1 text-[11px] text-[var(--al-warn)]">{switchError}</p>
		{/if}
	</div>
</div>
