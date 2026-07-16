// Package config holds runtime configuration resolved from environment variables.
package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"
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

	// --- Authentication ---

	// AuthEnabled gates the whole /api surface behind a login (ARCLOOM_AUTH,
	// default true). Setting it false is a deliberate escape hatch for trusted
	// single-user LAN setups.
	AuthEnabled bool
	// UsersDir holds one YAML file per user account (never in Git).
	UsersDir string
	// SessionsDir holds server-side login sessions (never in Git).
	SessionsDir string
	// SessionTTL is how long a login session stays valid (ARCLOOM_SESSION_TTL).
	SessionTTL time.Duration
	// CookieName is the session cookie name (fixed).
	CookieName string
	// CookieSecure marks the session cookie Secure (ARCLOOM_COOKIE_SECURE,
	// default true). Set false to allow login over plain HTTP on a trusted LAN.
	CookieSecure bool
	// AdminUsername is the username seeded on first run (ARCLOOM_ADMIN_USERNAME).
	AdminUsername string
	// AdminPassword seeds the first admin's password on first run only
	// (ARCLOOM_ADMIN_PASSWORD). It is hashed into the user store and never used
	// as the live credential; ignored once any user exists.
	AdminPassword string

	// --- OIDC SSO (optional; enabled when Issuer + ClientID are set) ---

	// OIDCIssuer is the identity provider's issuer URL (ARCLOOM_OIDC_ISSUER),
	// e.g. https://accounts.google.com. Empty disables SSO.
	OIDCIssuer string
	// OIDCClientID / OIDCClientSecret are the registered app credentials.
	OIDCClientID     string
	OIDCClientSecret string
	// OIDCRedirectURL is the callback URL registered with the IdP
	// (ARCLOOM_OIDC_REDIRECT_URL), e.g. https://arcloom.example.com/api/auth/sso/oidc/callback.
	OIDCRedirectURL string
	// OIDCScopes are the requested scopes (ARCLOOM_OIDC_SCOPES, space-separated);
	// "openid" is always included.
	OIDCScopes string
}

// OIDCEnabled reports whether OIDC SSO is configured.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != ""
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

		AuthEnabled:   boolEnv("ARCLOOM_AUTH", true),
		UsersDir:      env("ARCLOOM_USERS_DIR", filepath.Join(filepath.Dir(dataDir), "users")),
		SessionsDir:   env("ARCLOOM_SESSIONS_DIR", filepath.Join(filepath.Dir(dataDir), "sessions")),
		SessionTTL:    durEnv("ARCLOOM_SESSION_TTL", 7*24*time.Hour),
		CookieName:    "arcloom_session",
		CookieSecure:  boolEnv("ARCLOOM_COOKIE_SECURE", true),
		AdminUsername: env("ARCLOOM_ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("ARCLOOM_ADMIN_PASSWORD"),

		OIDCIssuer:       os.Getenv("ARCLOOM_OIDC_ISSUER"),
		OIDCClientID:     os.Getenv("ARCLOOM_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("ARCLOOM_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:  os.Getenv("ARCLOOM_OIDC_REDIRECT_URL"),
		OIDCScopes:       os.Getenv("ARCLOOM_OIDC_SCOPES"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// boolEnv reads a boolean env var: "0", "false", "no", "off" (case-insensitive)
// are false; any other non-empty value is true; empty uses def.
func boolEnv(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// durEnv reads a time.Duration env var (e.g. "168h"); empty or unparseable uses
// def.
func durEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
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
