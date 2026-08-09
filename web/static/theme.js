// Resolve + apply the theme before first paint (no flash of the wrong theme).
// "system"/unset resolves to the OS preference; the concrete light|dark is
// stamped on <html> so the CSS needs only one light block.
//
// This lives in static/ rather than inline in app.html so the page can carry a
// script-src CSP with no 'unsafe-inline': SvelteKit hashes the bootstrap script
// it generates, but it cannot hash one written by hand in the shell. Loaded
// render-blocking from <head>, so it still runs before the first paint.
try {
	var t = localStorage.getItem('arcloom.theme');
	if (t !== 'light' && t !== 'dark') {
		t = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
	}
	document.documentElement.setAttribute('data-theme', t);
} catch (e) {}
