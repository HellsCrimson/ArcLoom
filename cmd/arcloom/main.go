// Command arcloom is the ArcLoom server: it serves the REST/SSE API and the
// embedded Svelte SPA, wrapping the Terraform/OpenTofu CLI.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/api"
	"github.com/HellsCrimson/ArcLoom/internal/apikey"
	"github.com/HellsCrimson/ArcLoom/internal/approvals"
	"github.com/HellsCrimson/ArcLoom/internal/audit"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/notify"
	"github.com/HellsCrimson/ArcLoom/internal/oauth"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/runs"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/session"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/user"
	"github.com/HellsCrimson/ArcLoom/internal/webui"

	"github.com/google/uuid"
)

// Build metadata, injected by GoReleaser via -ldflags -X.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("arcloom %s", version)
			if commit != "" {
				fmt.Printf(" (%s)", commit)
			}
			if date != "" {
				fmt.Printf(" built %s", date)
			}
			fmt.Println()
			return
		case "rekey":
			// Offline master-key rotation (see rekey.go). Runs instead of the
			// server, so nothing is reading the stores while they are re-sealed.
			os.Exit(rekeyCmd(os.Args[2:]))
		}
	}

	cfg := config.Load()
	setupLogging(cfg)

	// Webhook destinations: loopback is refused unless the operator says their
	// notification service really does run there (network_mode: host).
	notify.AllowLoopback(cfg.NotifyAllowLoopback)

	// Build a runner per engine that resolves on PATH. Engines that are not
	// installed are skipped rather than fatal.
	runners := map[model.Engine]runner.Runner{}
	sandbox := engineSandbox(cfg)
	for _, eng := range []model.Engine{model.EngineTofu, model.EngineTerraform} {
		r, err := runner.New(eng, "", cfg.WorkDir, cfg.PluginCacheDir)
		if err != nil {
			slog.Warn("engine unavailable", "engine", eng, "err", err)
			continue
		}
		// Contain the child processes before anything can run one. A failure
		// here is fatal only in "require" mode, which is the point of that mode.
		if err := r.UseSandbox(sandbox); err != nil {
			fatal("engine sandbox", "engine", eng, "err", err)
		}
		defer r.Close()
		runners[eng] = r
		slog.Info("engine ready", "engine", eng, "path", r.ExecPath(), "sandboxed", r.Sandboxed())
	}
	if len(runners) == 0 {
		fatal("no engines available: install tofu or terraform")
	}
	if _, ok := runners[model.Engine(cfg.DefaultEngine)]; !ok {
		// Fall back the default to whatever is available.
		for eng := range runners {
			cfg.DefaultEngine = string(eng)
			break
		}
		slog.Info("default engine adjusted", "engine", cfg.DefaultEngine)
	}

	st, err := store.NewDisk(cfg.DataDir, hclgen.GenerateFiles)
	if err != nil {
		fatal("init store", "err", err)
	}
	slog.Info("projects store ready", "dir", cfg.DataDir)
	sc := cache.New(cfg.CacheDir)

	// Refuse to seal anything under a key that cannot protect it. Checked before
	// the store is built, so a bad key is a clean startup failure rather than
	// data encrypted under it.
	if err := secrets.ValidateMasterKey(cfg.SecretKey); err != nil {
		fatal("ARCLOOM_SECRET_KEY rejected", "err", err)
	}
	sec := secrets.NewStore(cfg.SecretsDir, cfg.SecretKey)

	runHist, err := runs.NewDisk(cfg.RunsDir)
	if err != nil {
		fatal("init run history", "err", err)
	}
	auditLog, err := audit.NewDisk(cfg.AuditDir)
	if err != nil {
		fatal("init audit log", "err", err)
	}
	approvalStore, err := approvals.NewDisk(cfg.ApprovalsDir)
	if err != nil {
		fatal("init approvals", "err", err)
	}
	// Scoped API keys: the non-browser credential (CI, scripts, MCP clients).
	apiKeys, err := apikey.NewDisk(cfg.APIKeysDir)
	if err != nil {
		fatal("init api keys", "err", err)
	}
	// OAuth authorization server for MCP clients that authenticate by consent
	// rather than a pasted key.
	var oauthStore oauth.Store
	if cfg.OAuthEnabled {
		od, err := oauth.NewDisk(cfg.OAuthDir)
		if err != nil {
			fatal("init oauth store", "err", err)
		}
		oauthStore = od
		// Expired codes and access tokens accumulate otherwise; grants are kept
		// until revoked or expired on use.
		go func() {
			t := time.NewTicker(time.Hour)
			defer t.Stop()
			for range t.C {
				_ = od.GC()
			}
		}()
	}
	if sec.LocalEnabled() {
		slog.Info("local secret store enabled", "dir", cfg.SecretsDir)
	} else {
		slog.Info("local secret store disabled (set ARCLOOM_SECRET_KEY); env secrets still work")
	}

	// Authentication: a user store (password accounts) + a server-side session
	// store, both persisted under the data volume. LocalProvider is the only
	// provider in slice 1; OIDC SSO is appended here in slice 2.
	users, err := user.NewDisk(cfg.UsersDir)
	if err != nil {
		fatal("init user store", "err", err)
	}
	sessions, err := session.NewDisk(cfg.SessionsDir)
	if err != nil {
		fatal("init session store", "err", err)
	}
	if cfg.AuthEnabled {
		if err := bootstrapAdmin(users, cfg); err != nil {
			fatal("auth bootstrap", "err", err)
		}
		if err := ensureAdmin(users, cfg); err != nil {
			fatal("ensure admin", "err", err)
		}
		slog.Info("authentication enabled", "usersDir", cfg.UsersDir)
		// Periodically purge expired sessions.
		go func() {
			t := time.NewTicker(time.Hour)
			defer t.Stop()
			for range t.C {
				_ = sessions.GC()
			}
		}()
	} else {
		slog.Warn("authentication disabled (ARCLOOM_AUTH=false); the API is open")
	}
	// App-level settings (runtime-editable SSO). Seed from the ARCLOOM_OIDC_* env
	// vars on first run so existing env-based deployments migrate automatically;
	// after that the persisted settings are the source of truth.
	set, err := settings.NewStore(cfg.SettingsFile, cfg.SecretKey)
	if err != nil {
		fatal("init settings", "err", err)
	}
	if cfg.OIDCEnabled() && !set.SSO().Enabled {
		if err := set.SetSSO(settings.SSO{
			Enabled: true, Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret, RedirectURL: cfg.OIDCRedirectURL, Scopes: cfg.OIDCScopes,
		}); err != nil {
			slog.Warn("could not seed SSO settings from env", "err", err)
		} else {
			slog.Info("seeded SSO settings from ARCLOOM_OIDC_* env")
		}
	}

	// Initial Authenticator is local-only; ReloadAuth then adds OIDC from the
	// persisted settings (bounded discovery; failure is non-fatal).
	srv := api.New(cfg, runners, st, sc, sec, webui.FS(), auth.New(auth.NewLocalProvider(users)), sessions, users, set, runHist, auditLog, approvalStore, apiKeys, oauthStore)
	srv.SetBuildInfo(api.BuildInfo{Version: version, Commit: commit, Date: date})
	rctx, rcancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := srv.ReloadAuth(rctx); err != nil {
		slog.Warn("OIDC SSO disabled", "err", err)
	} else if set.SSO().Enabled {
		slog.Info("OIDC SSO enabled", "issuer", set.SSO().Issuer)
	}
	rcancel()

	// Point the secret store at the configured backend (external store or the
	// local encrypted files). A broken external config is non-fatal but loud:
	// the store stays local-less rather than silently serving stale secrets, so
	// secret reads fail until it is fixed.
	if err := srv.ReloadSecretStore(); err != nil {
		slog.Error("external secret store unavailable; secret reads will fail until it is fixed", "err", err)
	}

	// Scheduled drift checks (refresh-only plans) for projects that enabled
	// them; stopped on shutdown so no plan outlives the server.
	driftCtx, driftCancel := context.WithCancel(context.Background())
	go srv.StartDriftScheduler(driftCtx)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second, // Slowloris guard on headers
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB header cap
		// No ReadTimeout/WriteTimeout: the plan/apply SSE streams are long-lived
		// on the write side, and request bodies are separately capped by the
		// limitBody middleware.
	}

	go func() {
		slog.Info("listening", "version", version, "addr", cfg.Addr, "defaultEngine", cfg.DefaultEngine)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal("server error", "err", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	driftCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	slog.Info("shut down")
}

// setupLogging installs the process-wide slog handler (text or JSON) at the
// configured level, and routes the standard log package through it so panic
// dumps and any third-party log.Print output stays structured and honors the
// same level.
func setupLogging(cfg config.Config) {
	var level slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(cfg.LogFormat, "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
	log.SetFlags(0)
	log.SetOutput(stdlogBridge{})
}

// stdlogBridge forwards standard-library log output into slog so nothing bypasses
// the configured handler.
type stdlogBridge struct{}

func (stdlogBridge) Write(p []byte) (int, error) {
	slog.Error("stdlog", "msg", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// engineSandbox describes what an engine child process may reach.
//
// The Hide list is ArcLoom's own state, and it is assembled here — the one place
// that already knows every path — rather than inside internal/runner, which
// should not have to learn the server's directory layout. Everything on it is
// something a `local-exec` provisioner would otherwise be able to read straight
// off disk: the encrypted secret stores, the password hashes and live session
// tokens that would let it log in as anyone, the API keys and OAuth grants it
// could authenticate with, and every OTHER project's configuration and state.
//
// The run workspace deliberately is NOT hidden — it lives inside DataDir, and
// the shim binds it back after the mask, so a run sees its own project and no
// other. PluginCacheDir must stay writable or every init re-downloads.
func engineSandbox(cfg config.Config) runner.Sandbox {
	return runner.Sandbox{
		Mode: cfg.Sandbox,
		HideDirs: []string{
			cfg.DataDir,      // every project's model + generated HCL
			cfg.SecretsDir,   // the encrypted per-project secret stores
			cfg.UsersDir,     // password hashes
			cfg.SessionsDir,  // live session tokens
			cfg.APIKeysDir,   // API key hashes
			cfg.OAuthDir,     // OAuth clients, grants and access tokens
			cfg.AuditDir,     // the record of what happened
			cfg.RunsDir,      // past run logs, which can quote provider errors
			cfg.ApprovalsDir, // saved plan artifacts (they embed variable values)
		},
		HideFiles: []string{
			cfg.SettingsFile, // SSO client secret, git tokens, Vault token (encrypted)
		},
		Writable: []string{cfg.PluginCacheDir, cfg.WorkDir},
	}
}

// fatal logs an error record and exits non-zero — the slog replacement for
// log.Fatal (which slog has no direct equivalent for).
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// bootstrapAdmin seeds the first admin user from ARCLOOM_ADMIN_PASSWORD on first
// run. If any user already exists the env is ignored (idempotent restarts, no
// lockout-reset surprise). With no users and no admin password it fails closed:
// an IDE that can run `apply` must never start open by accident.
func bootstrapAdmin(users user.Store, cfg config.Config) error {
	existing, err := users.List()
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}
	if cfg.AdminPassword == "" {
		return fmt.Errorf("no users exist: set ARCLOOM_ADMIN_PASSWORD to seed the first admin, or ARCLOOM_AUTH=false to run open")
	}
	hash, err := auth.Hash(cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	u := &user.User{
		ID:           uuid.NewString(),
		Username:     cfg.AdminUsername,
		PasswordHash: hash,
		Provider:     auth.ProviderLocal,
		CreatedAt:    time.Now(),
		Admin:        true,
	}
	if err := users.Create(u); err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	slog.Info("seeded admin user", "username", cfg.AdminUsername)
	return nil
}

// ensureAdmin guarantees at least one admin exists (an upgrade path for stores
// created before the admin flag): if none is marked admin, it promotes the user
// named by ARCLOOM_ADMIN_USERNAME, or the sole user if there is exactly one.
func ensureAdmin(users user.Store, cfg config.Config) error {
	all, err := users.List()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}
	for _, u := range all {
		if u.Admin {
			return nil
		}
	}
	var target string
	for _, u := range all {
		if u.Username == cfg.AdminUsername {
			target = u.ID
		}
	}
	if target == "" && len(all) == 1 {
		target = all[0].ID
	}
	if target == "" {
		slog.Warn("no admin user exists; set one via ARCLOOM_ADMIN_USERNAME")
		return nil
	}
	if err := users.Update(target, func(u *user.User) error { u.Admin = true; return nil }); err != nil {
		return err
	}
	slog.Info("promoted existing user to admin")
	return nil
}
