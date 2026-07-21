// Theme store: the app's second Svelte store (after auth). Persists the user's
// theme preference and reflects the *resolved* theme onto <html data-theme>. The
// FOUC-guard in app.html stamps the resolved theme before first paint; this
// store keeps it in sync at runtime and re-resolves "system" when the OS
// preference changes.
//
// data-theme is always a concrete "light"/"dark" (system resolves to the OS
// preference), so app.css declares the light palette only once.

export type ThemePref = 'system' | 'light' | 'dark';

const KEY = 'arcloom.theme';

function readPref(): ThemePref {
	if (typeof localStorage === 'undefined') return 'system';
	const v = localStorage.getItem(KEY);
	return v === 'light' || v === 'dark' ? v : 'system';
}

function systemPrefersDark(): boolean {
	return typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches;
}

class ThemeStore {
	/** The user's chosen preference (what the toggle reflects). */
	pref = $state<ThemePref>(readPref());
	/** Live OS preference, so `resolved` updates when it changes under "system". */
	private sysDark = $state(systemPrefersDark());

	/** The concrete theme in effect right now. */
	get resolved(): 'light' | 'dark' {
		return this.pref === 'system' ? (this.sysDark ? 'dark' : 'light') : this.pref;
	}

	/** Change the preference, persist it, and reflect it onto the document. */
	set(p: ThemePref) {
		this.pref = p;
		if (typeof localStorage !== 'undefined') {
			if (p === 'system') localStorage.removeItem(KEY);
			else localStorage.setItem(KEY, p);
		}
		this.apply();
	}

	private apply() {
		if (typeof document === 'undefined') return;
		document.documentElement.setAttribute('data-theme', this.resolved);
	}

	/** Sync the DOM and start watching the OS preference. Call once on app mount. */
	init() {
		this.apply();
		if (typeof matchMedia !== 'undefined') {
			matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
				this.sysDark = e.matches;
				if (this.pref === 'system') this.apply(); // re-resolve under system
			});
		}
	}
}

export const theme = new ThemeStore();
