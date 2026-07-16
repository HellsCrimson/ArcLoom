// Package api is the HTTP transport layer: a chi router exposing REST + SSE and
// serving the embedded Svelte SPA.
package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
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
	auth     *auth.Authenticator
	sessions session.Store
	users    user.Store
}

// New builds a Server. runners maps each available engine to its runner.
func New(cfg config.Config, runners map[model.Engine]runner.Runner, st store.Store, sc *cache.SchemaCache, sec *secrets.Store, static fs.FS, authn *auth.Authenticator, sessions session.Store, users user.Store) *Server {
	return &Server{cfg: cfg, runners: runners, store: st, cache: sc, secrets: sec, static: static, auth: authn, sessions: sessions, users: users}
}

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
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Post("/auth/password", s.handlePasswordChange)

			r.Get("/engines", s.handleEngines)
			r.Post("/providers", s.handleAddProvider)
			r.Get("/providers/{source}/schema", s.handleProviderSchema)
			r.Get("/providers/{source}/resources/{type}/schema", s.handleResourceSchema)

			r.Get("/projects", s.handleListProjects)
			r.Post("/projects", s.handleCreateProject)
			r.Get("/projects/{id}", s.handleGetProject)
			r.Delete("/projects/{id}", s.handleDeleteProject)
			r.Put("/projects/{id}/providers/{key}", s.handleSetProviderConfig)
			r.Put("/projects/{id}/config", s.handleSetConfig)
			r.Post("/projects/{id}/modules/local", s.handleCreateLocalModule)
			r.Delete("/projects/{id}/modules/local/{name}", s.handleDeleteLocalModule)
			r.Put("/projects/{id}/modules/local/{name}/inputs", s.handleSetModuleInputs)
			r.Get("/projects/{id}/secrets", s.handleListSecrets)
			r.Put("/projects/{id}/secrets/{name}", s.handleSetSecret)
			r.Delete("/projects/{id}/secrets/{name}", s.handleDeleteSecret)
			r.Post("/projects/{id}/resources", s.handleUpsertResource)
			r.Delete("/projects/{id}/resources/{rid}", s.handleDeleteResource)
			r.Get("/projects/{id}/hcl", s.handleProjectHCL)
			r.Get("/projects/{id}/git/status", s.handleGitStatus)
			r.Post("/projects/{id}/git/commit", s.handleGitCommit)
			r.Get("/projects/{id}/git/log", s.handleGitLog)
			r.Get("/projects/{id}/git/diff", s.handleGitDiff)
			r.Post("/projects/{id}/git/restore", s.handleGitRestore)
			r.Post("/projects/{id}/git/remote", s.handleGitRemote)
			r.Post("/projects/{id}/git/push", s.handleGitPush)
			r.Post("/projects/{id}/git/pull", s.handleGitPull)
			r.Post("/projects/{id}/import/hcl", s.handleImportHCL)
			r.Post("/projects/{id}/import/state", s.handleImportState)
			r.Get("/projects/{id}/graph", s.handleGraph)
			r.Get("/projects/{id}/plan", s.handlePlan)
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
