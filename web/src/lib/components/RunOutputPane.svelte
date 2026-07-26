<!--
  RunOutputPane — the run-output pane at the bottom of the HCL column.

  Purely presentational: its model IS the `runPane` store (written by the five
  SSE stream drivers in +page), so nothing here is threaded as a prop except the
  measured height, the approval gate state and the two callbacks. Permission
  arrives as data — `onToggleGate` is undefined for a user without `resources`,
  which is what keeps the button / "Gated" span / nothing triple identical.
-->
<script lang="ts">
	import type { Approval } from '$lib/api/client';
	import PlanDiffList from '$lib/components/PlanDiffList.svelte';
	import { runPane } from '$lib/stores/runPane.svelte';
	import { ACTION_COLOR, ACTION_SYMBOL } from '$lib/editor/graph';
	import { Lock, LockOpen } from 'lucide-svelte';

	let { height, hasProject, approvalRequired, approval, onToggleGate, onJump }: {
		height: number;
		hasProject: boolean;
		approvalRequired: boolean;
		approval: Approval | null;
		onToggleGate?: () => void;
		onJump: (address: string) => void;
	} = $props();

	// Accent as literal hex (for inline styles that concatenate/tint).
	const accentHex = '#4c8dff';
	const accentWeak = '#1f3a5f';
</script>

