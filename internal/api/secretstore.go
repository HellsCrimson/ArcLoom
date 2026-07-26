package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/secrets"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// vaultTokenEnv lets an operator keep the Vault token out of the settings file
// entirely (container-friendly, and rotating it needs no settings edit). The
// stored token wins when both are present.
const vaultTokenEnv = "ARCLOOM_VAULT_TOKEN"

// ReloadSecretStore points the secret store at whatever the settings select: an
// external backend, or the local encrypted files. Safe at boot and at runtime —
// the settings page swaps the backend without a restart, like ReloadAuth.
//
// It installs what it can and returns the construction error for the caller to
// surface: a broken remote config must NOT silently fall back to local, or a plan
// would run against secrets from a store the operator moved away from. On error
// the remote is left cleared, so secret reads fail loudly instead.
func (s *Server) ReloadSecretStore() error {
	if s.secrets == nil {
		return nil
	}
	cfg := s.settings.SecretStore()
	if cfg.Kind != settings.SecretStoreVault {
		s.secrets.SetRemote(nil)
		return nil
	}
	// Enterprise gate, enforced here as well as in the handler: an expired license
	// must not keep an external store alive across a restart.
	if !s.license.Has(license.FeatureVault) {
		s.secrets.SetRemote(nil)
		slog.Warn("external secret store configured but not licensed; using local secrets")
		return nil
	}
	b, err := vaultFromSettings(cfg)
	if err != nil {
		s.secrets.SetRemote(nil)
		return err
	}
	s.secrets.SetRemote(b)
	slog.Info("external secret store ready", "backend", b.Name(), "target", b.Description())
	return nil
}

// vaultFromSettings builds the backend, filling the token from the environment
// when the settings hold none.
func vaultFromSettings(cfg settings.SecretStore) (*secrets.Vault, error) {
	token := cfg.Token
	if token == "" {
		token = strings.TrimSpace(os.Getenv(vaultTokenEnv))
	}
	return secrets.NewVault(secrets.VaultConfig{
		Address:            cfg.Address,
		Token:              token,
		Mount:              cfg.Mount,
		Prefix:             cfg.Prefix,
		Namespace:          cfg.Namespace,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	})
}

