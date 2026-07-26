// Panel geometry for the app shell's three resizable panes: the structure
// sidebar's width, the HCL column's width and the run-output pane's height.
// All three are user-dragged and remembered per browser.
//
// This is a store rather than component state because ResizeHandle reports a
// *delta*: its callback needs the live px value to add to, and `asideHeight`
// is a `bind:clientHeight` ceiling measured on the HCL column but read by the
// run pane's clamp. Nothing runs at module init — the clamps read
// window.innerWidth/innerHeight, so restore happens in init() from onMount.

const SIDEBAR_WIDTH_KEY = 'arcloom.sidebarWidth';
const HCL_WIDTH_KEY = 'arcloom.hclWidth';
const RUN_HEIGHT_KEY = 'arcloom.runHeight';
const HCL_MIN_WIDTH = 320;
const SIDEBAR_MIN_WIDTH = 180;
const RUN_MIN_HEIGHT = 88;

class PanelGeometryStore {
	sidebarWidth = $state(256); // w-64, the previous fixed width
	hclWidth = $state(416); // 26rem, the previous fixed width
	runHeight = $state(280);
	asideHeight = $state(0); // measured, so the run pane can't be dragged past it

	// Restore the dragged panel geometry before the first paint of the aside.
	init(): void {
		if (typeof localStorage === 'undefined') return;
		const savedSidebar = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
		if (savedSidebar) this.setSidebarWidth(savedSidebar);
		const savedWidth = Number(localStorage.getItem(HCL_WIDTH_KEY));
		if (savedWidth) this.setHclWidth(savedWidth);
		const savedRun = Number(localStorage.getItem(RUN_HEIGHT_KEY));
		if (savedRun) this.setRunHeight(savedRun);
	}

	setSidebarWidth(px: number): void {
		const max = Math.max(SIDEBAR_MIN_WIDTH, window.innerWidth - 640);
		this.sidebarWidth = Math.round(Math.min(Math.max(px, SIDEBAR_MIN_WIDTH), max));
	}

	setHclWidth(px: number): void {
		const max = Math.max(HCL_MIN_WIDTH, window.innerWidth - 520);
		this.hclWidth = Math.round(Math.min(Math.max(px, HCL_MIN_WIDTH), max));
	}

	setRunHeight(px: number): void {
		const max = Math.max(RUN_MIN_HEIGHT, (this.asideHeight || window.innerHeight) - 160);
		this.runHeight = Math.round(Math.min(Math.max(px, RUN_MIN_HEIGHT), max));
	}

	persistSidebar(): void {
		localStorage.setItem(SIDEBAR_WIDTH_KEY, String(this.sidebarWidth));
	}
	persistHcl(): void {
		localStorage.setItem(HCL_WIDTH_KEY, String(this.hclWidth));
	}
	persistRun(): void {
		localStorage.setItem(RUN_HEIGHT_KEY, String(this.runHeight));
	}
}

export const panels = new PanelGeometryStore();
