// Package api is the HTTP transport layer: a chi router exposing REST + SSE and
// serving the embedded Svelte SPA.
package api

import (
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/approvals"
	"github.com/HellsCrimson/ArcLoom/internal/audit"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/oauth"
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
	cfg       config.Config
	runners   map[model.Engine]runner.Runner
	store     store.Store
	cache     *cache.SchemaCache
	secrets   *secrets.Store
	static    fs.FS // embedded SPA build (may be nil in dev)
	auth      atomic.Pointer[auth.Authenticator]
	sessions  session.Store
	users     user.Store
	settings  *settings.Store
	runs      runs.Store      // may be nil (history disabled, e.g. in tests)
	audit     audit.Store     // may be nil (audit disabled, e.g. in tests)
	approvals approvals.Store // may be nil (approval gate disabled, e.g. in tests)
	apikeys   apikey.Store    // may be nil (bearer auth unavailable, e.g. in tests)
	oauth     oauth.Store     // may be nil (OAuth disabled, e.g. in tests)
	registry  *registry.Client

	// authLimiter throttles the argon2-backed password endpoints (login +
	// password change) per IP / username / user to blunt brute force and the
	// memory-DoS a burst of hashes would cause.
	authLimiter *rateLimiter

	// engineOps counts engine operations currently running (plan/apply/state/
	// drift). The master-key rotation refuses to start while any is in flight,
	// because those runs hold resolved secrets and the workspace lock.
	engineOps atomic.Int64

	// build + startedAt back the admin instance panel (what this binary is and
	// how long it has been up).
	build     BuildInfo
	startedAt time.Time

	// mux is the router built by Handler, kept so the MCP layer can re-enter it
	// with a synthetic request (see mcpcall.go) instead of duplicating every
	// handler's validation. Set once, before the server starts serving.
	mux atomic.Pointer[chi.Mux]

	// mcpOps tracks MCP-initiated engine runs (plan/apply/...) that stream in
	// the background, so a tool call can return an id immediately and the agent
	// can poll it. In-memory by design: an op that did not survive a restart
	// has no stream to resume either, and the run itself is still in
	// internal/runs.
	mcpMu  sync.Mutex
	mcpOps map[string]*mcpOp

	// Drift scheduler state: latest check outcome per project (in-memory) and
	// a nudge channel so config changes take effect without waiting a tick.
	driftMu     sync.Mutex
	driftStates map[string]driftStatus
	driftKick   chan struct{}
}

// New builds a Server. runners maps each available engine to its runner.
func New(cfg config.Config, runners map[model.Engine]runner.Runner, st store.Store, sc *cache.SchemaCache, sec *secrets.Store, static fs.FS, authn *auth.Authenticator, sessions session.Store, users user.Store, set *settings.Store, runHist runs.Store, auditLog audit.Store, approvalStore approvals.Store, keys apikey.Store, oauthStore oauth.Store) *Server {
	s := &Server{cfg: cfg, runners: runners, store: st, cache: sc, secrets: sec, static: static, sessions: sessions, users: users, settings: set, runs: runHist, audit: auditLog, approvals: approvalStore, apikeys: keys, oauth: oauthStore,
		registry:    registry.NewClient(),
		startedAt:   time.Now(),
		build:       BuildInfo{Version: "dev"},
		authLimiter: newRateLimiter(loginBurst, loginRefill),
		mcpOps:      map[string]*mcpOp{},
		driftStates: map[string]driftStatus{}, driftKick: make(chan struct{}, 1)}
	s.auth.Store(authn)
	return s
}

// router returns the built router for re-entrant (MCP) calls. Handler stores it
// before the server starts serving, so it is always set by the time a request
// can arrive.
func (s *Server) router() http.Handler { return s.mux.Load() }

// beginEngineOp marks an engine run as in flight and returns its release
// function; defer it. Used to keep disruptive admin operations (master-key
// rotation) from starting mid-run.
func (s *Server) beginEngineOp() func() {
	s.engineOps.Add(1)
	return func() { s.engineOps.Add(-1) }
}

