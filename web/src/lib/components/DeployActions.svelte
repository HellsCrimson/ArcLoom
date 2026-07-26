<!--
  DeployActions — the Validate / Lint / Plan / Approve / Apply / Destroy row
  rendered into HclView's `actions` snippet.

  It MUST render a bare fragment: HclView's actions container is the flex row
  (`flex flex-none flex-wrap items-center gap-1.5`), so a wrapper element would
  collapse six flex items into one and kill the narrow-panel wrapping.
  Permission arrives as data (`canApprove`); the `deploy` gate stays in +page.
-->
<script lang="ts">
	import { btn, btnPrimary, btnGhost } from '$lib/styles';
	import { Play, Loader2, Rocket, Flame, ShieldCheck, Ruler, CheckCircle2 } from 'lucide-svelte';

	let {
		busy = '',
		hasProject = false,
		approvalRequired = false,
		approvalReady = true,
		canApprove = false,
		hasPlan = false,
		onValidate,
		onLint,
		onPlan,
		onApprove,
		onApply,
		onDestroy
	}: {
		busy: string;
		hasProject: boolean;
		approvalRequired: boolean;
		approvalReady: boolean;
		canApprove: boolean;
		hasPlan: boolean;
		onValidate: () => void;
		onLint: () => void;
		onPlan: () => void;
		onApprove: () => void;
		onApply: () => void;
		onDestroy: () => void;
	} = $props();

	const running = $derived(busy === 'plan' || busy === 'validate' || busy === 'lint' || busy === 'apply' || busy === 'destroy' || busy === 'import' || busy === 'state-rm' || busy === 'state-mv');
</script>

<button class={btnGhost} title="Validate the generated configuration" onclick={onValidate} disabled={!hasProject || running}>
	{#if busy === 'validate'}<Loader2 size={14} class="animate-spin" />{:else}<ShieldCheck size={14} />{/if} Validate
</button>
<button class={btnGhost} title="Lint the generated configuration with tflint" onclick={onLint} disabled={!hasProject || running}>
	{#if busy === 'lint'}<Loader2 size={14} class="animate-spin" />{:else}<Ruler size={14} />{/if} Lint
</button>
<button class={btnGhost} onclick={onPlan} disabled={!hasProject || running}>
	{#if busy === 'plan'}<Loader2 size={14} class="animate-spin" />{:else}<Play size={14} />{/if} Plan
</button>
{#if approvalRequired && canApprove}
	<button
		class={btnGhost}
		title={approvalReady ? 'This plan is approved' : 'Approve the current plan so it can be applied'}
		onclick={onApprove}
		disabled={!hasProject || running || approvalReady || !hasPlan}
	>
		<CheckCircle2 size={14} /> {approvalReady ? 'Approved' : 'Approve'}
	</button>
{/if}
<button
	class={btnPrimary}
	onclick={onApply}
	disabled={!hasProject || running || (approvalRequired && !approvalReady)}
	title={approvalRequired && !approvalReady ? 'Apply is gated: an approved plan is required' : 'Apply the plan'}
>
	{#if busy === 'apply'}<Loader2 size={14} class="animate-spin" />{:else}<Rocket size={14} />{/if} Apply
</button>
<button
	class="{btn} border border-[var(--al-border)] text-[var(--al-danger)] hover:bg-[var(--al-danger)]/10"
	title="Destroy all deployed resources"
	onclick={onDestroy}
	disabled={!hasProject || running}
>
	{#if busy === 'destroy'}<Loader2 size={14} class="animate-spin" />{:else}<Flame size={14} />{/if}
</button>
