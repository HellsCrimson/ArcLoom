package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/HellsCrimson/ArcLoom/internal/envvar"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// Per-project environment variables for engine runs.
//
// They exist because a provider's own environment variables are frequently the
// documented way to give it credentials, and the only injection ArcLoom had was
// TF_VAR_<name> — which feeds `variable` blocks and nothing else. Setting them
// on the ArcLoom process worked but was instance-wide: two projects pointing at
// different clusters could not each have their own OPENNEBULA_ENDPOINT.
//
// A value is a literal or a secret reference. Literals are stored in the clear
// (settings.yaml, like Environment.Vars), so anything sensitive belongs in the
// secret store — which is also what keeps it out of Git and lets it be rotated
// in one place.

// envVarDTO is one row on the wire. A value is never returned for a
// secret-backed row (there is nothing stored to return), and a literal is
// returned as-is — it was never a secret to begin with.
type envVarDTO struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	SecretRef string `json:"secretRef,omitempty"`
}

type envVarsDTO struct {
	Vars []envVarDTO `json:"vars"`
	// EnvVars maps environment name -> that environment's overrides, layered
	// over Vars by name at run time.
	EnvVars map[string][]envVarDTO `json:"envVars,omitempty"`
	// Blocked lists the names this server refuses, so the UI can say why before
	// a save fails rather than after.
	Blocked []string `json:"blocked,omitempty"`
}

func toEnvVarDTOs(in []envvar.Var) []envVarDTO {
	out := make([]envVarDTO, 0, len(in))
	for _, v := range in {
		out = append(out, envVarDTO{Name: v.Name, Value: v.Value, SecretRef: v.SecretRef})
	}
	return out
}

func fromEnvVarDTOs(in []envVarDTO) []envvar.Var {
	out := make([]envvar.Var, 0, len(in))
	for _, v := range in {
		out = append(out, envvar.Var{Name: v.Name, Value: v.Value, SecretRef: v.SecretRef})
	}
	return out
}

// handleGetEnvVars returns a project's environment variables plus the per-env
// overrides, so one screen shows the whole picture.
func (s *Server) handleGetEnvVars(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	dto := envVarsDTO{
		Vars:    toEnvVarDTOs(s.settings.EnvVars(id)),
		Blocked: envvar.BlockedNames(),
	}
	for _, env := range s.settings.Environments(id) {
		if len(env.EnvVars) == 0 {
			continue
		}
		if dto.EnvVars == nil {
			dto.EnvVars = map[string][]envVarDTO{}
		}
		dto.EnvVars[env.Name] = toEnvVarDTOs(env.EnvVars)
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleSetEnvVars replaces a project's environment variables, and optionally
// one environment's overrides (?env=).
//
// Names are validated here — the refusal list is a privilege boundary, not
// tidiness: these become environment variables of a process the SERVER spawns,
// and the permission to edit them is `resources`, which is not admin.
func (s *Server) handleSetEnvVars(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Vars []envVarDTO `json:"vars"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	vars, err := envvar.Normalize(fromEnvVarDTOs(req.Vars))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// ?env= targets one environment's overrides instead of the project's rows.
	if name := r.URL.Query().Get("env"); name != "" {
		envs := s.settings.Environments(id)
		found := false
		for i := range envs {
			if envs[i].Name == name {
				envs[i].EnvVars = vars
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, "unknown environment "+name)
			return
		}
		if err := s.settings.SetEnvironments(id, envs); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, envVarsDTO{Vars: toEnvVarDTOs(vars)})
		return
	}

	if err := s.settings.SetEnvVars(id, vars); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, envVarsDTO{Vars: toEnvVarDTOs(vars)})
}

// resolveEnvVars turns a project's stored rows (plus the active environment's
// overrides) into the variables a run sets. Secret-backed rows go through
// Store.Resolve — the same path a plan's TF_VAR_* secrets take — so a value is
// never persisted in settings.yaml and a broken remote store fails loudly
// instead of silently running with a stale credential.
//
// A missing secret is reported through onMissing and the variable is OMITTED
// rather than set to "": an empty credential makes a provider fail with
// "authentication failed", which sends the user looking in the wrong place,
// while an absent one usually produces the provider's own "no credentials"
// message naming what it wanted.
func (s *Server) resolveEnvVars(projectID string, env settings.Environment, onMissing func(name, ref string)) (map[string]string, error) {
	rows := envvar.Merge(s.settings.EnvVars(projectID), env.EnvVars)
	if len(rows) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(rows))
	for _, v := range rows {
		// Re-check on the way out: a name stored before a rule existed (or by an
		// older binary) must not reach the process environment.
		if err := envvar.Validate(v.Name); err != nil {
			return nil, fmt.Errorf("environment variable %s: %w", v.Name, err)
		}
		if v.SecretRef == "" {
			out[v.Name] = v.Value
			continue
		}
		val, found, err := s.secrets.Resolve(projectID, v.SecretRef)
		if err != nil {
			return nil, fmt.Errorf("environment variable %s: secret %s: %w", v.Name, v.SecretRef, err)
		}
		if !found {
			onMissing(v.Name, v.SecretRef)
			continue
		}
		out[v.Name] = val
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