// engineBusy reports whether any plan/apply/state/drift run is in flight.
func (s *Server) engineBusy() bool { return s.engineOps.Load() > 0 }

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
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)
	// Browser-enforced hardening on every response, including the SPA shell and
	// the OAuth consent route it serves (see headers.go).
	r.Use(s.securityHeaders)

	// Liveness/health probes: public, unauthenticated, dependency-free. Declared
	// before the SPA catch-all so they aren't shadowed by the index fallback.
	r.Get("/healthz", s.handleHealth)
	r.Get("/livez", s.handleHealth)

	// OAuth discovery lives at the ROOT, not under /api: RFC 9728 and RFC 8414
	// both define well-known paths relative to the host, and that is what a
	// client actually fetches (verified against Claude Code). Declared before the
	// SPA catch-all so the shell does not swallow them.
	if s.oauthEnabled() {
		r.Get("/.well-known/oauth-protected-resource", s.handleProtectedResourceMetadata)
		// Clients also probe the path-suffixed form when the resource is not at
		// the host root, which ours is not (/api/mcp).
		r.Get("/.well-known/oauth-protected-resource/*", s.handleProtectedResourceMetadata)
		r.Get("/.well-known/oauth-authorization-server", s.handleAuthServerMetadata)
		r.Get("/.well-known/oauth-authorization-server/*", s.handleAuthServerMetadata)
	}

	r.Route("/api", func(r chi.Router) {
		// Public: reachable without a session so the user can log in. /auth/me
		// returns the current user or 401, which the SPA uses to decide whether
		// to show the login screen.
		r.Group(func(r chi.Router) {
			r.Use(limitBody(maxBodySmall)) // auth payloads are tiny
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Get("/auth/sso/{provider}/login", s.handleSSOLogin)
			r.Get("/auth/sso/{provider}/callback", s.handleSSOCallback)

			// OAuth for MCP clients. Registration, discovery and the token
			// endpoint are necessarily unauthenticated — a client reaches them
			// precisely because it has no credential yet. What they hand out is
			// gated by the consent screen (authenticated, below), not here.
			if s.oauthEnabled() {
				r.Get("/oauth/authorization-server", s.handleAuthServerMetadata)
				r.Post("/oauth/register", s.handleRegisterClient)
				r.Post("/oauth/token", s.handleToken)
			}
		})

		// Protected: everything else requires a valid session. requireAuth runs
		// before every handler body, so the SSE plan route is gated before it
		// starts streaming (a 401 can't be sent once WriteHeader(200) fires).
		// Within it, mutating/operational routes are further gated by capability
		// (resources / deploy / git / admin); reads need only a session.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			// Reject a path-traversing {id} before any handler builds a
			// filesystem path from it (covers every {id} route, incl. the git
			// reads that skip store.Get and auth-disabled mode).
			r.Use(s.validateID)
			// Bound every request body so a large upload can't exhaust memory.
			// Import routes (tfstate/HCL) share this cap, hence the generous size.
			r.Use(limitBody(maxBodyLarge))

			// Read-only / self-service (any authenticated user, project-agnostic).
			r.Post("/auth/password", s.handlePasswordChange)
			r.Get("/engines", s.handleEngines)
			r.Get("/projects", s.handleListProjects) // filtered by per-project ACL

			// API keys are self-service: any authenticated user manages their
			// own, and a key can only ever be narrower than its owner (see
			// normalizeScopes), so this needs no capability of its own.
			// csrfGuard because these mint and revoke credentials.
			//
			// requireBrowserSession is what keeps that "narrower than its owner"
			// rule true: normalizeScopes measures the request against the OWNER's
			// permissions, so without it a scopeless key could mint one holding
			// everything its owner holds and escape its own restriction. Minting
			// and revoking are browser actions — the token is shown once, in the
			// UI — so a blanket refusal costs nothing and cannot be got subtly
			// wrong later.
			r.Group(func(r chi.Router) {
				r.Use(s.requireBrowserSession)
				r.Use(s.csrfGuard)
				r.Use(s.auditMiddleware)
				r.Get("/keys", s.handleListAPIKeys)
				r.Post("/keys", s.handleCreateAPIKey)
				r.Delete("/keys/{kid}", s.handleDeleteAPIKey)
				// The consent step: the user is already signed in here, which is
				// what makes approving an MCP client one click.
				r.Get("/oauth/authorize/info", s.handleAuthorizeInfo)
				r.Post("/oauth/authorize", s.handleAuthorizeApprove)
				r.Get("/oauth/grants", s.handleListGrants)
				r.Delete("/oauth/grants/{gid}", s.handleRevokeGrant)
			})

			// MCP (Model Context Protocol): one endpoint, all methods. It sits
			// inside the protected group so requireAuth authenticates it before
			// any tool runs, and every tool then re-enters this same router —
			// so per-tool authorization is the ordinary route gate, not a
			// second implementation. csrfGuard is deliberate: an MCP client
			// authenticates by bearer, which csrfSafe passes, while a browser
			// page cannot reach it cross-site.
			if s.mcpEnabled() {
				r.Group(func(r chi.Router) {
					r.Use(s.csrfGuard)
					r.Handle("/mcp", s.mcpHandler())
				})
			}

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
				r.Get("/projects/{id}/policy", s.handleGetPolicy)
				r.Get("/projects/{id}/notify", s.handleGetNotify)
				r.Get("/projects/{id}/provider-updates", s.handleProviderUpdates)
				r.Get("/projects/{id}/acl", s.handleGetACL)
				r.Get("/projects/{id}/environments", s.handleGetEnvironments)
				r.Get("/projects/{id}/env-vars", s.handleGetEnvVars)
				r.Get("/projects/{id}/approval", s.handleGetApproval)
			})

			// Resource editing (PermResources + per-project ACL, audited).
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermResources))
				r.Use(s.csrfGuard)
				r.Use(s.auditMiddleware)
				// Schema reads init + execute the named provider plugin, so they
				// need PermResources (a read-only user must not make the server
				// download and run an arbitrary binary). They carry no {id}, so
				// requireProjectPerm falls back to the global capability check.
				r.Get("/providers/{source}/schema", s.handleProviderSchema)
				r.Get("/providers/{source}/resources/{type}/schema", s.handleResourceSchema)
				r.Get("/providers/{source}/datasources/{type}/schema", s.handleDataSourceSchema)
				r.Post("/providers", s.handleAddProvider)
				r.Post("/projects", s.handleCreateProject)
				r.Put("/projects/{id}", s.handleReplaceProject)
				r.Delete("/projects/{id}", s.handleDeleteProject)
				r.Put("/projects/{id}/providers/{key}", s.handleSetProviderConfig)
				r.Delete("/projects/{id}/providers/{key}", s.handleDeleteProviderInstance)
				r.Put("/projects/{id}/config", s.handleSetConfig)
				// modules/local/* is the DEFINITION (a modules/<name>/ folder);
				// modules/call/* is a USE of one (or of a registry module).
				r.Post("/projects/{id}/modules/local", s.handleCreateLocalModule)
				r.Delete("/projects/{id}/modules/local/{name}", s.handleDeleteLocalModule)
				r.Put("/projects/{id}/modules/call", s.handleSetModuleCall)
				r.Delete("/projects/{id}/modules/call/{name}", s.handleDeleteModuleCall)
				r.Put("/projects/{id}/files", s.handleSetFile)
				r.Delete("/projects/{id}/files", s.handleDeleteFile)
				r.Put("/projects/{id}/backend", s.handleSetBackend)
				r.Put("/projects/{id}/terraform", s.handleSetTerraform)
				r.Put("/projects/{id}/imports", s.handleSetImports)
				r.Put("/projects/{id}/moved", s.handleSetMoved)
				r.Put("/projects/{id}/secrets/{name}", s.handleSetSecret)
				// Reveal is a POST for the reasons in handleRevealSecret, and lives
				// in the resources group so it inherits requireProjectPerm + the
				// project ACL + csrfGuard + the audit middleware.
				r.Post("/projects/{id}/secrets/{name}/reveal", s.handleRevealSecret)
				r.Delete("/projects/{id}/secrets/{name}", s.handleDeleteSecret)
				r.Post("/projects/{id}/resources", s.handleUpsertResource)
				r.Delete("/projects/{id}/resources/{rid}", s.handleDeleteResource)
				r.Post("/projects/{id}/import/hcl", s.handleImportHCL)
				r.Post("/projects/{id}/import/state", s.handleImportState)
				r.Put("/projects/{id}/cost", s.handleSetCost)
				r.Put("/projects/{id}/policy", s.handleSetPolicy)
				r.Put("/projects/{id}/notify", s.handleSetNotify)
				// Sends a test event through the stored webhook so a setup can
				// be verified without waiting for a real failure.
				r.Post("/projects/{id}/notify/test", s.handleTestNotify)
				r.Put("/projects/{id}/environments", s.handleSetEnvironments)
				r.Put("/projects/{id}/env-vars", s.handleSetEnvVars)
				r.Put("/projects/{id}/approval", s.handleSetApprovalPolicy)
			})

			// Deploy: plan / apply / destroy (PermDeploy). Plan is a GET so the
			// SPA can use EventSource; apply and destroy mutate infrastructure
			// and are POSTs (fetch-streamed) so a bare link can never trigger
			// them.
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermDeploy))
				// Guards the GET SSE plan/validate/lint (side effects) as well as
				// the POST apply/destroy/state ops against cross-site triggering.
				r.Use(s.csrfGuard)
				r.Get("/projects/{id}/plan", s.handlePlan)
				r.Get("/projects/{id}/validate", s.handleValidate)
				r.Get("/projects/{id}/lint", s.handleLint)
				r.Post("/projects/{id}/apply", s.handleApply)
				r.Post("/projects/{id}/destroy", s.handleDestroy)
				// State surgery: adopt a live resource / fix drifted addresses.
				r.Post("/projects/{id}/state/import", s.handleStateImport)
				r.Post("/projects/{id}/state/rm", s.handleStateRm)
				r.Post("/projects/{id}/state/mv", s.handleStateMv)
				// Mark an instance for replacement, or clear a mark a failed
				// provisioner left behind.
				r.Post("/projects/{id}/state/taint", s.handleStateTaint)
				r.Post("/projects/{id}/state/untaint", s.handleStateUntaint)
				// Release a lock a crashed run left behind (the only remedy with
				// a remote backend, where there is no file to delete).
				r.Post("/projects/{id}/state/force-unlock", s.handleStateForceUnlock)
				r.Post("/projects/{id}/state/init-backend", s.handleInitBackend)
				// Absorb drift into the state (apply -refresh-only; no infra change).
				r.Post("/projects/{id}/state/accept-drift", s.handleAcceptDrift)
				// Drift schedule: enabling it makes the server run plans.
				r.Put("/projects/{id}/drift", s.handleSetDrift)
			})

			// Approve a saved plan for a gated apply (PermApprove + per-project ACL,
			// audited). Separate from deploy so an org can split "can review/approve"
			// from "can run plans".
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermApprove))
				r.Use(s.csrfGuard)
				r.Use(s.auditMiddleware)
				r.Post("/projects/{id}/approve", s.handleApprove)
			})

			// Git operations (PermGit + per-project ACL, audited).
			r.Group(func(r chi.Router) {
				r.Use(s.requireProjectPerm(user.PermGit))
				r.Use(s.csrfGuard)
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
				r.Post("/projects/{id}/git/undo-commit", s.handleGitUndoCommit)
				r.Get("/projects/{id}/git/credentials", s.handleGitCredentials)
				r.Put("/projects/{id}/git/credentials", s.handleSetGitCredentials)
				r.Delete("/projects/{id}/git/credentials", s.handleDeleteGitCredentials)
			})

			// Administration: user management + settings + audit (admin only,
			// mutations audited).
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Use(s.csrfGuard)
				r.Use(s.auditMiddleware)
				r.Get("/admin/users", s.handleListUsers)
				r.Post("/admin/users", s.handleCreateUser)
				r.Put("/admin/users/{uid}", s.handleUpdateUser)
				r.Post("/admin/users/{uid}/password", s.handleResetUserPassword)
				r.Delete("/admin/users/{uid}", s.handleDeleteUser)
				r.Get("/admin/settings/sso", s.handleGetSSO)
				r.Put("/admin/settings/sso", s.handleSetSSO)
				r.Get("/admin/settings/secret-store", s.handleGetSecretStore)
				r.Put("/admin/settings/secret-store", s.handleSetSecretStore)
				r.Post("/admin/settings/secret-store/test", s.handleTestSecretStore)
				// Master-key health + rotation (the Danger zone): re-keys the
				// encrypted secret files and the settings document together.
				r.Get("/admin/settings/secret-key", s.handleGetSecretKey)
				r.Post("/admin/settings/secret-key/rotate", s.handleRotateSecretKey)
				r.Get("/admin/keys", s.handleListAllAPIKeys)
				r.Delete("/admin/keys/{kid}", s.handleDeleteAPIKey)
				r.Get("/admin/audit", s.handleListAudit)
				r.Get("/admin/instance", s.handleInstanceInfo)
				// ACL editing is admin-only and enforced in the handler.
				r.Put("/projects/{id}/acl", s.handleSetACL)
			})
		})
	})

	if s.static != nil {
		r.Handle("/*", s.spaHandler())
	}
	// Keep the router for re-entrant MCP tool calls (mcpcall.go). Stored before
	// the caller starts serving, so it is never nil under a live request.
	s.mux.Store(r)
	return r
}

// spaHandler serves embedded static assets and falls back to 200.html for any
// client-side route so deep links work.
func (s *Server) spaHandler() http.Handler {
	// Go's built-in table has no entry for .webmanifest, so the icon manifest
	// would go out as text/plain and Chrome would refuse it.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
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
