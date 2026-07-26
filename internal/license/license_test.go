package license

import (
	"crypto/ed25519"
	"slices"
	"testing"
	"time"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv := keypair(t)
	in := License{
		Licensee:  "Acme",
		Tier:      TierEnterprise,
		Seats:     10,
		IssuedAt:  time.Now().Truncate(time.Second),
		ExpiresAt: time.Now().Add(24 * time.Hour).Truncate(time.Second),
		Features:  []string{FeatureSSO, FeatureDrift},
	}
	token, err := Sign(priv, in)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	out, err := Verify(pub, token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.Licensee != "Acme" || out.Tier != TierEnterprise || out.Seats != 10 {
		t.Fatalf("payload mismatch: %+v", out)
	}
}

func TestVerifyRejectsForgery(t *testing.T) {
	pub, priv := keypair(t)
	token, _ := Sign(priv, License{Tier: TierEnterprise})
	// Tamper: flip a byte in the payload segment.
	bad := []byte(token)
	bad[0] ^= 0xFF
	if _, err := Verify(pub, string(bad)); err == nil {
		t.Fatal("expected verification to fail on tampered token")
	}
	// A different key must not verify.
	otherPub, _ := keypair(t)
	if _, err := Verify(otherPub, token); err == nil {
		t.Fatal("expected verification to fail under a different key")
	}
	if _, err := Verify(pub, "not-a-token"); err == nil {
		t.Fatal("expected malformed token error")
	}
}

func TestManagerFeatureGates(t *testing.T) {
	pub, priv := keypair(t)
	m := NewManager(pub)

	// Community default: no features, community seat limit.
	if m.Tier() != TierCommunity || m.Has(FeatureSSO) {
		t.Fatal("expected community with no features")
	}
	if m.SeatLimit() != CommunitySeatLimit {
		t.Fatalf("expected community seat limit %d, got %d", CommunitySeatLimit, m.SeatLimit())
	}
	if !m.SeatsExceeded(CommunitySeatLimit) || m.SeatsExceeded(CommunitySeatLimit-1) {
		t.Fatal("seat exceeded boundary wrong")
	}

	// Enterprise license limited to SSO + multi_user (20 seats).
	tok, _ := Sign(priv, License{
		Tier: TierEnterprise, Seats: 20,
		ExpiresAt: time.Now().Add(time.Hour),
		Features:  []string{FeatureSSO, FeatureMultiUser},
	})
	if err := m.Set(tok); err != nil {
		t.Fatalf("set: %v", err)
	}
	if m.Tier() != TierEnterprise || !m.Has(FeatureSSO) {
		t.Fatal("expected SSO unlocked")
	}
	if m.Has(FeatureDrift) {
		t.Fatal("drift should NOT be unlocked (not in Features)")
	}
	if m.SeatLimit() != 20 || m.SeatsExceeded(19) {
		t.Fatalf("expected 20-seat limit, got %d", m.SeatLimit())
	}

	// Full enterprise (empty Features) grants everything and unlimited seats.
	full, _ := Sign(priv, License{Tier: TierEnterprise, Seats: 0, ExpiresAt: time.Now().Add(time.Hour)})
	_ = m.Set(full)
	if !m.Has(FeatureDrift) || !m.Has(FeatureRBAC) {
		t.Fatal("full enterprise should grant all features")
	}
	if m.SeatLimit() != 0 || m.SeatsExceeded(1000) {
		t.Fatal("full enterprise should be unlimited seats")
	}

	// Expired license falls back to community.
	exp, _ := Sign(priv, License{Tier: TierEnterprise, ExpiresAt: time.Now().Add(-time.Hour)})
	_ = m.Set(exp)
	if m.Tier() != TierCommunity || m.Has(FeatureSSO) {
		t.Fatal("expired license should not grant features")
	}
	if m.Current() == nil || !m.Current().Expired() {
		t.Fatal("expired license should still be inspectable")
	}

	// Clearing reverts to community.
	_ = m.Set("")
	if m.Current() != nil {
		t.Fatal("expected cleared license")
	}
}

// TestAllFeaturesCoversEveryFlag guards the mistake that shipped `vault`
// enforced-but-unlisted: a feature absent from AllFeatures is gated correctly yet
// never appears in Settings → License, so nobody can tell their key unlocked it.
func TestAllFeaturesCoversEveryFlag(t *testing.T) {
	// Every flag the codebase defines. Go cannot enumerate constants, so this list
	// is the deliberate second copy — if it and AllFeatures disagree, one of them
	// was forgotten.
	defined := []string{
		FeatureSSO, FeatureRBAC, FeatureDrift, FeatureMultiUser, FeatureCost, FeatureVault,
	}
	if len(AllFeatures) != len(defined) {
		t.Fatalf("AllFeatures has %d entries, %d flags are defined: %v", len(AllFeatures), len(defined), AllFeatures)
	}
	for _, f := range defined {
		if !slices.Contains(AllFeatures, f) {
			t.Errorf("feature %q is missing from AllFeatures (it would be enforced but invisible)", f)
		}
	}
	// A blanket enterprise license (empty Features) must unlock every one of them.
	m := NewManager(nil)
	m.Install(&License{Tier: TierEnterprise})
	for _, f := range AllFeatures {
		if !m.Has(f) {
			t.Errorf("a blanket enterprise license should grant %q", f)
		}
	}
}
