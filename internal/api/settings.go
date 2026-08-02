package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/auth"
	"github.com/HellsCrimson/ArcLoom/internal/settings"
	"github.com/HellsCrimson/ArcLoom/internal/user"
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
	if sso.Enabled && sso.Issuer != "" && sso.ClientID != "" {
		p, err := auth.NewOIDCProvider(ctx, sso.Issuer, sso.ClientID, sso.ClientSecret, sso.RedirectURL, ssoScopes(sso), sso.GroupsClaim)
		if err != nil {
			oidcErr = err
		} else {
			providers = append(providers, p)
		}
	}
	s.setAuth(auth.New(providers...))
	return oidcErr
}

// ssoScopes is the scope list to request: the admin's extra scopes, plus
// "groups" once any group→role mapping exists (most IdPs only put the groups
// claim in the ID token when that scope is asked for). Adding no mapping leaves
// the request byte-identical to before, so existing installs are unaffected.
func ssoScopes(sso settings.SSO) []string {
	scopes := strings.Fields(sso.Scopes)
	if len(sso.GroupRoles) == 0 || slices.Contains(scopes, auth.DefaultGroupsClaim) {
		return scopes
	}
	return append(scopes, auth.DefaultGroupsClaim)
}

// ssoDTO is the SSO settings payload. The client secret is never returned; the
// UI only learns whether one is set.
type ssoDTO struct {
	Enabled     bool   `json:"enabled"`
	Issuer      string `json:"issuer"`
	ClientID    string `json:"clientId"`
	RedirectURL string `json:"redirectUrl"`
	Scopes      string `json:"scopes"`
	// GroupsClaim / GroupRoles / SyncGroupsOnLogin drive group→role mapping for
	// provisioned SSO users.
	GroupsClaim       string               `json:"groupsClaim"`
	GroupRoles        []settings.GroupRole `json:"groupRoles"`
	SyncGroupsOnLogin bool                 `json:"syncGroupsOnLogin"`
	HasSecret         bool                 `json:"hasSecret"`
	// SecretStorable is false when ARCLOOM_SECRET_KEY is unset (no encryption).
	SecretStorable bool `json:"secretStorable"`
	// Active reports whether the OIDC provider is currently live.
	Active bool `json:"active"`
}

func (s *Server) ssoResponse() ssoDTO {
	sso := s.settings.SSO()
	_, active := s.authn().Redirect(auth.ProviderOIDC)
	roles := sso.GroupRoles
	if roles == nil {
		roles = []settings.GroupRole{} // the UI binds a table to this; never null
	}
	return ssoDTO{
		Enabled:           sso.Enabled,
		Issuer:            sso.Issuer,
		ClientID:          sso.ClientID,
		RedirectURL:       sso.RedirectURL,
		Scopes:            sso.Scopes,
		GroupsClaim:       sso.GroupsClaim,
		GroupRoles:        roles,
		SyncGroupsOnLogin: sso.SyncGroupsOnLogin,
		HasSecret:         s.settings.HasSecret(),
		SecretStorable:    s.settings.SecretStorable(),
		Active:            active,
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

	GroupsClaim       string               `json:"groupsClaim"`
	GroupRoles        []settings.GroupRole `json:"groupRoles"`
	SyncGroupsOnLogin bool                 `json:"syncGroupsOnLogin"`
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

	// Reject unknown permissions here rather than silently storing a mapping that
	// grants nothing (the perms become user.Permissions entries verbatim).
	roles, err := normalizeGroupRoles(req.GroupRoles)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Enabled {
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
		_, err = auth.NewOIDCProvider(ctx, req.Issuer, req.ClientID, secret, req.RedirectURL, strings.Fields(req.Scopes), req.GroupsClaim)
		cancel()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "could not connect to the identity provider: "+err.Error())
			return
		}
	}

	if err := s.settings.SetSSO(settings.SSO{
		Enabled:           req.Enabled,
		Issuer:            req.Issuer,
		ClientID:          req.ClientID,
		ClientSecret:      req.ClientSecret,
		RedirectURL:       req.RedirectURL,
		Scopes:            req.Scopes,
		GroupsClaim:       strings.TrimSpace(req.GroupsClaim),
		GroupRoles:        roles,
		SyncGroupsOnLogin: req.SyncGroupsOnLogin,
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

// normalizeGroupRoles trims and validates a group→role mapping: group names must
// be non-empty and every permission must be one ArcLoom knows, since these
// strings are copied straight into user.Permissions.
func normalizeGroupRoles(in []settings.GroupRole) ([]settings.GroupRole, error) {
	out := make([]settings.GroupRole, 0, len(in))
	for _, gr := range in {
		gr.Group = strings.TrimSpace(gr.Group)
		if gr.Group == "" {
			continue // an empty row is the UI's "not filled in yet", not an error
		}
		perms := make([]string, 0, len(gr.Perms))
		for _, p := range gr.Perms {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !slices.Contains(knownPerms, p) {
				return nil, fmt.Errorf("unknown permission %q for group %q", p, gr.Group)
			}
			if !slices.Contains(perms, p) {
				perms = append(perms, p)
			}
		}
		gr.Perms = perms
		out = append(out, gr)
	}
	return out, nil
}

// knownPerms is the set a group mapping may grant (admin is its own flag).
var knownPerms = []string{user.PermResources, user.PermDeploy, user.PermGit, user.PermApprove}
