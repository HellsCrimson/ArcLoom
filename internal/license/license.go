// Package license verifies ArcLoom Enterprise license keys and gates
// enterprise-only features. Verification is fully offline: a license is a
// vendor-signed token (Ed25519), and ArcLoom embeds only the vendor PUBLIC key,
// so a valid license can be checked in an air-gapped install and cannot be
// forged without the vendor's private key.
//
// This package (the verifier) is part of the Apache-licensed core — checking a
// signature is not itself an enterprise feature. Which features a tier unlocks,
// and the enforcement of the license terms, are governed by the ArcLoom
// Enterprise Edition License (see ee/LICENSE); circumventing or disabling this
// gate to use enterprise features without a valid license violates that
// license.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Tiers.
const (
	TierCommunity  = "community"
	TierEnterprise = "enterprise"
)

// Enterprise feature flags. A community install has none of these; an
// enterprise license grants them (all of them when its Features list is empty,
// otherwise exactly the ones listed).
const (
	FeatureSSO       = "sso"        // OIDC single sign-on
	FeatureRBAC      = "rbac"       // per-project ACLs + audit log (PLAN item 14)
	FeatureDrift     = "drift"      // scheduled drift detection
	FeatureMultiUser = "multi_user" // more users than the community seat limit
	FeatureCost      = "cost"       // plan-time cost estimation (overrides + Infracost)
	FeatureVault     = "vault"      // external secret store (Vault / OpenBao)
)

// CommunitySeatLimit is how many users an unlicensed (community) install may
// create. Enough for a homelab; more requires a license.
const CommunitySeatLimit = 3

// License is the decoded, verified payload of a license token.
type License struct {
	Licensee  string    `json:"licensee"`
	Tier      string    `json:"tier"`
	Seats     int       `json:"seats"` // max users; 0 = unlimited
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"` // zero = perpetual
	// Features restricts which enterprise features this license unlocks. Empty
	// on an enterprise license means "all enterprise features".
	Features []string `json:"features,omitempty"`
}

// Expired reports whether the license is past its expiry.
func (l *License) Expired() bool {
	return !l.ExpiresAt.IsZero() && time.Now().After(l.ExpiresAt)
}

// grants reports whether this (assumed valid) license unlocks a feature.
func (l *License) grants(feature string) bool {
	if l.Tier != TierEnterprise {
		return false
	}
	if len(l.Features) == 0 {
		return true // full enterprise
	}
	return slices.Contains(l.Features, feature)
}

// Manager holds the active license and answers feature/seat questions. The
// zero-value behavior (no license set) is community tier. Safe for concurrent
// use.
type Manager struct {
	pub ed25519.PublicKey
	cur atomic.Pointer[License]
}

// NewManager builds a Manager that verifies tokens against pub.
func NewManager(pub ed25519.PublicKey) *Manager {
	return &Manager{pub: pub}
}

// Set installs a license from its token. An empty token clears the license
// (reverting to community). A token that fails verification is rejected and the
// previous license is left in place.
func (m *Manager) Set(token string) error {
	if strings.TrimSpace(token) == "" {
		m.cur.Store(nil)
		return nil
	}
	lic, err := Verify(m.pub, token)
	if err != nil {
		return err
	}
	m.cur.Store(lic)
	return nil
}

// Install stores an already-obtained License directly (bypassing token
// verification). Used when a License comes from a trusted source other than a
// signed token — e.g. a future online activation response, or tests.
func (m *Manager) Install(l *License) {
	m.cur.Store(l)
}

// Current returns the active license, or nil for community tier.
func (m *Manager) Current() *License {
	return m.cur.Load()
}

// valid returns the current license only if it is present and not expired.
func (m *Manager) valid() *License {
	l := m.cur.Load()
	if l == nil || l.Expired() {
		return nil
	}
	return l
}

// Has reports whether an enterprise feature is currently unlocked.
func (m *Manager) Has(feature string) bool {
	l := m.valid()
	return l != nil && l.grants(feature)
}

// Tier returns the effective tier ("community" unless a valid enterprise
// license is installed).
func (m *Manager) Tier() string {
	if l := m.valid(); l != nil {
		return l.Tier
	}
	return TierCommunity
}

// SeatLimit returns the maximum number of users allowed. 0 means unlimited.
func (m *Manager) SeatLimit() int {
	l := m.valid()
	if l == nil || !l.grants(FeatureMultiUser) {
		return CommunitySeatLimit
	}
	return l.Seats // 0 = unlimited
}

// SeatsExceeded reports whether creating one more user (bringing the total to
// current+1) would exceed the seat limit.
func (m *Manager) SeatsExceeded(current int) bool {
	limit := m.SeatLimit()
	return limit > 0 && current >= limit
}

// --- token format: base64url(payload JSON) + "." + base64url(signature) ---

// Sign produces a license token for lic signed with priv. Vendor-side (used by
// the signer CLI and tests); ArcLoom itself only ever verifies.
func Sign(priv ed25519.PrivateKey, lic License) (string, error) {
	payload, err := json.Marshal(lic)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	return b64(payload) + "." + b64(sig), nil
}

// Verify decodes and cryptographically verifies a token against pub. It does
// NOT check expiry — that is a policy decision left to the Manager so an expired
// license can still be displayed.
func Verify(pub ed25519.PublicKey, token string) (*License, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed license token")
	}
	payload, err := unb64(parts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed license payload")
	}
	sig, err := unb64(parts[1])
	if err != nil {
		return nil, fmt.Errorf("malformed license signature")
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, payload, sig) {
		return nil, fmt.Errorf("license signature is not valid")
	}
	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, fmt.Errorf("malformed license payload")
	}
	return &lic, nil
}

func b64(b []byte) string            { return base64.RawURLEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
