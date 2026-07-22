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
