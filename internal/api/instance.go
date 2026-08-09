package api

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/schema"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
)

// BuildInfo is the build metadata main.go injects at link time (-ldflags -X).
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// SetBuildInfo records what this binary was built from, for the instance panel.
func (s *Server) SetBuildInfo(b BuildInfo) { s.build = b }

// toolTimeout bounds each `<tool> --version` probe; the panel must not hang on a
// wedged binary.
const toolTimeout = 3 * time.Second

// optionalTools are the external binaries features degrade without. Reporting
// their presence in one place answers most "why is X not doing anything?"
// questions without shell access to the server.
var optionalTools = []struct{ name, feature string }{
	{"tflint", "Lint"},
	{"conftest", "Policy as code"},
	{"infracost", "Cost estimation (cloud pricing)"},
	{"git", "Git integration (go-git is built in; the CLI is not required)"},
}

type instanceEngine struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	Version           string `json:"version,omitempty"`
	Error             string `json:"error,omitempty"`
	IsResolvedDefault bool   `json:"default"`
}

type instanceTool struct {
	Name      string `json:"name"`
	Feature   string `json:"feature"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

type instanceInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"buildDate,omitempty"`

	Go            string `json:"go"`
	Platform      string `json:"platform"`
	StartedAt     string `json:"startedAt"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	Goroutines    int    `json:"goroutines"`
	HeapMB        int    `json:"heapMB"`

	DefaultEngine string           `json:"defaultEngine"`
	Engines       []instanceEngine `json:"engines"`
	Tools         []instanceTool   `json:"tools"`

	// SchemaFormat is the normalizer's output version; it is part of the schema
	// cache key, so a bump means previously cached schemas are re-fetched.
	SchemaFormat int `json:"schemaFormat"`

	Paths map[string]string `json:"paths"`

	// MCPEnabled reports whether the Model Context Protocol endpoint is mounted,
	// and APIKeyCount how many credentials exist — together they answer "can an
	// agent reach this instance right now", which is not otherwise visible.
	MCPEnabled  bool `json:"mcpEnabled"`
	APIKeyCount int  `json:"apiKeyCount"`

	AuthEnabled   bool     `json:"authEnabled"`
	CookieSecure  bool     `json:"cookieSecure"`
	AuthProviders []string `json:"authProviders"`
	SSOEnabled    bool     `json:"ssoEnabled"`
	SecretStore   bool     `json:"secretStore"`
	// SecretBackend names the store actually serving secrets ("local", "vault",
	// or "" when only env secrets are readable).
	SecretBackend string `json:"secretBackend,omitempty"`
	LogLevel      string `json:"logLevel"`
	LogFormat     string `json:"logFormat"`

	Projects int `json:"projects"`
	Users    int `json:"users"`
}

// handleInstanceInfo reports what this ArcLoom process is and what it can see:
// build metadata, runtime, the engines and optional tools it resolved, where it
// keeps things, and which switches are on. Admin-only, and deliberately free of
// secrets — paths and flags, never keys, tokens or credentials.
func (s *Server) handleInstanceInfo(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	info := instanceInfo{
		Version:       s.build.Version,
		Commit:        s.build.Commit,
		BuildDate:     s.build.Date,
		Go:            runtime.Version(),
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		StartedAt:     s.startedAt.UTC().Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Goroutines:    runtime.NumGoroutine(),
		HeapMB:        int(mem.HeapAlloc / (1 << 20)),
		DefaultEngine: s.cfg.DefaultEngine,
		SchemaFormat:  schema.FormatVersion,
		MCPEnabled:    s.mcpEnabled(),
		APIKeyCount:   s.apiKeyCount(),
		Paths: map[string]string{
			"data":        s.cfg.DataDir,
			"work":        s.cfg.WorkDir,
			"cache":       s.cfg.CacheDir,
			"pluginCache": s.cfg.PluginCacheDir,
			"secrets":     s.cfg.SecretsDir,
			"runs":        s.cfg.RunsDir,
			"audit":       s.cfg.AuditDir,
			"approvals":   s.cfg.ApprovalsDir,
			"apiKeys":     s.cfg.APIKeysDir,
			"users":       s.cfg.UsersDir,
			"sessions":    s.cfg.SessionsDir,
			"settings":    s.cfg.SettingsFile,
		},
		AuthEnabled:   s.cfg.AuthEnabled,
		CookieSecure:  s.cfg.CookieSecure,
		AuthProviders: s.authn().Enabled(),
		SecretStore:   s.secrets != nil && s.secrets.Writable(),
		SecretBackend: secretBackendName(s.secrets),
		LogLevel:      s.cfg.LogLevel,
		LogFormat:     s.cfg.LogFormat,
	}
	if s.settings != nil {
		info.SSOEnabled = s.settings.SSO().Enabled
	}
	if projects, err := s.store.List(); err == nil {
		info.Projects = len(projects)
	}
	if users, err := s.users.List(); err == nil {
		info.Users = len(users)
	}

	for name, run := range s.runners {
		e := instanceEngine{Name: string(name), Path: run.ExecPath(), IsResolvedDefault: string(name) == s.cfg.DefaultEngine}
		ctx, cancel := context.WithTimeout(r.Context(), toolTimeout)
		v, err := run.Version(ctx)
		cancel()
		if err != nil {
			e.Error = firstNonEmptyLine(err.Error())
		} else {
			e.Version = v
		}
		info.Engines = append(info.Engines, e)
	}
	for _, t := range optionalTools {
		info.Tools = append(info.Tools, probeTool(r.Context(), t.name, t.feature))
	}
	writeJSON(w, http.StatusOK, info)
}

// probeTool reports whether an optional binary is on PATH and which version it
// is. Anything that fails is simply "not available" — this is diagnostics, not
// an operation that may error.
func probeTool(ctx context.Context, name, feature string) instanceTool {
	t := instanceTool{Name: name, Feature: feature}
	path, err := exec.LookPath(name)
	if err != nil {
		return t
	}
	t.Available, t.Path = true, path

	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return t
	}
	t.Version = firstNonEmptyLine(string(out))
	return t
}

// firstNonEmptyLine trims a tool's banner down to something displayable.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}

// secretBackendName reports the live secret backend for the instance panel,
// tolerating a nil store (tests construct servers without one).
func secretBackendName(st *secrets.Store) string {
	if st == nil {
		return ""
	}
	return st.BackendName()
}
