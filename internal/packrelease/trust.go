package packrelease

import (
	"context"
	"crypto/ed25519"
	"time"
)

// KeyRing maps an approved key identifier to its ed25519 public key. A release
// is admitted only when its signature key_id resolves here and the signature
// verifies. The ring is an operator-controlled trust anchor; it is never
// populated from the release record's own embedded key material.
type KeyRing map[string]ed25519.PublicKey

// ArtifactResolver binds a stable artifact identifier to the digest of the
// bytes actually present. When supplied, the verifier confirms that the card,
// manifest, and validation references resolve and match their recorded
// digests, rejecting a foreign or mismatched artifact. A nil resolver skips
// reference resolution in Verify but never skips digest-format validation;
// VerifyForLoad requires one. A lookup error refuses the release.
type ArtifactResolver interface {
	Resolve(ctx context.Context, artifactID string) (digest string, found bool, err error)
}

// Options carry the trust anchors and evaluation instant for Verify. The zero
// value admits no key and evaluates expiry against the current wall clock.
type Options struct {
	// Now is the instant used for expiry and future-approval evaluation. When
	// zero, Verify uses time.Now().
	Now time.Time

	// Keys is the approved signing-key ring. A release signed by an unknown key
	// is rejected.
	Keys KeyRing

	// ApprovedAlgorithms restricts the accepted signature algorithms. When
	// empty, only SignatureAlgorithmEd25519 is approved.
	ApprovedAlgorithms []string

	// Resolver binds recorded references to present artifact bytes.
	Resolver ArtifactResolver

	// Implementation is the running phebs and pack identity. When set, a
	// release must name it exactly; VerifyForLoad requires it.
	Implementation *Implementation

	// ReferencedArtifactsRootDigest is the digest of the referenced artifacts
	// actually present. When set, a release must name it exactly;
	// VerifyForLoad requires it.
	ReferencedArtifactsRootDigest string

	// Revoked lists release IDs withdrawn by a later suspension, revocation or
	// superseding release. A listed release is rejected in every lifecycle
	// state, so a withdrawn record cannot be replayed before it expires.
	Revoked map[string]struct{}
}

// now returns the evaluation instant, defaulting to the current wall clock.
func (o Options) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now
}

// algorithmApproved reports whether alg is an accepted signature algorithm.
func (o Options) algorithmApproved(alg string) bool {
	if len(o.ApprovedAlgorithms) == 0 {
		return alg == SignatureAlgorithmEd25519
	}
	for _, approved := range o.ApprovedAlgorithms {
		if approved == alg {
			return true
		}
	}
	return false
}
