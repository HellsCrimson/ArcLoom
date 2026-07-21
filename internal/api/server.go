// Package api is the HTTP transport layer: a chi router exposing REST + SSE and
// serving the embedded Svelte SPA.
package api

import (
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/HellsCrimson/ArcLoom/internal/audit"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/registry"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
)

// Server holds shared dependencies for the HTTP handlers.
type Server struct {
	cfg      config.Config
	runners  map[model.Engine]runner.Runner
	store    store.Store
	cache    *cache.SchemaCache
	secrets  *secrets.Store
	static   fs.FS // embedded SPA build (may be nil in dev)
	auth     atomic.Pointer[auth.Authenticator]
	sessions session.Store
	users    user.Store
	settings *settings.Store
	license  *license.Manager
	runs     runs.Store  // may be nil (history disabled, e.g. in tests)
	audit    audit.Store // may be nil (audit disabled, e.g. in tests)
	registry *registry.Client

	// Drift scheduler state: latest check outcome per project (in-memory) and
	// a nudge channel so config changes take effect without waiting a tick.
	driftMu     sync.Mutex
	driftStates map[string]driftStatus
	driftKick   chan struct{}
}

// New builds a Server. runners maps each available engine to its runner.
func New(cfg config.Config, runners map[model.Engine]runner.Runner, st store.Store, sc *cache.SchemaCache, sec *secrets.Store, static fs.FS, authn *auth.Authenticator, sessions session.Store, users user.Store, set *settings.Store, runHist runs.Store, auditLog audit.Store) *Server {
	s := &Server{cfg: cfg, runners: runners, store: st, cache: sc, secrets: sec, static: static, sessions: sessions, users: users, settings: set, runs: runHist, audit: auditLog,
		registry:    registry.NewClient(),
		driftStates: map[string]driftStatus{}, driftKick: make(chan struct{}, 1)}
	s.auth.Store(authn)
	// License gate: verify the persisted token (if any) against the embedded
	// vendor key. A bad/expired token just leaves the install at community tier.
	s.license = license.NewManager(license.VendorKey())
	if tok := set.License(); tok != "" {
		if err := s.license.Set(tok); err != nil {
			log.Printf("WARNING: stored license rejected (%v); running as community", err)
		}
	}
	return s
}

// authn returns the current Authenticator (swapped atomically when SSO settings
// change at runtime).
func (s *Server) authn() *auth.Authenticator { return s.auth.Load() }

// setAuth atomically replaces the Authenticator (used after SSO settings change).
func (s *Server) setAuth(a *auth.Authenticator) { s.auth.Store(a) }

// runnerFor returns the runner for an engine name, falling back to the
// configured default engine when name is empty.
func (s *Server) runnerFor(name string) (runner.Runner, bool) {
	if name == "" {
		name = s.cfg.DefaultEngine
	}
	r, ok := s.runners[model.Engine(name)]
	return r, ok
}