<div class="flex min-h-0 flex-none flex-col border-t" style="height:{height}px;border-color:var(--al-code-border);background:var(--al-code-bg);">
	<div class="flex h-11 flex-none items-center border-b px-4" style="border-color:var(--al-code-border);">
		<div class="font-mono" style="font-size:12.5px;font-weight:600;color:var(--al-code-text);">run output</div>
		{#if runPane.result}<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:{accentWeak};color:{accentHex};">{runPane.result}</span>{/if}
		{#if runPane.cost}<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:rgba(16,185,129,0.15);color:#10b981;" title="Estimated monthly cost">${runPane.cost.totalMonthly.toFixed(2)}/mo</span>{/if}
		{#if runPane.policy && runPane.policy.available && !runPane.policy.error}
			{#if runPane.policy.failureCount > 0}
				<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:{ACTION_COLOR.delete}22;color:{ACTION_COLOR.delete};" title="Policy failures — an enforced apply is blocked">{runPane.policy.failureCount} policy failure{runPane.policy.failureCount === 1 ? '' : 's'}</span>
			{:else}
				<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:rgba(16,185,129,0.15);color:#10b981;" title="All policies passed">policy ok</span>
			{/if}
		{/if}
		{#if hasProject}
			{#if onToggleGate}
				<button
					class="ml-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold"
					style="background:var(--al-panel-2);color:var(--al-muted);"
					title={approvalRequired ? 'Apply requires an approved plan — click to disable' : 'Require an approved plan before apply'}
					onclick={onToggleGate}
				>
					{#if approvalRequired}<Lock size={11} /> Gate on{:else}<LockOpen size={11} /> Gate off{/if}
				</button>
			{:else if approvalRequired}
				<span class="ml-2 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:var(--al-panel-2);color:var(--al-muted);"><Lock size={11} /> Gated</span>
			{/if}
			{#if approvalRequired && approval && !approval.consumed}
				<span class="ml-2 rounded px-1.5 py-0.5 text-[10px] font-semibold" style="background:rgba(16,185,129,0.15);color:#10b981;" title="Approved — apply enabled">✓ approved by {approval.approvedBy}</span>
			{/if}
		{/if}
		{#if runPane.counts}
			<span class="ml-auto flex items-center gap-2 font-mono text-[11px] font-semibold">
				<span style="color:{ACTION_COLOR.create}">+{runPane.counts.add}</span>
				<span style="color:{ACTION_COLOR.update}">~{runPane.counts.change}</span>
				<span style="color:{ACTION_COLOR.replace}">±{runPane.counts.replace}</span>
				<span style="color:{ACTION_COLOR.delete}">−{runPane.counts.destroy}</span>
			</span>
		{/if}
	</div>
	{#if runPane.changes.length > 0}
		<div class="max-h-[45%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
			{#each runPane.changes as c (c.address)}
				<div class="flex items-center gap-1 px-[18px]">
					{#if (c.diffs?.length ?? 0) > 0}
						<!-- Expand the attribute diff in place, so finding out WHAT changes
						     never means scrolling the raw log below. -->
						<button
							class="flex-none font-mono text-[10px]"
							style="color:var(--al-code-comment);"
							title={runPane.expanded[c.address] ? 'Hide changed attributes' : 'Show changed attributes'}
							aria-expanded={runPane.expanded[c.address] ?? false}
							onclick={() => runPane.toggleExpanded(c.address)}
						>{runPane.expanded[c.address] ? '▾' : '▸'}</button>
					{:else}
						<span class="flex-none font-mono text-[10px]" style="color:transparent;">▸</span>
					{/if}
					<button
						class="flex min-w-0 flex-1 items-center gap-2 py-0.5 text-left font-mono hover:bg-[var(--al-panel-2)]"
						style="font-size:11.5px;line-height:1.6;"
						title="{c.address} — click to open"
						onclick={() => onJump(c.address)}
					>
						<span class="w-3 flex-none text-center font-bold" style="color:{ACTION_COLOR[c.action]}">{ACTION_SYMBOL[c.action]}</span>
						<span class="truncate" style="color:var(--al-code-text);">{c.address}</span>
						{#if (c.diffs?.length ?? 0) > 0}
							<span class="flex-none text-[10px]" style="color:var(--al-code-comment);">{c.diffs?.length} attr</span>
						{/if}
						<span class="ml-auto flex-none text-[10px]" style="color:{ACTION_COLOR[c.action]}">{c.action}</span>
					</button>
				</div>
				{#if runPane.expanded[c.address]}
					<div class="mb-1 ml-[34px] mr-[18px] border-l pl-2" style="border-color:var(--al-code-border);">
						<PlanDiffList diffs={c.diffs ?? []} truncated={c.diffsTruncated} compact />
					</div>
				{/if}
			{/each}
		</div>
	{/if}
	{#if runPane.cost}
		<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
			<div class="flex items-center gap-2 px-[18px] py-0.5">
				<span class="text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">estimated cost</span>
				<span class="ml-auto font-mono text-[11px] font-semibold" style="color:var(--al-code-text);">${runPane.cost.totalMonthly.toFixed(2)}/mo</span>
			</div>
			{#if runPane.cost.error}
				<div class="px-[18px] py-0.5 text-[10px]" style="color:var(--al-code-comment);">Infracost unavailable ({runPane.cost.error}) — using price overrides only</div>
			{/if}
			{#each runPane.cost.lines as l (l.address)}
				<div class="flex items-center gap-3 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
					<span class="truncate" style="color:var(--al-code-text);" title={l.address}>{l.address}</span>
					<span class="flex-none text-[9px] uppercase tracking-wide" style="color:var(--al-code-comment);">{l.source}</span>
					<span class="ml-auto flex-none" style="color:var(--al-code-muted);">${l.monthly.toFixed(2)}/mo</span>
				</div>
			{/each}
			{#if runPane.cost.lines.length === 0}
				<div class="px-[18px] py-0.5 text-[11px]" style="color:var(--al-code-muted);">No priced resources — add price overrides or configure Infracost.</div>
			{/if}
		</div>
	{/if}
	{#if runPane.policy}
		<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
			<div class="px-[18px] py-0.5 text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">
				policy{#if !runPane.policy.available} — conftest not installed{:else if runPane.policy.error} — check failed{:else} · {runPane.policy.failureCount} failure{runPane.policy.failureCount === 1 ? '' : 's'} · {runPane.policy.warningCount} warning{runPane.policy.warningCount === 1 ? '' : 's'}{/if}
			</div>
			{#if runPane.policy.error}
				<div class="px-[18px] py-0.5 text-[11px]" style="color:{ACTION_COLOR.delete};">{runPane.policy.error}</div>
			{/if}
			{#each runPane.policy.violations as v, i (i)}
				<div class="flex items-start gap-2 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
					<span class="w-14 flex-none text-[10px] uppercase" style="color:{v.severity === 'failure' ? ACTION_COLOR.delete : ACTION_COLOR.update};">{v.severity}</span>
					<span class="truncate" style="color:var(--al-code-text);" title={v.message}>{v.message}</span>
					{#if v.rule}<span class="ml-auto flex-none text-[10px]" style="color:var(--al-code-comment);">{v.rule}</span>{/if}
				</div>
			{/each}
			{#if runPane.policy.available && !runPane.policy.error && runPane.policy.violations.length === 0}
				<div class="px-[18px] py-0.5 text-[11px]" style="color:var(--al-code-muted);">All policies passed ({runPane.policy.successes} rule{runPane.policy.successes === 1 ? '' : 's'}).</div>
			{/if}
		</div>
	{/if}
	{#if runPane.lint}
		<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
			<div class="px-[18px] py-0.5 text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">
				lint{#if !runPane.lint.available} — tflint not installed{:else} · {runPane.lint.issues.length} issue{runPane.lint.issues.length === 1 ? '' : 's'}{/if}
			</div>
			{#each runPane.lint.issues as iss, i (i)}
				<div class="flex items-start gap-2 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
					<span class="w-14 flex-none text-[10px] uppercase" style="color:{iss.severity === 'error' ? ACTION_COLOR.delete : ACTION_COLOR.update};">{iss.severity}</span>
					<span class="truncate" style="color:var(--al-code-text);" title={iss.message}>{iss.message}</span>
					{#if iss.filename}<span class="ml-auto flex-none text-[10px]" style="color:var(--al-code-comment);">{iss.filename}:{iss.line}</span>{/if}
				</div>
			{/each}
			{#if runPane.lint.available && runPane.lint.issues.length === 0}
				<div class="px-[18px] py-0.5 text-[11px]" style="color:var(--al-code-muted);">No issues found.</div>
			{/if}
		</div>
	{/if}
	{#if runPane.outputs.length > 0}
		<div class="max-h-[40%] flex-none overflow-auto border-b py-1" style="border-color:var(--al-code-border);">
			<div class="px-[18px] py-0.5 text-[10px] font-semibold uppercase tracking-wide" style="color:var(--al-code-comment);">outputs</div>
			{#each runPane.outputs as o (o.name)}
				<div class="flex items-center gap-3 px-[18px] py-0.5 font-mono" style="font-size:11.5px;line-height:1.6;">
					<span class="flex-none" style="color:var(--al-code-text);">{o.name}</span>
					<span class="ml-auto truncate" style="color:var(--al-code-muted);" title={o.sensitive ? '' : o.value}>{o.sensitive ? '(sensitive)' : o.value}</span>
				</div>
			{/each}
		</div>
	{/if}
	<pre class="flex-1 overflow-auto whitespace-pre-wrap px-[18px] py-3 font-mono" style="font-size:11.5px;line-height:1.6;color:var(--al-code-muted);">{runPane.log.join('\n') || '// run plan or apply to see output'}</pre>
</div>
