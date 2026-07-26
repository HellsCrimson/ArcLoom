// Shared Tailwind class strings for the app shell's buttons and textareas.
// Kept in one module so the pieces split out of +page.svelte (toolbar, run
// pane, sidebar, modals) render byte-identical controls instead of each
// re-declaring the strings and drifting apart.

export const btn = 'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition disabled:opacity-40';
export const btnPrimary = `${btn} bg-[var(--al-accent)] text-white hover:brightness-110`;
export const btnGhost = `${btn} border border-[var(--al-border)] text-[var(--al-text)] hover:bg-[var(--al-panel-2)]`;
export const iconBtn = 'rounded-md p-1.5 text-[var(--al-muted)] hover:bg-[var(--al-panel-2)] hover:text-[var(--al-text)]';
export const hdrBtn = 'inline-flex items-center gap-1.5 rounded-md border border-[var(--al-border)] px-2.5 py-1 text-xs font-medium text-[var(--al-text)] hover:bg-[var(--al-panel-2)] disabled:opacity-40';
export const ta = 'mono w-full rounded-md border border-[var(--al-border)] bg-[var(--al-bg)] p-2.5 text-xs text-[var(--al-text)] outline-none focus:border-[var(--al-accent)]';
