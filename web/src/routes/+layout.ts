// ArcLoom is a single-page app served by the Go binary. Disable SSR and
// prerendering so every route resolves client-side (Go serves 200.html).
export const ssr = false;
export const prerender = false;
