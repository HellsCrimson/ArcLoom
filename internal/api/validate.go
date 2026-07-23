package api

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// safeSegmentRe matches identifiers safe to use as a single filesystem path
// segment: letters, digits, underscore and hyphen only. It forbids '.', '/'
// and '\', so a URL-supplied id can never traverse out of its parent
// directory. This is exactly the base64url alphabet, so the same rule also
// covers session tokens and every UUID id ArcLoom mints.
var safeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validID reports whether a project id is safe to interpolate into a path.
func validID(id string) bool { return safeSegmentRe.MatchString(id) }

// providerSourceRe validates a Terraform/OpenTofu provider source address:
//
//	[hostname/][namespace/]type
//
// Labels are alphanumerics with internal dots and hyphens; the final `type`
// (which becomes the provider's local name and an HCL identifier via
// providerKeyFromSource) must start with a letter. This keeps a URL- or
// body-supplied source from carrying path-traversal or HCL-breaking characters
// into run.ProvidersSchema (which writes it into a generated config, then
// downloads and executes that provider plugin) and into hclgen's
// required_providers key (`generate.go` uses the local name as a bare attribute
// name).
var providerSourceRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9.-]*/){0,2}[A-Za-z][A-Za-z0-9_-]*$`)

// validProviderSource reports whether a provider source address is well-formed.
func validProviderSource(source string) bool { return providerSourceRe.MatchString(source) }

// Request body caps. Unbounded bodies let a client exhaust server memory
// (`json.NewDecoder(r.Body)` reads as much as it is given). maxBodySmall bounds
// the tiny auth payloads; maxBodyLarge bounds everything else, sized generously
// because the import routes (tfstate/HCL) share it.
const (
	maxBodySmall = 64 << 10 // 64 KiB
	maxBodyLarge = 32 << 20 // 32 MiB
)

// limitBody wraps the request body in an http.MaxBytesReader so a read past n
// bytes fails (handlers surface that as a 400 on decode). Mounted per group so
// the small auth cap and the larger default don't nest (an inner reader would
// otherwise win).
func limitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// validateID rejects any request whose {id} path param is not a safe path
// segment, before a handler interpolates it into a filesystem path (project
// dir, run dir, git worktree). It sits above the permission gates so a forged
// id is refused even for routes that skip store.Get (several git reads) and in
// auth-disabled mode. Routes without an {id} pass through untouched.
func (s *Server) validateID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := chi.URLParam(r, "id"); id != "" && !validID(id) {
			writeErr(w, http.StatusBadRequest, "invalid project id")
			return
		}
		next.ServeHTTP(w, r)
	})
}
