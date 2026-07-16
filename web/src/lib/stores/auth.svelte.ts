// auth is the app's authentication state. It is the first Svelte store in the
// codebase; everything else uses component-local runes. It talks to the
// cookie-based session API (`/api/auth/*`) — the session cookie is httpOnly, so
// JS never sees the token; we only track who is logged in.

export interface MeUser {
	username: string;
	email?: string;
	provider?: string;
}

interface MeResponse {
	authEnabled: boolean;
	authProviders: string[];
	user: MeUser | null;
}

class AuthStore {
	user = $state<MeUser | null>(null);
	authEnabled = $state(true);
	authProviders = $state<string[]>([]);
	loading = $state(true);
	error = $state('');

	// authenticated is true when the app should render: either auth is disabled
	// entirely, or a user is logged in.
	get authenticated(): boolean {
		return !this.authEnabled || this.user !== null;
	}

	private apply(d: MeResponse) {
		this.authEnabled = d.authEnabled;
		this.authProviders = d.authProviders ?? [];
		this.user = d.user;
	}

	// refresh loads the current session state (called once on mount).
	async refresh(): Promise<void> {
		this.loading = true;
		try {
			const r = await fetch('/api/auth/me', { credentials: 'same-origin' });
			if (r.ok) {
				this.apply((await r.json()) as MeResponse);
			} else {
				this.user = null;
			}
		} catch {
			this.user = null;
		} finally {
			this.loading = false;
		}
	}

	async login(username: string, password: string): Promise<boolean> {
		this.error = '';
		let r: Response;
		try {
			r = await fetch('/api/auth/login', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ username, password })
			});
		} catch {
			this.error = 'Could not reach the server';
			return false;
		}
		if (!r.ok) {
			let msg = 'Login failed';
			try {
				const b = await r.json();
				if (b?.error) msg = b.error;
			} catch {
				/* ignore */
			}
			this.error = msg;
			return false;
		}
		this.apply((await r.json()) as MeResponse);
		return true;
	}

	// changePassword rotates the current user's password. On success the server
	// revokes every session (including this one), so we clear local state and the
	// gate returns to the login screen. Returns null on success, else an error
	// message.
	async changePassword(currentPassword: string, newPassword: string): Promise<string | null> {
		let r: Response;
		try {
			r = await fetch('/api/auth/password', {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ currentPassword, newPassword })
			});
		} catch {
			return 'Could not reach the server';
		}
		if (r.status === 204) {
			this.user = null;
			return null;
		}
		let msg = 'Could not change password';
		try {
			const b = await r.json();
			if (b?.error) msg = b.error;
		} catch {
			/* ignore */
		}
		return msg;
	}

	async logout(): Promise<void> {
		try {
			await fetch('/api/auth/logout', { method: 'POST', credentials: 'same-origin' });
		} catch {
			/* ignore */
		}
		this.user = null;
	}

	// clear is called by the API client when any request returns 401 (session
	// expired/revoked) so the layout gate flips back to the login screen.
	clear(): void {
		this.user = null;
	}
}

export const auth = new AuthStore();