// secretStoreDTO is the settings payload. The token is never returned — only
// whether one is on file, and from where.
type secretStoreDTO struct {
	Kind               string `json:"kind"`
	Address            string `json:"address"`
	Mount              string `json:"mount"`
	Prefix             string `json:"prefix"`
	Namespace          string `json:"namespace"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	// TokenSet is true when a token is stored; TokenFromEnv when ARCLOOM_VAULT_TOKEN
	// is providing it instead.
	TokenSet     bool `json:"tokenSet"`
	TokenFromEnv bool `json:"tokenFromEnv"`
	// Active is the backend actually serving secrets right now ("vault", "local"
	// or "" when nothing is writable), so a config that failed to come up is
	// visible rather than implied.
	Active string `json:"active"`
	// LocalEnabled reports whether ARCLOOM_SECRET_KEY is set, i.e. whether "local"
	// is a usable choice (and whether a token can be encrypted at rest).
	LocalEnabled bool `json:"localEnabled"`
	// Licensed reports whether an external store may be enabled at all.
	Licensed bool `json:"licensed"`
}

func (s *Server) secretStoreResponse() secretStoreDTO {
	cfg := s.settings.SecretStore()
	kind := cfg.Kind
	if kind == "" {
		kind = settings.SecretStoreLocal
	}
	return secretStoreDTO{
		Kind:               kind,
		Address:            cfg.Address,
		Mount:              cfg.Mount,
		Prefix:             cfg.Prefix,
		Namespace:          cfg.Namespace,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		TokenSet:           s.settings.SecretStoreTokenSet(),
		TokenFromEnv:       os.Getenv(vaultTokenEnv) != "",
		Active:             s.secrets.BackendName(),
		LocalEnabled:       s.secrets.LocalEnabled(),
		Licensed:           s.license.Has(license.FeatureVault),
	}
}

func (s *Server) handleGetSecretStore(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.secretStoreResponse())
}

type secretStoreRequest struct {
	Kind               string `json:"kind"`
	Address            string `json:"address"`
	Mount              string `json:"mount"`
	Prefix             string `json:"prefix"`
	Namespace          string `json:"namespace"`
	Token              string `json:"token"` // omit/empty keeps the stored one
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	// Migrate copies the secrets ArcLoom currently holds into the new store
	// before switching, so the values a project references keep resolving.
	Migrate bool `json:"migrate"`
}

// handleSetSecretStore validates the backend (by probing it), optionally migrates
// the existing secrets into it, then persists and hot-swaps. Admin only.
//
// Selecting an external store is enterprise-gated; selecting local never is, so an
// expired license can always be walked back.
func (s *Server) handleSetSecretStore(w http.ResponseWriter, r *http.Request) {
	if !s.csrfSafe(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req secretStoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Kind == "" {
		req.Kind = settings.SecretStoreLocal
	}
	if req.Kind != settings.SecretStoreLocal && req.Kind != settings.SecretStoreVault {
		writeErr(w, http.StatusBadRequest, "unknown secret store kind "+req.Kind)
		return
	}

	if req.Kind == settings.SecretStoreLocal {
		if err := s.settings.SetSecretStore(settings.SecretStore{Kind: settings.SecretStoreLocal}); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.ReloadSecretStore(); err != nil {
			writeErr(w, http.StatusInternalServerError, "saved but could not activate: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.secretStoreResponse())
		return
	}

	if !s.license.Has(license.FeatureVault) {
		writeErr(w, http.StatusForbidden, "an external secret store requires an ArcLoom Enterprise license")
		return
	}
	cfg := settings.SecretStore{
		Kind: settings.SecretStoreVault, Address: req.Address, Mount: req.Mount,
		Prefix: req.Prefix, Namespace: req.Namespace, Token: req.Token,
		InsecureSkipVerify: req.InsecureSkipVerify,
	}
	// Probe with the effective token (the new one, or the stored/env one when the
	// field was left blank) BEFORE persisting: a config that cannot be reached
	// would otherwise leave the instance unable to read any secret.
	effective := cfg
	if effective.Token == "" {
		effective.Token = s.settings.SecretStore().Token
	}
	backend, err := vaultFromSettings(effective)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := backend.Check(ctx); err != nil {
		writeErr(w, http.StatusBadRequest, "could not use that secret store: "+err.Error())
		return
	}

	// Migrate before switching, while the OLD backend is still the live one.
	migrated := 0
	if req.Migrate {
		ids, err := s.projectIDs()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		migrated, err = s.secrets.MigrateTo(ctx, backend, ids)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "migration failed after copying "+strconv.Itoa(migrated)+" secret(s): "+err.Error())
			return
		}
	}

	if err := s.settings.SetSecretStore(cfg); err != nil {
		if errors.Is(err, settings.ErrSecretKeyRequired) {
			// The shared message names the OIDC secret; say what applies here, and
			// point at the alternative that needs no key at all.
			writeErr(w, http.StatusBadRequest,
				"storing the token needs ARCLOOM_SECRET_KEY (to encrypt it at rest) — or leave the token blank and provide it as "+vaultTokenEnv)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ReloadSecretStore(); err != nil {
		writeErr(w, http.StatusInternalServerError, "saved but could not activate: "+err.Error())
		return
	}
	resp := struct {
		secretStoreDTO
		Migrated int `json:"migrated"`
	}{s.secretStoreResponse(), migrated}
	writeJSON(w, http.StatusOK, resp)
}

// handleTestSecretStore probes a candidate configuration without saving it, so
// the settings page can validate before committing. Admin only.
func (s *Server) handleTestSecretStore(w http.ResponseWriter, r *http.Request) {
	if !s.csrfSafe(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req secretStoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := settings.SecretStore{
		Address: req.Address, Mount: req.Mount, Prefix: req.Prefix,
		Namespace: req.Namespace, Token: req.Token, InsecureSkipVerify: req.InsecureSkipVerify,
	}
	if cfg.Token == "" {
		cfg.Token = s.settings.SecretStore().Token
	}
	backend, err := vaultFromSettings(cfg)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := backend.Check(ctx); err != nil {
		// 502: the request was well-formed, the upstream store refused it.
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "target": backend.Description()})
}

// projectIDs lists every project id, for a store migration.
func (s *Server) projectIDs() ([]string, error) {
	projects, err := s.store.List()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	return ids, nil
}
