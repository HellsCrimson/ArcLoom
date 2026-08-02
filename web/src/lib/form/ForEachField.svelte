<!--
  ForEachField — the **Instances** control: how many copies of this block the
  config describes, and (for a for_each) over what.

  The two meta-args are mutually exclusive, so the mode selector makes that
  structural — *Single* / *count* / *for_each* — instead of rendering both boxes
  and greying one out. Which one is active is DERIVED from the text, like every
  other mode here: there is nowhere to store it (the model has one raw string
  per meta-arg) and deriving it is what makes a hand-written config editable
  after an import. Switching modes stashes the text it leaves behind, so a
  mistaken click is not a retype.

  count is a number input for the overwhelmingly common literal case, with the
  **ƒx** escape to a raw expression (`var.replicas`, `length(var.subnets)`) —
  the idiom FieldRenderer already uses for primitive collection elements.

  for_each offers three ways to produce its string:

    Variable/local  pick a declared collection              -> `var.x`
    Build           edit entries with the structural editor -> `{ a = { … } }`
    Expression      type raw HCL                            -> anything

  Build mode round-trips through parseHclCollection/toHclText. A collection the
  parser cannot read structurally (a comprehension, a function call) refuses to
  switch rather than mangling it, which is the same rule expand.ts follows.

  The component is keyed by the block being edited at both call sites: `mode`,
  `draft` and the stash are seeded once per mount, and without the key a form
  switched from one resource to another would keep the previous block's Build
  draft — and project it into the new block's for_each.
