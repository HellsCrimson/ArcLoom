import adapter from '@sveltejs/adapter-static';
import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// SPA served by the Go binary: emit static assets with a 200.html
			// fallback that Go serves for any unmatched client route.
			adapter: adapter({ fallback: '200.html' }),
			// script-src is set HERE and not in the Go middleware because only the
			// build knows the hash of the bootstrap script SvelteKit inlines into
			// the shell — it changes with the output, so a hash hand-copied into Go
			// would white-screen the app on the next build. SvelteKit emits these
			// directives as a <meta> tag; the Go headers carry the half that has
			// nothing to do with page content (frame-ancestors, base-uri,
			// object-src, form-action — see internal/api/headers.go). Both policies
			// apply, and a browser enforces the stricter of the two.
			//
			// style-src keeps 'unsafe-inline': Svelte writes style="" attributes
			// (the shell's own <div style="display: contents"> among them), and
			// those are governed by style-src in browsers that do not implement
			// style-src-attr. Scripts are the half that matters for XSS.
			csp: {
				mode: 'hash',
				directives: {
					'script-src': ['self'],
					'style-src': ['self', 'unsafe-inline']
				}
			}
		})
	],
	server: {
		// In dev, proxy API + SSE calls to the Go backend.
		proxy: {
			'/api': {
				target: 'http://localhost:8080',
				changeOrigin: true
			}
		}
	}
});
