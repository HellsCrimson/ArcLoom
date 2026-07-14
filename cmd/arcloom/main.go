// Command arcloom is the ArcLoom server: it serves the REST/SSE API and the
// embedded Svelte SPA, wrapping the Terraform/OpenTofu CLI.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/api"
	"github.com/HellsCrimson/ArcLoom/internal/cache"
	"github.com/HellsCrimson/ArcLoom/internal/config"
	"github.com/HellsCrimson/ArcLoom/internal/hclgen"
	"github.com/HellsCrimson/ArcLoom/internal/model"
	"github.com/HellsCrimson/ArcLoom/internal/runner"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/store"
	"github.com/HellsCrimson/ArcLoom/internal/webui"
)

func main() {
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
	if sec.LocalEnabled() {
		log.Printf("local secret store enabled (%s)", cfg.SecretsDir)
	} else {
		log.Printf("local secret store disabled (set ARCLOOM_SECRET_KEY); env secrets still work")
	}

	srv := api.New(cfg, runners, st, sc, sec, webui.FS())

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("ArcLoom listening on %s (default engine: %s)", cfg.Addr, cfg.DefaultEngine)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Println("shut down")
}
