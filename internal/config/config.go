// Package config holds runtime configuration resolved from environment variables.
package config

import (
	"os"
	"path/filepath"
)

// Config is the resolved runtime configuration for the ArcLoom server.
type Config struct {
	// Addr is the listen address for the HTTP server, e.g. ":8080".
	Addr string
	// DefaultEngine is the engine ("tofu" or "terraform") used when a project
	// does not specify one.
	DefaultEngine string
	// WorkDir is the base directory under which temporary Terraform/OpenTofu
	// workspaces are created.
	WorkDir string
	// CacheDir is where provider plugins and normalized schemas are cached.
	CacheDir string
	// PluginCacheDir is exported to the CLI as TF_PLUGIN_CACHE_DIR so provider
	// plugins are downloaded once and shared across workspaces.
	PluginCacheDir string
	// DataDir holds one folder per project (arcloom.yaml + generated/*.tf).
	DataDir string
	// SecretsDir holds the encrypted per-project secret stores (never in Git).
	SecretsDir string
	// SecretKey is the master passphrase for the local encrypted secrets
	// backend (from ARCLOOM_SECRET_KEY); empty disables local storage.
	SecretKey string
}

// Load reads configuration from the environment, applying sensible defaults.
func Load() Config {
	cacheDir := env("ARCLOOM_CACHE_DIR", defaultCacheDir())
	dataDir := env("ARCLOOM_DATA", defaultDataDir())
	return Config{
		Addr:           env("ARCLOOM_ADDR", ":8080"),
		DefaultEngine:  env("ARCLOOM_ENGINE", "tofu"),
		WorkDir:        env("ARCLOOM_WORKDIR", filepath.Join(os.TempDir(), "arcloom", "work")),
		CacheDir:       cacheDir,
		PluginCacheDir: env("TF_PLUGIN_CACHE_DIR", filepath.Join(cacheDir, "plugins")),
		DataDir:        dataDir,
		SecretsDir:     env("ARCLOOM_SECRETS_DIR", filepath.Join(filepath.Dir(dataDir), "secrets")),
		SecretKey:      os.Getenv("ARCLOOM_SECRET_KEY"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "arcloom")
	}
	return filepath.Join(os.TempDir(), "arcloom", "cache")
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "arcloom", "projects")
	}
	return filepath.Join(os.TempDir(), "arcloom", "projects")
}