// Handler builds the full HTTP handler (API routes + SPA fallback).
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		// Public: reachable without a session so the user can log in. /auth/me
		// returns the current user or 401, which the SPA uses to decide whether
		// to show the login screen.
		r.Group(func(r chi.Router) {
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Get("/auth/sso/{provider}/login", s.handleSSOLogin)
			r.Get("/auth/sso/{provider}/callback", s.handleSSOCallback)
		})

		// Protected: everything else requires a valid session. requireAuth runs
		// before every handler body, so the SSE plan route is gated before it
		// starts streaming (a 401 can't be sent once WriteHeader(200) fires).
		// Within it, mutating/operational routes are further gated by capability
		// (resources / deploy / git / admin); reads need only a session.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			// Read-only / self-service (any authenticated user, project-agnostic).
			r.Post("/auth/password", s.handlePasswordChange)
			r.Get("/license", s.handleGetLicense)
			r.Get("/engines", s.handleEngines)
			r.Get("/providers/{source}/schema", s.handleProviderSchema)
			r.Get("/providers/{source}/resources/{type}/schema", s.handleResourceSchema)
			r.Get("/providers/{source}/datasources/{type}/schema", s.handleDataSourceSchema)
			r.Get("/projects", s.handleListProjects) // filtered by per-project ACL

			// Project reads gated by the per-project access list.
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectRead)
				r.Get("/projects/{id}", s.handleGetProject)
				r.Get("/projects/{id}/secrets", s.handleListSecrets)
				r.Get("/projects/{id}/hcl", s.handleProjectHCL)
				r.Get("/projects/{id}/graph", s.handleGraph)
				r.Get("/projects/{id}/git/status", s.handleGitStatus)
				r.Get("/projects/{id}/git/log", s.handleGitLog)
				r.Get("/projects/{id}/git/diff", s.handleGitDiff)
				r.Get("/projects/{id}/git/branches", s.handleGitBranches)
				r.Get("/projects/{id}/runs", s.handleListRuns)
				r.Get("/projects/{id}/runs/{rid}", s.handleGetRun)
				r.Get("/projects/{id}/state", s.handleStateList)
				r.Get("/projects/{id}/drift", s.handleGetDrift)
				r.Get("/projects/{id}/cost", s.handleGetCost)
				r.Get("/projects/{id}/provider-updates", s.handleProviderUpdates)
				r.Get("/projects/{id}/acl", s.handleGetACL)
			})

			// Resource editing (PermResources + per-project ACL, audited).
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermResources))
				r.Use(s.auditMiddleware)
				r.Post("/providers", s.handleAddProvider)
				r.Post("/projects", s.handleCreateProject)
				r.Put("/projects/{id}", s.handleReplaceProject)
				r.Delete("/projects/{id}", s.handleDeleteProject)
				r.Put("/projects/{id}/providers/{key}", s.handleSetProviderConfig)
				r.Delete("/projects/{id}/providers/{key}", s.handleDeleteProviderInstance)
				r.Put("/projects/{id}/config", s.handleSetConfig)
				r.Post("/projects/{id}/modules/local", s.handleCreateLocalModule)
				r.Delete("/projects/{id}/modules/local/{name}", s.handleDeleteLocalModule)
				r.Put("/projects/{id}/modules/local/{name}/inputs", s.handleSetModuleInputs)
				r.Put("/projects/{id}/modules/registry", s.handleSetRegistryModule)
				r.Delete("/projects/{id}/modules/registry/{name}", s.handleDeleteRegistryModule)
				r.Put("/projects/{id}/backend", s.handleSetBackend)
				r.Put("/projects/{id}/secrets/{name}", s.handleSetSecret)
				r.Delete("/projects/{id}/secrets/{name}", s.handleDeleteSecret)
				r.Post("/projects/{id}/resources", s.handleUpsertResource)
				r.Delete("/projects/{id}/resources/{rid}", s.handleDeleteResource)
				r.Post("/projects/{id}/import/hcl", s.handleImportHCL)
				r.Post("/projects/{id}/import/state", s.handleImportState)
				r.Put("/projects/{id}/cost", s.handleSetCost)
			})

			// Deploy: plan / apply / destroy (PermDeploy). Plan is a GET so the
			// SPA can use EventSource; apply and destroy mutate infrastructure
			// and are POSTs (fetch-streamed) so a bare link can never trigger
			// them.
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermDeploy))
				r.Get("/projects/{id}/plan", s.handlePlan)
				r.Get("/projects/{id}/validate", s.handleValidate)
				r.Get("/projects/{id}/lint", s.handleLint)
				r.Post("/projects/{id}/apply", s.handleApply)
				r.Post("/projects/{id}/destroy", s.handleDestroy)
				// State surgery: adopt a live resource / fix drifted addresses.
				r.Post("/projects/{id}/state/import", s.handleStateImport)
				r.Post("/projects/{id}/state/rm", s.handleStateRm)
				r.Post("/projects/{id}/state/mv", s.handleStateMv)
				r.Post("/projects/{id}/state/init-backend", s.handleInitBackend)
				// Drift schedule: enabling it makes the server run plans.
				r.Put("/projects/{id}/drift", s.handleSetDrift)
			})

			// Git operations (PermGit + per-project ACL, audited).
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermGit))
				r.Use(s.auditMiddleware)
				r.Post("/projects/{id}/git/commit", s.handleGitCommit)
				r.Post("/projects/{id}/git/restore", s.handleGitRestore)
				r.Post("/projects/{id}/git/remote", s.handleGitRemote)
				r.Post("/projects/{id}/git/push", s.handleGitPush)
				r.Post("/projects/{id}/git/pull", s.handleGitPull)
				r.Post("/projects/{id}/git/branch", s.handleGitCreateBranch)
				r.Delete("/projects/{id}/git/branch", s.handleGitDeleteBranch)
				r.Post("/projects/{id}/git/checkout", s.handleGitCheckout)
				r.Post("/projects/{id}/git/merge", s.handleGitMerge)
				r.Get("/projects/{id}/git/credentials", s.handleGitCredentials)
				r.Put("/projects/{id}/git/credentials", s.handleSetGitCredentials)
				r.Delete("/projects/{id}/git/credentials", s.handleDeleteGitCredentials)
			})

			// Administration: user management + settings + audit (admin only,
			// mutations audited).
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Use(s.auditMiddleware)
				r.Get("/admin/users", s.handleListUsers)
				r.Post("/admin/users", s.handleCreateUser)
				r.Put("/admin/users/{uid}", s.handleUpdateUser)
				r.Post("/admin/users/{uid}/password", s.handleResetUserPassword)
				r.Delete("/admin/users/{uid}", s.handleDeleteUser)
				r.Get("/admin/settings/sso", s.handleGetSSO)
				r.Put("/admin/settings/sso", s.handleSetSSO)
				r.Put("/admin/settings/license", s.handleSetLicense)
				r.Get("/admin/audit", s.handleListAudit)
				// ACL editing is admin-only and enforced in the handler (also
				// gated behind the rbac license feature there).
				r.Put("/projects/{id}/acl", s.handleSetACL)
			})
		})
	})

	if s.static != nil {
		r.Handle("/*", s.spaHandler())
	}
	return r
}

// spaHandler serves embedded static assets and falls back to 200.html for any
// client-side route so deep links work.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := s.static.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, req)
			return
		}
		// SPA fallback.
		data, err := fs.ReadFile(s.static, "200.html")
		if err != nil {
			if data, err = fs.ReadFile(s.static, "index.html"); err != nil {
				http.NotFound(w, req)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}
