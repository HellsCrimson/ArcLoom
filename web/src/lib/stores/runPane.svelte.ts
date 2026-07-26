// The run-output pane's model: the streamed log plus everything the last plan
// (or validate / lint / apply / state op) said about it.
//
// This is a store rather than component state for two reasons. It is written
// by five stream drivers and two $effects, and the invariant "any op or model
// edit invalidates the plan overlay, so the canvas badges can't lie" was
// duplicated at seven call sites — reset()/clearOverlay() make it one method.
// And `expanded` / `dismissedAddress` must outlive the DOM: the run pane sits
// in the `{:else}` of the canvas toggle, so it unmounts on every canvas visit,
// yet an expansion/dismissal is documented to survive that.
//
// SSE callbacks must dereference the store at callback time
// (`runPane.append(ev.line)`), never destructure fields into locals first —
// that captures a stale array and the log silently stops appending.

import type { CostSummary, LintSummary, PlanChange, PolicySummary, RunOutput } from '$lib/api/client';

class RunPaneStore {
	log = $state<string[]>([]);
	result = $state<string>('');
	changes = $state<PlanChange[]>([]);
	counts = $state<{ add: number; change: number; destroy: number; replace: number } | null>(null);
	cost = $state<CostSummary | null>(null);
	lint = $state<LintSummary | null>(null);
	policy = $state<PolicySummary | null>(null);
	outputs = $state<RunOutput[]>([]);
	// Which run-pane change rows have their attribute diff expanded (by address;
	// an expansion survives the next plan, so re-planning keeps the same row open).
	expanded = $state<Record<string, boolean>>({});
	// Dismissal is remembered per address (not a bare flag) so it neither leaks to
	// the next resource the user opens nor survives the next plan, which resets it.
	dismissedAddress = $state<string | null>(null);

	// Spread-reassign, never push: the log is read reactively.
	append(line: string): void {
		this.log = [...this.log, line];
	}

	// Everything a fresh run replaces. Deliberately does NOT touch
	// expanded/dismissedAddress.
	reset(): void {
		this.log = [];
		this.result = '';
		this.changes = [];
		this.counts = null;
		this.cost = null;
		this.lint = null;
		this.policy = null;
		this.outputs = [];
	}

	// Everything, including the two fields a run deliberately preserves. Called
	// once when +page mounts: this store used to be that component's own $state,
	// so it died on logout with it, and a singleton would otherwise show the
	// previous user's output to the next one signing in in the same tab.
	resetSession(): void {
		this.reset();
		this.expanded = {};
		this.dismissedAddress = null;
	}

	// Just the plan overlay the canvas and the form banner read.
	clearOverlay(): void {
		this.changes = [];
		this.counts = null;
		this.cost = null;
		this.lint = null;
		this.policy = null;
	}

	toggleExpanded(addr: string): void {
		this.expanded[addr] = !this.expanded[addr];
	}
}

export const runPane = new RunPaneStore();
