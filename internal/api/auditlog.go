package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HellsCrimson/ArcLoom/internal/audit"
)

// auditActions maps "METHOD <route-pattern>" (with the /api prefix stripped) to
// a friendly action verb. Unmapped mutating routes fall back to "METHOD pattern".
var auditActions = map[string]string{
	"POST /providers":                            "provider.add",
	"POST /projects":                             "project.create",
	"PUT /projects/{id}":                         "project.replace",
	"DELETE /projects/{id}":                      "project.delete",
	"PUT /projects/{id}/providers/{key}":         "provider.set",
	"DELETE /projects/{id}/providers/{key}":      "provider.delete",
	"PUT /projects/{id}/config":                  "config.set",
	"POST /projects/{id}/modules/local":          "module.local.create",
	"DELETE /projects/{id}/modules/local/{name}": "module.local.delete",
	"PUT /projects/{id}/modules/call":            "module.call.set",
	"DELETE /projects/{id}/modules/call/{name}":  "module.call.delete",
	"PUT /projects/{id}/files":                   "file.set",
	"DELETE /projects/{id}/files":                "file.delete",
	"PUT /projects/{id}/backend":                 "backend.set",
	"PUT /projects/{id}/terraform":               "terraform.set",
	"PUT /projects/{id}/imports":                 "imports.set",
	"PUT /projects/{id}/moved":                   "moved.set",
	"PUT /projects/{id}/secrets/{name}":          "secret.set",
	// Reading a secret back is as worth recording as changing one — more so, since
	// it is the only way a value leaves the server.
	"POST /projects/{id}/secrets/{name}/reveal": "secret.reveal",
	"DELETE /projects/{id}/secrets/{name}":      "secret.delete",
	"POST /projects/{id}/resources":             "resource.upsert",
	"DELETE /projects/{id}/resources/{rid}":     "resource.delete",
	"POST /projects/{id}/import/hcl":            "import.hcl",
	"POST /projects/{id}/import/state":          "import.state",
	"PUT /projects/{id}/cost":                   "cost.set",
	"PUT /projects/{id}/policy":                 "policy.set",
	"PUT /projects/{id}/notify":                 "notify.set",
	"POST /projects/{id}/notify/test":           "notify.test",
	"PUT /projects/{id}/environments":           "environments.set",
	"PUT /projects/{id}/approval":               "approval.policy",
	"POST /projects/{id}/approve":               "approval.approve",
	"POST /projects/{id}/git/commit":            "git.commit",
	"POST /projects/{id}/git/restore":           "git.restore",
	"POST /projects/{id}/git/remote":            "git.remote",
	"POST /projects/{id}/git/push":              "git.push",
	"POST /projects/{id}/git/pull":              "git.pull",
	"POST /projects/{id}/git/branch":            "git.branch.create",
	"DELETE /projects/{id}/git/branch":          "git.branch.delete",
	"POST /projects/{id}/git/checkout":          "git.checkout",
	"POST /projects/{id}/git/merge":             "git.merge",
	"POST /projects/{id}/git/undo-commit":       "git.commit.undo",
	"PUT /projects/{id}/git/credentials":        "git.credentials.set",
	"DELETE /projects/{id}/git/credentials":     "git.credentials.delete",
	"PUT /projects/{id}/acl":                    "acl.set",
	"POST /admin/users":                         "user.create",
	"PUT /admin/users/{uid}":                    "user.update",
	"POST /admin/users/{uid}/password":          "user.password",
	"DELETE /admin/users/{uid}":                 "user.delete",
	"PUT /admin/settings/sso":                   "settings.sso",
	"PUT /admin/settings/secret-store":          "settings.secret_store.set",
	"POST /admin/settings/secret-store/test":    "settings.secret_store.test",
	"POST /admin/settings/secret-key/rotate":    "settings.secret_key.rotate",
}

// statusRecorder captures the response status so the audit middleware records
// only successful mutations.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush proxies to the underlying writer so SSE streaming still works when the
// middleware wraps a streaming handler (defensive; audited groups aren't SSE).
func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// auditMiddleware records a successful mutating request to the audit log. Reads
// and non-2xx responses are ignored. Applied to the resource/git/admin/ACL
// route groups; deploy operations are excluded because internal/runs already
// records them per project.
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.audit == nil || !mutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			s.recordAudit(r, auditActionFor(r), chi.URLParam(r, "id"), r.URL.Path)
		}
	})
}

func mutatingMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete || m == http.MethodPatch
}

// auditActionFor derives the friendly action for the matched route.
func auditActionFor(r *http.Request) string {
	pattern := r.Method + " " + strings.TrimPrefix(routePattern(r), "/api")
	if a, ok := auditActions[pattern]; ok {
		return a
	}
	return pattern
}

func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		return rc.RoutePattern()
	}
	return r.URL.Path
}

// recordAudit appends an audit entry (best-effort). The user is the current
// request's identity ("" when auth is disabled).
func (s *Server) recordAudit(r *http.Request, action, projectID, detail string) {
	if s.audit == nil {
		return
	}
	username := ""
	if u, ok := userFromContext(r); ok {
		username = u.Username
	}
	_ = s.audit.Append(&audit.Entry{
		ID:        uuid.NewString(),
		Time:      time.Now(),
		User:      username,
		Action:    action,
		ProjectID: projectID,
		Detail:    detail,
	})
}

// recordAuditAs appends an audit entry attributed to a username instead of the
// request's authenticated user — for actions with no logged-in actor, such as an
// SSO callback provisioning the very account it is about to log in.
func (s *Server) recordAuditAs(username, action, detail string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Append(&audit.Entry{
		ID:     uuid.NewString(),
		Time:   time.Now(),
		User:   username,
		Action: action,
		Detail: detail,
	})
}

// handleListAudit returns audit entries (admin only), newest first, with
// optional ?project=, ?user= and ?limit= filters.
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusOK, []*audit.Entry{})
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	entries, err := s.audit.List(audit.Filter{
		ProjectID: r.URL.Query().Get("project"),
		User:      r.URL.Query().Get("user"),
		Limit:     limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []*audit.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
