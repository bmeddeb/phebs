// Package packrelease implements the fail-closed verifier for the signed
// PackRelease binding described by docs/PACK_MANIFEST.md section 4. A
// PackRelease record, not a self-asserted manifest or card field, determines
// which fixed in-tree pack may load and in which mode.
//
// This slice owns the schema and verifier only. It performs no runtime
// registration, no startup selection, and no pack execution: an absent
// production selection adds no pack work, and registration is never obtained
// by toggling a provisional extraction switch. Startup and runtime recipe
// selection are a later slice that consumes Verify.
//
// The verifier is fail closed. It rejects an unsupported schema major, an
// unknown field, a duplicate object key, a non-canonical artifact, trailing
// JSON, a malformed digest, an unsatisfied reference, an unknown enum, an
// invalid timestamp, an unmeasured or expired or revoked claim, and an
// invalid or unapproved signature. Every rejection carries a stable Reason
// code and unwraps to ErrInvalidRelease.
package packrelease

import (
	"encoding/json"
	"fmt"
)

// Frozen contract identifiers. The canonical-JSON algorithm matches the
// platform frozen form used by internal/sourcepartition: compact json.Marshal
// in Go struct-declaration order followed by a single trailing newline.
const (
	// ReleaseSchemaVersion is the exact supported MAJOR.MINOR of the record.
	ReleaseSchemaVersion = "1.0"

	// CanonicalAlgorithm and CanonicalVersion name the frozen canonical-JSON
	// binding recorded in every release.
	CanonicalAlgorithm = "phebs-canonical-json"
	CanonicalVersion   = "1"

	// SignatureAlgorithmEd25519 is the only supported signature algorithm.
	SignatureAlgorithmEd25519 = "ed25519"

	// MaxReleaseBytes bounds an admitted release record.
	MaxReleaseBytes = 1 << 20

	supportedSchemaMajor = 1
	supportedSchemaMinor = 0
)

// Derived lifecycle statuses. Only an unexpired, applicable, approved
// StatusReleased record admits an ordinary claim or decision-support load; the
// other states are structurally valid but never admit one. Runtime selection
// among them is a later slice.
const (
	StatusDesign           = "design"
	StatusExperimentalDark = "experimental-dark"
	StatusShadow           = "shadow"
	StatusReleased         = "released"
	StatusSuspended        = "suspended"
	StatusRetired          = "retired"
)

// PackRelease is the signed immutable binding of an approved card, manifest,
// implementation, referenced-artifact root, validation result, and approvals.
// Field order is the canonical JSON order and must match docs/PACK_MANIFEST.md
// section 4 exactly.
type PackRelease struct {
	ReleaseSchemaVersion          string           `json:"release_schema_version"`
	ReleaseID                     string           `json:"release_id"`
	ReleaseVersion                string           `json:"release_version"`
	PackID                        string           `json:"pack_id"`
	PackClaimVersion              string           `json:"pack_claim_version"`
	Card                          ArtifactRef      `json:"card"`
	Manifest                      ArtifactRef      `json:"manifest"`
	Implementation                Implementation   `json:"implementation"`
	ReferencedArtifactsRootDigest string           `json:"referenced_artifacts_root_digest"`
	Validation                    Validation       `json:"validation"`
	DerivedStatus                 string           `json:"derived_status"`
	ApprovedAt                    string           `json:"approved_at"`
	ApprovalRecords               []string         `json:"approval_records"`
	Canonicalization              Canonicalization `json:"canonicalization"`
	Signature                     Signature        `json:"signature"`
}

// ArtifactRef binds a stable artifact identifier to its content digest.
type ArtifactRef struct {
	ArtifactID string `json:"artifact_id"`
	Digest     string `json:"digest"`
}

// Implementation records the exact binary and toolchain identities the release
// was built and validated against.
type Implementation struct {
	PhebsSourceCommit        string `json:"phebs_source_commit"`
	PhebsBinaryDigest        string `json:"phebs_binary_digest"`
	PackImplementationDigest string `json:"pack_implementation_digest"`
	ToolchainDigest          string `json:"toolchain_digest"`
}

// Validation binds the applicable validation result and its expiry. A nil
// ExpiryTrigger means no suspension or expiry cause is recorded.
type Validation struct {
	ArtifactID    string  `json:"artifact_id"`
	Digest        string  `json:"digest"`
	Applies       bool    `json:"applies"`
	ExpiresAt     string  `json:"expires_at"`
	ExpiryTrigger *string `json:"expiry_trigger"`
}

// Canonicalization names the frozen canonical-JSON algorithm and version.
type Canonicalization struct {
	Algorithm string `json:"algorithm"`
	Version   string `json:"version"`
}

// Signature binds an approved key, algorithm, and encoded signature value.
// The value covers the canonical record excluding the value itself.
type Signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// CanonicalPayload returns the frozen canonical bytes of the release: compact
// JSON in field-declaration order followed by a single trailing newline. These
// are the bytes a digest covers.
func CanonicalPayload(release *PackRelease) ([]byte, error) {
	if release == nil {
		return nil, fmt.Errorf("encode pack release: nil record")
	}
	data, err := json.Marshal(release)
	if err != nil {
		return nil, fmt.Errorf("encode pack release: %w", err)
	}
	return append(data, '\n'), nil
}

// SigningPayload returns the canonical bytes covered by the signature: the
// canonical record with the signature value cleared, per docs/PACK_MANIFEST.md
// section 3 ("signatures cover the canonical PackRelease payload excluding the
// signature value itself").
func SigningPayload(release *PackRelease) ([]byte, error) {
	if release == nil {
		return nil, fmt.Errorf("encode pack release signing payload: nil record")
	}
	clone := *release
	clone.Signature.Value = ""
	return CanonicalPayload(&clone)
}
