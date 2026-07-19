// Command arcloom is the ArcLoom server: it serves the REST/SSE API and the
// embedded Svelte SPA, wrapping the Terraform/OpenTofu CLI.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/api"
	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
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
		}
	}

	cfg := config.Load()

	// Build a runner per engine that resolves on PATH. Engines that are not
	// installed are skipped rather than fatal.
	runners := map[model.Engine]runner.Runner{}
	for _, eng := range []model.Engine{model.EngineTofu, model.EngineTerraform} {
		r, err := runner.New(eng, "", cfg.WorkDir, cfg.PluginCacheDir)
		if err != nil {
			log.Printf("engine %s unavailable: %v", eng, err)
			continue
		}
		runners[eng] = r
		log.Printf("engine %s -> %s", eng, r.ExecPath())
	}
	if len(runners) == 0 {
		log.Fatal("no engines available: install tofu or terraform")
	}
	if _, ok := runners[model.Engine(cfg.DefaultEngine)]; !ok {
		// Fall back the default to whatever is available.
		for eng := range runners {
			cfg.DefaultEngine = string(eng)
			break
		}
		log.Printf("default engine adjusted to %s", cfg.DefaultEngine)
	}

	st, err := store.NewDisk(cfg.DataDir, hclgen.GenerateFiles)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	log.Printf("projects stored in %s", cfg.DataDir)
	sc := cache.New(cfg.CacheDir)

	sec := secrets.NewStore(cfg.SecretsDir, cfg.SecretKey)

	runHist, err := runs.NewDisk(cfg.RunsDir)
	if err != nil {
		log.Fatalf("init run history: %v", err)
	}
	if sec.LocalEnabled() {
		log.Printf("local secret store enabled (%s)", cfg.SecretsDir)
	} else {
		log.Printf("local secret store disabled (set ARCLOOM_SECRET_KEY); env secrets still work")
	}

	// Authentication: a user store (password accounts) + a server-side session
	// store, both persisted under the data volume. LocalProvider is the only
	// provider in slice 1; OIDC SSO is appended here in slice 2.
	users, err := user.NewDisk(cfg.UsersDir)
	if err != nil {
		log.Fatalf("init user store: %v", err)
	}
	sessions, err := session.NewDisk(cfg.SessionsDir)
	if err != nil {
		log.Fatalf("init session store: %v", err)
	}
	if cfg.AuthEnabled {
		if err := bootstrapAdmin(users, cfg); err != nil {
			log.Fatalf("auth bootstrap: %v", err)
		}
		if err := ensureAdmin(users, cfg); err != nil {
			log.Fatalf("ensure admin: %v", err)
		}
		log.Printf("authentication enabled (users in %s)", cfg.UsersDir)
		// Periodically purge expired sessions.
		go func() {
			t := time.NewTicker(time.Hour)
			defer t.Stop()
			for range t.C {
				_ = sessions.GC()
			}
		}()
	} else {
		log.Printf("WARNING: authentication disabled (ARCLOOM_AUTH=false); the API is open")
	}
	// App-level settings (runtime-editable SSO). Seed from the ARCLOOM_OIDC_* env
	// vars on first run so existing env-based deployments migrate automatically;
	// after that the persisted settings are the source of truth.
	set, err := settings.NewStore(cfg.SettingsFile, cfg.SecretKey)
	if err != nil {
		log.Fatalf("init settings: %v", err)
	}
	if cfg.OIDCEnabled() && !set.SSO().Enabled {
		if err := set.SetSSO(settings.SSO{
			Enabled: true, Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret, RedirectURL: cfg.OIDCRedirectURL, Scopes: cfg.OIDCScopes,
		}); err != nil {
			log.Printf("WARNING: could not seed SSO settings from env: %v", err)
		} else {
			log.Printf("seeded SSO settings from ARCLOOM_OIDC_* env")
		}
	}

	// Initial Authenticator is local-only; ReloadAuth then adds OIDC from the
	// persisted settings (bounded discovery; failure is non-fatal).
	srv := api.New(cfg, runners, st, sc, sec, webui.FS(), auth.New(auth.NewLocalProvider(users)), sessions, users, set, runHist)
	rctx, rcancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := srv.ReloadAuth(rctx); err != nil {
		log.Printf("WARNING: OIDC SSO disabled: %v", err)
	} else if set.SSO().Enabled {
		log.Printf("OIDC SSO enabled (issuer %s)", set.SSO().Issuer)
	}
	rcancel()

	// Scheduled drift checks (refresh-only plans) for projects that enabled
	// them; stopped on shutdown so no plan outlives the server.
	driftCtx, driftCancel := context.WithCancel(context.Background())
	go srv.StartDriftScheduler(driftCtx)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("ArcLoom %s listening on %s (default engine: %s)", version, cfg.Addr, cfg.DefaultEngine)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	driftCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Println("shut down")
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
	log.Printf("seeded admin user %q", cfg.AdminUsername)
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
		log.Printf("WARNING: no admin user exists; set one via ARCLOOM_ADMIN_USERNAME")
		return nil
	}
	if err := users.Update(target, func(u *user.User) error { u.Admin = true; return nil }); err != nil {
		return err
	}
	log.Printf("promoted existing user to admin")
	return nil
}
