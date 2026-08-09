package api

import "net/http"

// securityHeaders sets the response headers a browser needs in order to enforce
// anything on ArcLoom's behalf. It runs on the ROOT router, so it covers the SPA
// shell, the static assets, the API and the OAuth discovery documents alike —
// there is no page or response worth leaving out, and a per-group version would
// be one more thing to remember when a route is added.
//
// The sharpest of these is frame protection, because of the OAuth consent page
// (/oauth/authorize). That page is an ordinary SPA route whose whole job is
// granting a credential to an MCP client, and its Approve button issues a
// same-origin POST — so inside an attacker's <iframe> the click passes
// csrfGuard exactly as a real one would. Framing is the missing control there,
// not CSRF. X-Frame-Options and frame-ancestors are both sent because they are
// not quite the same: the header is honoured by older browsers, the directive is
// the one that is actually specified, and neither is costly.
//
// The CSP here deliberately carries NO script-src or default-src. Those govern
// the SPA's own inline bootstrap and are set from svelte.config.js, which can
// hash the scripts it generates at build time; a hand-maintained script-src in
// Go would silently stop matching on the next Vite build and white-screen the
// app. What is here is the half that has nothing to do with page content, so it
// cannot rot:
//
//   - frame-ancestors 'none' — no framing at all (see above)
//   - base-uri 'none'        — an injected <base> can otherwise repoint every
//     relative URL on the page, including the API calls
//   - object-src 'none'      — no plugin content, ever
//   - form-action 'self'     — a form cannot post credentials off-origin
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	const csp = "frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		// Without this, a browser may sniff a response's type from its bytes and
		// treat something ArcLoom serves as script or HTML — the generated-HCL and
		// run-log endpoints return attacker-influenced text.
		h.Set("X-Content-Type-Options", "nosniff")
		// Full URLs must not travel to third parties. The authorize query carries
		// the client id and PKCE challenge, and project URLs name infrastructure.
		h.Set("Referrer-Policy", "same-origin")
		// HSTS only when the operator has declared there is TLS in front (the same
		// flag that decides whether the session cookie is Secure). Sending it from
		// a plain-HTTP homelab install would pin browsers to an https:// origin
		// that does not answer, locking users out of their own server.
		if s.cfg.CookieSecure {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
