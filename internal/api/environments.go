package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// (identRe — the HCL-identifier matcher — is defined in resources.go and reused
// here for backend-config keys and variable names.)

// handleGetEnvironments returns a project's deployment environments (empty list
// when none are configured). Any reader of the project may see them.
func (s *Server) handleGetEnvironments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	envs := s.settings.Environments(id)
	if envs == nil {
		envs = []settings.Environment{}
	}
	writeJSON(w, http.StatusOK, envs)
}

// handleSetEnvironments replaces a project's environment list (resources perm).
// An empty list clears it (the project reverts to the single default workspace).
// Names must be safe path segments (they become run/envs/<name>/ directories);
// backend-config keys and variable names must be identifiers.
func (s *Server) handleSetEnvironments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(id); err != nil {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	var req struct {
		Environments []settings.Environment `json:"environments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	seen := map[string]bool{}
	cleaned := make([]settings.Environment, 0, len(req.Environments))
	for _, e := range req.Environments {
		if !validID(e.Name) {
			writeErr(w, http.StatusBadRequest, "invalid environment name "+strconv.Quote(e.Name)+" (letters, digits, _ and - only)")
			return
		}
		if seen[e.Name] {
			writeErr(w, http.StatusBadRequest, "duplicate environment "+strconv.Quote(e.Name))
			return
		}
		seen[e.Name] = true
		for k := range e.BackendConfig {
			if !identRe.MatchString(k) {
				writeErr(w, http.StatusBadRequest, "invalid backend config key "+strconv.Quote(k))
				return
			}
		}
		for k := range e.Vars {
			if !identRe.MatchString(k) {
				writeErr(w, http.StatusBadRequest, "invalid variable name "+strconv.Quote(k))
				return
			}
		}
		cleaned = append(cleaned, settings.Environment{Name: e.Name, BackendConfig: e.BackendConfig, Vars: e.Vars})
	}
	if err := s.settings.SetEnvironments(id, cleaned); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	handleEnvironmentsResponse(w, cleaned)
}

func handleEnvironmentsResponse(w http.ResponseWriter, envs []settings.Environment) {
	if envs == nil {
		envs = []settings.Environment{}
	}
	writeJSON(w, http.StatusOK, envs)
}

// resolveEnv looks up the environment named by the ?env= query value for a
// deploy/state request. An empty name selects the default (unnamed) workspace
// with no overrides. A non-empty name that is not configured is an error, so a
// stale UI can't silently deploy to the default workspace.
func (s *Server) resolveEnv(projectID string, r *http.Request) (name string, env settings.Environment, err error) {
	name = r.URL.Query().Get("env")
	if name == "" {
		return "", settings.Environment{}, nil
	}
	if !validID(name) {
		return name, settings.Environment{}, fmt.Errorf("invalid environment name")
	}
	e, ok := s.settings.Environment(projectID, name)
	if !ok {
		return name, settings.Environment{}, fmt.Errorf("unknown environment %q", name)
	}
	return name, e, nil
}

// backendConfigFileContents renders the partial backend configuration handed to
// `init -backend-config` as an HCL `.tfbackend` body (`key = <literal>` lines).
// It carries two things: `secrets` (backend settings bound to a secret,
// already resolved to plaintext — a backend block cannot reference a variable,
// so this file is the only way a credential stays out of the generated HCL)
// overlaid with the environment's own values, which win because an env exists
// precisely to override the shared config. Returns nil when there is nothing to
// write, which is what makes the runner skip -backend-config entirely.
func backendConfigFileContents(env settings.Environment, secrets map[string]string) []byte {
	// Values are stored already rendered. A secret is always quoted: it is an
	// opaque credential, and hclLiteral's bare-token heuristic would turn a
	// token that happens to read as `true` or `01234` into a bool or a number.
	vals := make(map[string]string, len(secrets)+len(env.BackendConfig))
	for k, v := range secrets {
		vals[k] = strconv.Quote(v)
	}
	for k, v := range env.BackendConfig {
		vals[k] = hclLiteral(v)
	}
	if len(vals) == 0 {
		return nil
	}
	// Deterministic order for a stable file (avoids needless workspace churn).
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = append(b, []byte(k+" = "+vals[k]+"\n")...)
	}
	return b
}

// hclLiteral renders a user-supplied backend-config value as an HCL literal:
// bare booleans and numbers pass through; everything else is a quoted string.
func hclLiteral(v string) string {
	if v == "true" || v == "false" {
		return v
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v
	}
	return strconv.Quote(v)
}
