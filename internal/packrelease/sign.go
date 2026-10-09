package packrelease

import (
	"crypto/ed25519"
	"encoding/base64"
	"time"
)

// Sign authoritatively signs a release record in place. It is an offline
// authoring and operator action, mirroring the release-bundle build step; it is
// never invoked on the ordinary request, sync, or startup path. The signature
// covers the canonical record with the signature value cleared, so the recorded
// value round-trips through Verify's SigningPayload.
//
// Sign sets the frozen canonicalization binding and the ed25519 algorithm so an
// authored record cannot drift from what Verify admits, and refuses a record
// Verify would reject on its structure or its released-status facts. Expiry
// and future approval are judged at load time, not when signing.
func Sign(release *PackRelease, keyID string, privateKey ed25519.PrivateKey) error {
	if release == nil {
		return reject(ReasonInvalidField, "cannot sign a nil release record")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return reject(ReasonUnknownKey, "signing key %q is not a valid ed25519 private key", keyID)
	}
	if err := validID("signature.key_id", keyID); err != nil {
		return err
	}
	release.Canonicalization = Canonicalization{
		Algorithm: CanonicalAlgorithm,
		Version:   CanonicalVersion,
	}
	release.Signature = Signature{KeyID: keyID, Algorithm: SignatureAlgorithmEd25519}
	if err := validateEnvelope(release); err != nil {
		return err
	}
	if err := validateStructure(release); err != nil {
		return err
	}
	if err := validateStatusGate(release, time.Time{}); err != nil {
		return err
	}
	return sign(release, privateKey)
}

// sign writes the signature value over the current record without validating
// it. Tests use it to author records that Verify must refuse.
func sign(release *PackRelease, privateKey ed25519.PrivateKey) error {
	release.Signature.Value = ""
	payload, err := SigningPayload(release)
	if err != nil {
		return err
	}
	release.Signature.Value = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return nil
}
