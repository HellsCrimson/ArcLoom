package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/HellsCrimson/ArcLoom/internal/license"
)

// licenseDTO is the license status the SPA uses to show the tier and gate
// enterprise-only actions. The raw token is never returned.
type licenseDTO struct {
	Tier      string   `json:"tier"`
	Licensee  string   `json:"licensee,omitempty"`
	Features  []string `json:"features"`  // effective unlocked features
	SeatLimit int      `json:"seatLimit"` // 0 = unlimited
	SeatsUsed int      `json:"seatsUsed"` // current user count
	ExpiresAt string   `json:"expiresAt,omitempty"`
	Expired   bool     `json:"expired"`
	// KeyConfigured is false when no vendor public key is embedded (no license
	// can ever verify — the build is community-only).
	KeyConfigured bool `json:"keyConfigured"`
}

// allFeatures is the catalog reported as "unlocked" for the current license.
var allFeatures = []string{license.FeatureSSO, license.FeatureRBAC, license.FeatureDrift, license.FeatureMultiUser}

func (s *Server) licenseResponse() licenseDTO {
	m := s.license
	dto := licenseDTO{
		Tier:          m.Tier(),
		SeatLimit:     m.SeatLimit(),
		SeatsUsed:     s.userCount(),
		Features:      []string{},
		KeyConfigured: license.VendorKey() != nil,
	}
	for _, f := range allFeatures {
		if m.Has(f) {
			dto.Features = append(dto.Features, f)
		}
	}
	if cur := m.Current(); cur != nil {
		dto.Licensee = cur.Licensee
		dto.Expired = cur.Expired()
		if !cur.ExpiresAt.IsZero() {
			dto.ExpiresAt = cur.ExpiresAt.Format(time.RFC3339)
		}
	}
	return dto
}

// userCount returns the number of stored users (0 when auth is disabled / the
// store errors).
func (s *Server) userCount() int {
	list, err := s.users.List()
	if err != nil {
		return 0
	}
	return len(list)
}

// handleGetLicense returns the current license status (any authenticated user).
func (s *Server) handleGetLicense(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.licenseResponse())
}

// handleSetLicense installs (or clears, with an empty token) a license. Admin
// only. It verifies the token before persisting and hot-swaps the live gate,
// then reloads auth so an SSO-gated provider comes up or down accordingly.
func (s *Server) handleSetLicense(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Verify before persisting so a bad token is rejected up front (Set leaves
	// the previous license in place on error).
	if err := s.license.Set(req.Token); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid license: "+err.Error())
		return
	}
	if err := s.settings.SetLicense(req.Token); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// SSO availability may change with the license; rebuild the authenticator.
	_ = s.ReloadAuth(r.Context())
	writeJSON(w, http.StatusOK, s.licenseResponse())
}
