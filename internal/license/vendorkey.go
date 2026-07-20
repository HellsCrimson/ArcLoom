package license

import (
	"crypto/ed25519"
	"encoding/base64"
)

// vendorPublicKeyB64 is the ArcLoom vendor's Ed25519 PUBLIC key (base64
// std-encoded). Only the vendor holds the matching private key, so only the
// vendor can issue valid licenses. Replace this with your own public key
// (generate one with `go run ./cmd/arcloom-license keygen`) to issue licenses
// under your own key.
const vendorPublicKeyB64 = "3+Q1OSr0iD3ys6XU/JJpgam9UKd3cz3Hlv3bM31CdKA="

// VendorKey returns the embedded vendor public key, or nil if unset/invalid
// (in which case no license can verify and the install stays community-only).
func VendorKey() ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(vendorPublicKeyB64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(raw)
}