-->
<script lang="ts">
	import type { AttrValue } from '$lib/types';
	import type { Scope } from '$lib/model/expand';
	import DynamicEditor from '$lib/form/DynamicEditor.svelte';
	import { parseHclCollection, toHclText } from '$lib/model/hcltext';
	import { refParts, stripWrappers } from '$lib/model/expand';
	import { describeForEach } from '$lib/model/foreach';
	import { Braces, Code2, Copy, Hash, List, Square, Wand2 } from 'lucide-svelte';

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

	/** Which meta-arg (if any) this block uses. */
	type Instances = 'single' | 'count' | 'for_each';
	/** How the for_each string is being authored. */
	type Source = 'pick' | 'build' | 'expr';

	const uid = $props.id();

	// An EMPTY meta-arg opens on the picker, never on Build: Build projects its
	// draft into the text, so opening there on a block that has no for_each would
	// write `{}` into it — an empty collection means "create no instances", so a
	// plain resource would silently stop existing.
	function detectSource(text: string): Source {
		const t = text.trim();
		if (!t) return 'pick';
		// `toset(var.x)` is still a pick — the wrapper is how a list is made
		// acceptable to for_each, not a different way of choosing the collection.
		if (refParts(stripWrappers(t))) return 'pick';
		return parseHclCollection(t) ? 'build' : 'expr';
	}

	function detectInstances(): Instances {
		if (count.trim()) return 'count';
		if (forEach.trim()) return 'for_each';
		return 'single';
	}

	let instances = $state<Instances>(detectInstances());
	let mode = $state<Source>(detectSource(forEach));
	// The structural draft lives here; the text stays the single source of truth
	// on the model, rewritten on every edit.
	let draft = $state<AttrValue>(parseHclCollection(forEach) ?? { mode: 'literal', entries: [] });
	// What each meta-arg held when the user last left its mode, so switching back
	// and forth inside one editing session is lossless. It is deliberately NOT
	// persisted: only the text that is live when Save runs reaches the model.
	let stash = $state({ count: '', forEach: '' });
	let switchError = $state('');
	// ƒx pinned open on the count input. Derived from the text as well, so an
	// imported `count = var.n` opens on the expression box; the flag only keeps
	// it open after the box is cleared.
	let countFx = $state(false);

	const isNumeric = (t: string) => /^\d+$/.test(t.trim());
	const countExpr = $derived(countFx || (count.trim() !== '' && !isNumeric(count)));

	function toggleCountFx() {
		if (!countExpr) {
			countFx = true;
			return;
		}
		// Going back to the number input over an expression would show a numeric
		// box holding `var.n`; drop the text it cannot represent.
		if (!isNumeric(count)) count = '';
		countFx = false;
	}

	function setInstances(next: Instances) {
		if (next === instances || disabled) return;
		switchError = '';
		if (instances === 'count') stash.count = count;
		if (instances === 'for_each') stash.forEach = forEach;
		count = '';
		forEach = '';
		if (next === 'count') {
			// A mode that produced no meta-arg would be a selector that lies: an
			// empty box reads as "count" while the block is created once. 1 is the
			// truthful seed — it changes the addressing (x.y becomes x.y[0]), which
			// the panel below spells out, not the number of instances.
			count = stash.count || '1';
		} else if (next === 'for_each') {
			forEach = stash.forEach;
			mode = detectSource(forEach);
			draft = parseHclCollection(forEach) ?? { mode: 'literal', entries: [] };
		}
		instances = next;
	}

	function setMode(next: Source) {
		switchError = '';
		if (next === 'build') {
			const parsed = parseHclCollection(forEach);
			if (!parsed && forEach.trim()) {
				// Refuse rather than overwrite: the value in the box may be a
				// reference or a comprehension the user wants to keep, and Build
				// cannot represent either. Say how to get there instead —
				// through the wrappers, so `toset(var.x)` is named for what it is
				// rather than reported as "a function".
				switchError = refParts(stripWrappers(forEach.trim()))
					? `${forEach.trim()} is a reference — clear the field to build a collection here instead.`
					: 'Not a plain collection (a function or comprehension) — clear the field to build one here.';
				return;
			}
			draft = parsed ?? { mode: 'literal', entries: [] };
		}
		if (next === 'pick' && !refParts(forEach.trim())) forEach = '';
		mode = next;
	}

	// Collections a for_each can iterate: every declared variable and local. The
	// list is not filtered by type — a variable with no declared type is still a
	// perfectly good map, and guessing would hide it.
	const candidates = $derived([
		...(scope.variables ?? []).map((v) => ({ addr: `var.${v.name}`, type: v.type ?? '' })),
		...(scope.locals ?? []).map((l) => ({ addr: `local.${l.name}`, type: '' }))
	]);

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
	// the two modes, and writes `forEach`, which it never reads back, so there is
	// no loop. The guards are what keep it from overwriting a hand-typed
	// expression, or from re-filling a for_each the mode selector just cleared.
	$effect(() => {
		if (instances !== 'for_each' || mode !== 'build') return;
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
	const on = 'bg-[var(--al-accent-weak)] text-[var(--al-text)]';
	const off = 'text-[var(--al-muted)] hover:text-[var(--al-text)]';
</script>

<div class="min-w-0">
	<div class="mb-1 flex flex-wrap items-center gap-2">
		<span class="text-xs text-[var(--al-muted)]" title="How many instances of this block the configuration describes">Instances</span>
		<div class="inline-flex overflow-hidden rounded-md border border-[var(--al-border)]">
			<button
				class="{tab} {instances === 'single' ? on : off}"
				onclick={() => setInstances('single')}
				{disabled}
				title="One instance — no count, no for_each"
			><Square size={11} /> Single</button>
			<button
				class="{tab} {instances === 'count' ? on : off}"
				onclick={() => setInstances('count')}
				{disabled}
				title="count — N identical instances, addressed [0], [1], …"
			><Hash size={11} /> count</button>
			<button
				class="{tab} {instances === 'for_each' ? on : off}"
				onclick={() => setInstances('for_each')}
				{disabled}
				title="for_each — one instance per member of a map or set"
			><Copy size={11} /> for_each</button>
		</div>
	</div>

	{#if instances === 'single'}
		<p class="text-[11px] text-[var(--al-muted)]">
			Created once, addressed without an index. Pick <span class="mono">count</span> or
			<span class="mono">for_each</span> above to multiply it.
		</p>
	{:else if instances === 'count'}
		<div class="flex flex-wrap items-center gap-2">
			{#if countExpr}
				<input
					id="{uid}-count"
					class="{inp} w-full max-w-xs"
					bind:value={count}
					placeholder="var.replicas · length(var.subnets)"
					{disabled}
					aria-label="count expression"
				/>
			{:else}
				<input
					id="{uid}-count"
					type="number"
					min="0"
					class="{inp} w-24"
					value={count}
					oninput={(e) => (count = (e.currentTarget as HTMLInputElement).value)}
					{disabled}
					aria-label="count"
				/>
				<span class="text-[11px] text-[var(--al-muted)]">instance{count.trim() === '1' ? '' : 's'}</span>
			{/if}
			<button
				class="rounded border border-[var(--al-border)] px-1.5 py-0.5 text-[11px] {countExpr ? on : off}"
				onclick={toggleCountFx}
				{disabled}
				title={countExpr ? 'Back to a plain number' : 'Write an expression instead — var.replicas, length(var.subnets)'}
			>ƒx</button>
		</div>
		{#if count.trim() === ''}
			<!-- The selector would otherwise claim `count` while an empty box
			     generates no meta-arg at all. -->
			<p class="mt-1 text-[11px] text-[var(--al-muted)]">Empty — the block is created once, as if this were Single.</p>
		{/if}
	{:else}
		<div class="mb-1 inline-flex overflow-hidden rounded-md border border-[var(--al-border)]">
			<button
				class="{tab} {mode === 'pick' ? on : off}"
				onclick={() => setMode('pick')}
				{disabled}
				title="Iterate a variable or local declared in this scope"
			><List size={11} /> Variable</button>
			<button
				class="{tab} {mode === 'build' ? on : off}"
				onclick={() => setMode('build')}
				{disabled}
				title="Build the collection here — rendered to HCL for you"
			><Braces size={11} /> Build</button>
			<button
				class="{tab} {mode === 'expr' ? on : off}"
				onclick={() => setMode('expr')}
				{disabled}
				title="Write the expression yourself"
			><Code2 size={11} /> Expression</button>
		</div>

		{#if mode === 'pick'}
			<select
				class="{inp} w-full"
				value={pickValue}
				onchange={(e) => pick((e.currentTarget as HTMLSelectElement).value)}
				{disabled}
				aria-label="for_each collection"
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
		{:else if mode === 'build'}
			<div class="rounded-md border border-[var(--al-border)] p-2">
				<DynamicEditor bind:value={draft} />
				<p class="mt-1 text-[11px] text-[var(--al-muted)]">
					Written into the config as <span class="mono">{forEach.trim() ? forEach.replace(/\s+/g, ' ').slice(0, 90) : '(empty)'}</span>
				</p>
			</div>
		{:else}
			<input class="{inp} w-full" bind:value={forEach} placeholder={'toset(["a", "b"]) · var.map · { for … }'} {disabled} aria-label="for_each expression" />
		{/if}
		{#if needsToset}
			<div class="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-[var(--al-warn)]">
				<span>
					This is a list (a <span class="mono">tuple</span> in HCL) — <span class="mono">for_each</span> takes a map or a set of strings.
				</span>
				<button
					class="inline-flex items-center gap-1 rounded border border-[var(--al-warn)] px-1.5 py-0.5 text-[var(--al-text)] hover:bg-[var(--al-panel-2)]"
					onclick={wrapToset}
					{disabled}
				><Wand2 size={11} /> Wrap in toset()</button>
			</div>
		{/if}
		{#if switchError}
			<p class="mt-1 text-[11px] text-[var(--al-warn)]">{switchError}</p>
		{/if}
	{/if}
</div>
