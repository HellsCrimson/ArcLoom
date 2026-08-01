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
	import { refParts, stripWrappers } from '$lib/model/expand';
	import { describeForEach } from '$lib/model/foreach';
	import { Braces, Code2, List, Wand2 } from 'lucide-svelte';

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

	// An EMPTY meta-arg opens on the picker, never on Build: Build projects its
	// draft into the text, so opening there on a block that has no for_each would
	// write `{}` into it — an empty collection means "create no instances", so a
	// plain resource would silently stop existing.
	function detect(text: string): Mode {
		const t = text.trim();
		if (!t) return 'pick';
		// `toset(var.x)` is still a pick — the wrapper is how a list is made
		// acceptable to for_each, not a different way of choosing the collection.
		if (refParts(stripWrappers(t))) return 'pick';
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

	// for_each takes a map or a SET; a `[...]` list is a tuple in HCL and the
	// engine rejects it outright. describeForEach knows which shape the chosen
	// collection has, so the wrapper can be offered exactly when it is needed
	// instead of being learned from a failed plan.
	const info = $derived(describeForEach(forEach, scope));
	const needsToset = $derived(info?.needsToset ?? false);

	function wrapToset() {
		const t = forEach.trim();
		if (t) forEach = `toset(${t})`;
	}

	// The picker writes the wrapper itself: for a list-shaped variable or local
	// the bare reference cannot work, so offering it unwrapped would only be a
	// step towards an error. `pickValue` keeps the select in sync with a stored
	// `toset(...)`.
	const pickValue = $derived(stripWrappers(forEach.trim()));
	function pick(addr: string) {
		if (!addr) {
			forEach = '';
			return;
		}
		forEach = addr;
		// Re-read the shape now that the expression names the collection.
		const d = describeForEach(addr, scope);
		if (d?.needsToset) forEach = `toset(${addr})`;
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
		if (text === forEach) return;
		// Emptying a collection the block HAD is a real edit (`{}` = no
		// instances); an untouched empty editor on a block that never had one is
		// not, and must not fabricate a for_each.
		if ((text === '{}' || text === '[]') && !forEach.trim()) return;
		forEach = text;
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
			<select
				class="{inp} w-full"
				value={pickValue}
				onchange={(e) => pick((e.currentTarget as HTMLSelectElement).value)}
				disabled={disabled || count.trim() !== ''}
			>
				<option value="">Select a variable or local…</option>
				{#each candidates as c (c.addr)}
					<option value={c.addr}>{c.addr}{c.type ? ` · ${c.type}` : ''}</option>
				{/each}
			</select>
			{#if info?.wrapped}
				<p class="mt-1 text-[11px] text-[var(--al-muted)]">
					Written as <span class="mono text-[var(--al-text)]">{forEach.trim()}</span> — for_each takes a map or a set,
					and a list of strings is a tuple until <span class="mono">toset()</span> converts it.
				</p>
			{/if}
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
		{#if needsToset}
			<div class="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-[var(--al-warn)]">
				<span>
					This is a list (a <span class="mono">tuple</span> in HCL) — <span class="mono">for_each</span> takes a map or a set of strings.
				</span>
				<button
					class="inline-flex items-center gap-1 rounded border border-[var(--al-warn)] px-1.5 py-0.5 text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
					onclick={wrapToset}
				><Wand2 size={11} /> Wrap in toset()</button>
			</div>
		{/if}
		{#if switchError}
			<p class="mt-1 text-[11px] text-[var(--al-warn)]">{switchError}</p>
		{/if}
	</div>
</div>
