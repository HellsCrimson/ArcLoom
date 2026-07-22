package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/license"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
)

// ReloadAuth rebuilds the Authenticator from the persisted settings: always the
// local password provider, plus an OIDC provider when SSO is configured. It
// installs whatever it can (so local login always works) and returns any OIDC
// construction error for the caller to surface. Safe to call at boot and at
// runtime after a settings change.
func (s *Server) ReloadAuth(ctx context.Context) error {
	providers := []auth.Provider{auth.NewLocalProvider(s.users)}
	sso := s.settings.SSO()
	var oidcErr error
	// SSO is an enterprise feature: only stand up the OIDC provider when the
	// license grants it, so an unlicensed install never advertises or accepts it.
	if sso.Enabled && sso.Issuer != "" && sso.ClientID != "" && s.license.Has(license.FeatureSSO) {
		p, err := auth.NewOIDCProvider(ctx, sso.Issuer, sso.ClientID, sso.ClientSecret, sso.RedirectURL, strings.Fields(sso.Scopes))
		if err != nil {
			oidcErr = err
		} else {
			providers = append(providers, p)
		}
	}
	s.setAuth(auth.New(providers...))
	return oidcErr
}

// ssoDTO is the SSO settings payload. The client secret is never returned; the
// UI only learns whether one is set.
type ssoDTO struct {
	Enabled     bool   `json:"enabled"`
	Issuer      string `json:"issuer"`
	ClientID    string `json:"clientId"`
	RedirectURL string `json:"redirectUrl"`
	Scopes      string `json:"scopes"`
	HasSecret   bool   `json:"hasSecret"`
	// SecretStorable is false when ARCLOOM_SECRET_KEY is unset (no encryption).
	SecretStorable bool `json:"secretStorable"`
	// Active reports whether the OIDC provider is currently live.
	Active bool `json:"active"`
}

func (s *Server) ssoResponse() ssoDTO {
	sso := s.settings.SSO()
	_, active := s.authn().Redirect(auth.ProviderOIDC)
	return ssoDTO{
		Enabled:        sso.Enabled,
		Issuer:         sso.Issuer,
		ClientID:       sso.ClientID,
		RedirectURL:    sso.RedirectURL,
		Scopes:         sso.Scopes,
		HasSecret:      s.settings.HasSecret(),
		SecretStorable: s.settings.SecretStorable(),
		Active:         active,
	}
}

func (s *Server) handleGetSSO(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.ssoResponse())
}

type ssoUpdateRequest struct {
	Enabled      bool   `json:"enabled"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"` // omit/empty to keep the stored one
	RedirectURL  string `json:"redirectUrl"`
	Scopes       string `json:"scopes"`
}

// handleSetSSO validates the OIDC config (by attempting discovery when enabled),
// persists it, and hot-swaps the live provider. Admin only.
func (s *Server) handleSetSSO(w http.ResponseWriter, r *http.Request) {
	if !s.csrfSafe(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req ssoUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Issuer = strings.TrimSpace(req.Issuer)
	req.ClientID = strings.TrimSpace(req.ClientID)

	if req.Enabled {
		if !s.license.Has(license.FeatureSSO) {
			writeErr(w, http.StatusForbidden, "SSO (OIDC) requires an ArcLoom Enterprise license")
			return
		}
		if req.Issuer == "" || req.ClientID == "" {
			writeErr(w, http.StatusBadRequest, "issuer and client ID are required to enable SSO")
			return
		}
		// Validate discovery with the effective secret (new one, or the stored
		// one when the field was left blank) before persisting.
		secret := req.ClientSecret
		if secret == "" {
			secret = s.settings.SSO().ClientSecret
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		_, err := auth.NewOIDCProvider(ctx, req.Issuer, req.ClientID, secret, req.RedirectURL, strings.Fields(req.Scopes))
		cancel()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "could not connect to the identity provider: "+err.Error())
			return
		}
	}

	if err := s.settings.SetSSO(settings.SSO{
		Enabled:      req.Enabled,
		Issuer:       req.Issuer,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		RedirectURL:  req.RedirectURL,
		Scopes:       req.Scopes,
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ReloadAuth(r.Context()); err != nil {
		// Persisted, but the provider failed to come up (rare after validation).
		writeErr(w, http.StatusInternalServerError, "saved but could not activate: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ssoResponse())
}
