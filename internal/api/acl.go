package api

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// projectAllows reports whether a user may perform a permission-gated action on
// a project, assuming the global permission check already passed. Access-list
// enforcement is a *restriction* on top of global RBAC:
//   - admins and the auth-disabled placeholder always pass;
//   - a project with no ACL defers to global RBAC (passes);
//   - a project with an ACL restricts to listed users, whose per-project perms
//     must include the requested capability.
func (s *Server) projectAllows(u *user.User, projectID, perm string) bool {
	if u == nil || u.Admin {
		return true
	}
	acl, ok := s.settings.ProjectACL(projectID)
	if !ok {
		return true
	}
	for _, e := range acl {
		if e.UserID == u.ID {
			return slices.Contains(e.Perms, perm)
		}
	}
	return false
}

// projectVisible reports whether a user may read a project. Mirrors
// projectAllows without a specific permission: a restricted project is readable
// only by admins and listed users.
func (s *Server) projectVisible(u *user.User, projectID string) bool {
	if u == nil || u.Admin {
		return true
	}
	acl, ok := s.settings.ProjectACL(projectID)
	if !ok {
		return true
	}
	for _, e := range acl {
		if e.UserID == u.ID {
			return true
		}
	}
	return false
}

// requireProjectPerm gates a permission-scoped route: it enforces the global
// capability (like requirePerm) and, when the route carries a project {id},
// additionally enforces that project's access list. Routes without an {id}
// (e.g. project creation) fall back to the plain global check.
func (s *Server) requireProjectPerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.cfg.AuthEnabled {
				next.ServeHTTP(w, r)
				return
			}
			u, ok := userFromContext(r)
			if !ok {
				writeErr(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !u.Can(perm) {
				writeErr(w, http.StatusForbidden, "you do not have permission to do that")
				return
			}
			id := chi.URLParam(r, "id")
			if id != "" && !s.projectAllows(u, id, perm) {
				writeErr(w, http.StatusForbidden, "you do not have access to this project")
				return
			}
			// An API key restricts on top of all of the above: its scopes must
			// include this capability and, if it is project-restricted, this
			// project. Routes with no {id} pass "" and are checked on scope only.
			if msg := keyDenies(r, perm, id); msg != "" {
				writeErr(w, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireProjectRead gates a project read route on the project's access list
// (any authenticated user may read an unrestricted project; a restricted one
// only its members and admins).
func (s *Server) requireProjectRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthEnabled {
			next.ServeHTTP(w, r)
			return
		}
		u, ok := userFromContext(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id := chi.URLParam(r, "id")
		if id != "" && !s.projectVisible(u, id) {
			writeErr(w, http.StatusForbidden, "you do not have access to this project")
			return
		}
		// Reads need no scope — any authenticated caller may read — but a
		// project-restricted key must not read outside its projects either,
		// so pass an empty perm and let keyDenies check only the restriction.
		if msg := keyDenies(r, "", id); msg != "" {
			writeErr(w, http.StatusForbidden, msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// aclEntryDTO is one access-list row enriched with the username for display.
type aclEntryDTO struct {
	UserID   string   `json:"userId"`
	Username string   `json:"username"`
	Perms    []string `json:"perms"`
}

type aclDTO struct {
	Restricted bool          `json:"restricted"`
	Entries    []aclEntryDTO `json:"entries"`
}

// handleGetACL returns a project's access list (visible to admins + members via
// the requireProjectRead gate on the route).
func (s *Server) handleGetACL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	entries, restricted := s.settings.ProjectACL(id)
	out := aclDTO{Restricted: restricted, Entries: []aclEntryDTO{}}
	for _, e := range entries {
		username := e.UserID
		if u, err := s.users.Get(e.UserID); err == nil {
			username = u.Username
		}
		perms := e.Perms
		if perms == nil {
			perms = []string{}
		}
		out.Entries = append(out.Entries, aclEntryDTO{UserID: e.UserID, Username: username, Perms: perms})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetACL replaces a project's access list. Admin-only. An empty entry list
// clears the restriction.
func (s *Server) handleSetACL(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, ok := userFromContext(r)
	if s.cfg.AuthEnabled && (!ok || !u.Admin) {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		Entries []settings.ACLEntry `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	valid := user.AllPermissions()
	seen := map[string]bool{}
	cleaned := make([]settings.ACLEntry, 0, len(req.Entries))
	for _, e := range req.Entries {
		if e.UserID == "" || seen[e.UserID] {
			writeErr(w, http.StatusBadRequest, "each entry needs a unique userId")
			return
		}
		if _, err := s.users.Get(e.UserID); err != nil {
			writeErr(w, http.StatusBadRequest, "unknown user in access list")
			return
		}
		for _, p := range e.Perms {
			if !slices.Contains(valid, p) {
				writeErr(w, http.StatusBadRequest, "unknown permission: "+p)
				return
			}
		}
		seen[e.UserID] = true
		cleaned = append(cleaned, settings.ACLEntry{UserID: e.UserID, Perms: e.Perms})
	}
	if err := s.settings.SetProjectACL(id, cleaned); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.handleGetACL(w, r)
}
