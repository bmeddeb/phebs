package packrelease

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidRelease is the sentinel every verification failure unwraps to.
var ErrInvalidRelease = errors.New("invalid pack release")

// Reason is a stable, machine-readable rejection code. Callers classify a
// refusal with ReasonOf rather than matching message text.
type Reason string

// The complete rejection taxonomy. Each maps to a distinct fail-closed refusal
// named by the AC: unknown, duplicate/non-canonical fields, foreign or
// mismatched digests, invalid signatures, unmeasured claims, expired or
// revoked releases, and unsupported versions.
const (
	ReasonOversized           Reason = "oversized"
	ReasonDecode              Reason = "decode"
	ReasonDuplicateKey        Reason = "duplicate_key"
	ReasonUnknownField        Reason = "unknown_field"
	ReasonTrailingJSON        Reason = "trailing_json"
	ReasonNonCanonical        Reason = "noncanonical"
	ReasonUnsupportedSchema   Reason = "unsupported_schema_version"
	ReasonUnsupportedCanon    Reason = "unsupported_canonicalization"
	ReasonMalformedDigest     Reason = "malformed_digest"
	ReasonUnknownEnum         Reason = "unknown_enum"
	ReasonInvalidTimestamp    Reason = "invalid_timestamp"
	ReasonInvalidField        Reason = "invalid_field"
	ReasonMissingApproval     Reason = "missing_approval"
	ReasonUnmeasuredClaim     Reason = "unmeasured_claim"
	ReasonExpired             Reason = "expired"
	ReasonRevoked             Reason = "revoked"
	ReasonFutureApproval      Reason = "future_approval"
	ReasonUnresolvedReference Reason = "unresolved_reference"
	ReasonDigestMismatch      Reason = "digest_mismatch"
	ReasonUnapprovedAlgorithm Reason = "unapproved_algorithm"
	ReasonUnknownKey          Reason = "unknown_key"
	ReasonInvalidSignature    Reason = "invalid_signature"
)

// ReleaseError is a typed verification failure carrying a stable Reason.
type ReleaseError struct {
	Reason Reason
	Detail error
}

func (e *ReleaseError) Error() string {
	if e.Detail != nil {
		return string(ErrInvalidRelease.Error()) + ": " + string(e.Reason) + ": " + e.Detail.Error()
	}
	return ErrInvalidRelease.Error() + ": " + string(e.Reason)
}

// Unwrap reports the sentinel so errors.Is(err, ErrInvalidRelease) holds for
// every rejection in the taxonomy.
func (e *ReleaseError) Unwrap() error { return ErrInvalidRelease }

// ReasonOf extracts the stable rejection code from err, if it is one.
func ReasonOf(err error) (Reason, bool) {
	var releaseErr *ReleaseError
	if errors.As(err, &releaseErr) {
		return releaseErr.Reason, true
	}
	return "", false
}

func reject(reason Reason, format string, args ...any) error {
	return &ReleaseError{Reason: reason, Detail: fmt.Errorf(format, args...)}
}

var (
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	semverRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	idRE     = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,256}$`)
)

const maxIDLen = 256

// Verify parses and validates raw canonical release bytes against the supplied
// trust anchors. It is fail closed: any structural, canonicalization, binding,
// lifecycle, or signature defect returns a typed ReleaseError wrapping
// ErrInvalidRelease. On success it returns the parsed record.
func Verify(raw []byte, opts Options) (*PackRelease, error) {
	if len(raw) == 0 {
		return nil, reject(ReasonDecode, "release record is empty")
	}
	if len(raw) > MaxReleaseBytes {
		return nil, reject(ReasonOversized, "release record is %d bytes, exceeds %d", len(raw), MaxReleaseBytes)
	}

	// Reject duplicate keys and structural defects before canonicalization,
	// per docs/PACK_MANIFEST.md section 3.
	if err := scanStructure(raw); err != nil {
		return nil, err
	}

	release := &PackRelease{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(release); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return nil, reject(ReasonUnknownField, "decode release record: %v", err)
		}
		return nil, reject(ReasonDecode, "decode release record: %v", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, reject(ReasonTrailingJSON, "%v", err)
	}

	canonical, err := CanonicalPayload(release)
	if err != nil {
		return nil, reject(ReasonNonCanonical, "%v", err)
	}
	if !bytes.Equal(raw, canonical) {
		return nil, reject(ReasonNonCanonical, "release record is not canonical JSON")
	}

	if err := validateStructure(release); err != nil {
		return nil, err
	}
	if err := validateStatusGate(release, opts.now()); err != nil {
		return nil, err
	}
	if err := resolveReferences(release, opts.Resolver); err != nil {
		return nil, err
	}
	if err := verifySignature(release, opts); err != nil {
		return nil, err
	}
	return release, nil
}

// scanStructure walks the token stream and rejects duplicate object keys and
// structurally invalid JSON before any decoding or canonicalization.
func scanStructure(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	type frame struct {
		array     bool
		keys      map[string]struct{}
		expectKey bool
	}
	var stack []frame
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if len(stack) != 0 {
				return reject(ReasonDecode, "unbalanced JSON")
			}
			return nil
		}
		if err != nil {
			return reject(ReasonDecode, "scan JSON: %v", err)
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				stack = append(stack, frame{keys: map[string]struct{}{}, expectKey: true})
			case '[':
				stack = append(stack, frame{array: true})
			case '}', ']':
				if len(stack) == 0 {
					return reject(ReasonDecode, "unbalanced JSON")
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && !stack[len(stack)-1].array {
					stack[len(stack)-1].expectKey = true
				}
			}
			continue
		}
		if len(stack) == 0 {
			continue
		}
		top := &stack[len(stack)-1]
		if top.array {
			continue
		}
		if top.expectKey {
			key, ok := token.(string)
			if !ok {
				return reject(ReasonDecode, "object key is not a string")
			}
			if _, duplicate := top.keys[key]; duplicate {
				return reject(ReasonDuplicateKey, "duplicate key %q", key)
			}
			top.keys[key] = struct{}{}
			top.expectKey = false
		} else {
			top.expectKey = true
		}
	}
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

// validateStructure enforces the exact supported version, canonicalization
// binding, digest and timestamp formats, enums, and identifier bounds. It is
// lifecycle-agnostic; validateStatusGate applies the released-load gate.
func validateStructure(release *PackRelease) error {
	if err := validateSchemaVersion(release.ReleaseSchemaVersion); err != nil {
		return err
	}
	if release.Canonicalization.Algorithm != CanonicalAlgorithm ||
		release.Canonicalization.Version != CanonicalVersion {
		return reject(ReasonUnsupportedCanon, "canonicalization %q/%q is not the frozen %q/%q",
			release.Canonicalization.Algorithm, release.Canonicalization.Version,
			CanonicalAlgorithm, CanonicalVersion)
	}

	for label, value := range map[string]string{
		"release_id":             release.ReleaseID,
		"pack_id":                release.PackID,
		"card.artifact_id":       release.Card.ArtifactID,
		"manifest.artifact_id":   release.Manifest.ArtifactID,
		"validation.artifact_id": release.Validation.ArtifactID,
		"signature.key_id":       release.Signature.KeyID,
	} {
		if err := validID(label, value); err != nil {
			return err
		}
	}
	if err := validSemver("release_version", release.ReleaseVersion); err != nil {
		return err
	}
	if err := validSemver("pack_claim_version", release.PackClaimVersion); err != nil {
		return err
	}

	for label, value := range map[string]string{
		"card.digest":                        release.Card.Digest,
		"manifest.digest":                    release.Manifest.Digest,
		"validation.digest":                  release.Validation.Digest,
		"implementation.phebs_binary":        release.Implementation.PhebsBinaryDigest,
		"implementation.pack_implementation": release.Implementation.PackImplementationDigest,
		"implementation.toolchain":           release.Implementation.ToolchainDigest,
		"referenced_artifacts_root":          release.ReferencedArtifactsRootDigest,
	} {
		if !validDigest(value) {
			return reject(ReasonMalformedDigest, "%s is not a sha256 digest", label)
		}
	}
	if !commitRE.MatchString(release.Implementation.PhebsSourceCommit) {
		return reject(ReasonInvalidField, "implementation.phebs_source_commit is not a 40-hex commit")
	}

	if err := validTimestamp("approved_at", release.ApprovedAt); err != nil {
		return err
	}
	if err := validTimestamp("validation.expires_at", release.Validation.ExpiresAt); err != nil {
		return err
	}

	if !knownStatus(release.DerivedStatus) {
		return reject(ReasonUnknownEnum, "derived_status %q", release.DerivedStatus)
	}

	seen := make(map[string]struct{}, len(release.ApprovalRecords))
	for _, record := range release.ApprovalRecords {
		if err := validID("approval_records", record); err != nil {
			return err
		}
		if _, duplicate := seen[record]; duplicate {
			return reject(ReasonInvalidField, "duplicate approval record %q", record)
		}
		seen[record] = struct{}{}
	}
	return nil
}

// validateStatusGate verifies the derived lifecycle status against the record's
// own observable facts rather than trusting it as an owner-supplied assertion.
// Only an unexpired, applicable, approved StatusReleased record admits an
// ordinary load; expired and revoked claims are rejected here.
func validateStatusGate(release *PackRelease, now time.Time) error {
	switch release.DerivedStatus {
	case StatusReleased:
		if !release.Validation.Applies {
			return reject(ReasonUnmeasuredClaim, "released status requires an applicable validation")
		}
		if release.Validation.ExpiryTrigger != nil {
			return reject(ReasonRevoked, "released status carries expiry trigger %q", *release.Validation.ExpiryTrigger)
		}
		expires, err := time.Parse(time.RFC3339, release.Validation.ExpiresAt)
		if err != nil {
			return reject(ReasonInvalidTimestamp, "validation.expires_at: %v", err)
		}
		if !expires.After(now) {
			return reject(ReasonExpired, "validation expired at %s", release.Validation.ExpiresAt)
		}
		if len(release.ApprovalRecords) == 0 {
			return reject(ReasonMissingApproval, "released status requires at least one approval record")
		}
		approved, err := time.Parse(time.RFC3339, release.ApprovedAt)
		if err != nil {
			return reject(ReasonInvalidTimestamp, "approved_at: %v", err)
		}
		if approved.After(now) {
			return reject(ReasonFutureApproval, "approved_at %s is in the future", release.ApprovedAt)
		}
	case StatusDesign, StatusExperimentalDark, StatusShadow, StatusSuspended, StatusRetired:
		// Structurally valid non-released states. They never admit an ordinary
		// claim load; runtime selection among them is a later slice.
	default:
		return reject(ReasonUnknownEnum, "derived_status %q", release.DerivedStatus)
	}
	return nil
}

func resolveReferences(release *PackRelease, resolver ArtifactResolver) error {
	if resolver == nil {
		return nil
	}
	refs := []struct {
		kind   string
		id     string
		digest string
	}{
		{"card", release.Card.ArtifactID, release.Card.Digest},
		{"manifest", release.Manifest.ArtifactID, release.Manifest.Digest},
		{"validation", release.Validation.ArtifactID, release.Validation.Digest},
	}
	for _, ref := range refs {
		got, found := resolver.Resolve(ref.id)
		if !found {
			return reject(ReasonUnresolvedReference, "%s artifact %q is not resolvable", ref.kind, ref.id)
		}
		if got != ref.digest {
			return reject(ReasonDigestMismatch, "%s artifact %q digest mismatch", ref.kind, ref.id)
		}
	}
	return nil
}

func verifySignature(release *PackRelease, opts Options) error {
	if !opts.algorithmApproved(release.Signature.Algorithm) {
		return reject(ReasonUnapprovedAlgorithm, "signature algorithm %q is not approved", release.Signature.Algorithm)
	}
	if release.Signature.Algorithm != SignatureAlgorithmEd25519 {
		return reject(ReasonUnapprovedAlgorithm, "signature algorithm %q is not supported", release.Signature.Algorithm)
	}
	public, ok := opts.Keys[release.Signature.KeyID]
	if !ok || len(public) != ed25519.PublicKeySize {
		return reject(ReasonUnknownKey, "key %q is not an approved signing key", release.Signature.KeyID)
	}
	signature, err := base64.StdEncoding.DecodeString(release.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return reject(ReasonInvalidSignature, "signature value is not a well-formed ed25519 signature")
	}
	payload, err := SigningPayload(release)
	if err != nil {
		return reject(ReasonInvalidSignature, "%v", err)
	}
	if !ed25519.Verify(public, payload, signature) {
		return reject(ReasonInvalidSignature, "signature does not verify over the canonical payload")
	}
	return nil
}

func validateSchemaVersion(version string) error {
	major, minor, ok := strings.Cut(version, ".")
	if !ok || !numericToken(major) || !numericToken(minor) || strings.Contains(minor, ".") {
		return reject(ReasonUnsupportedSchema, "release_schema_version %q is not MAJOR.MINOR", version)
	}
	majorValue, err := strconv.Atoi(major)
	if err != nil || majorValue != supportedSchemaMajor {
		return reject(ReasonUnsupportedSchema, "release major version %q is unsupported", version)
	}
	minorValue, err := strconv.Atoi(minor)
	if err != nil || minorValue > supportedSchemaMinor {
		return reject(ReasonUnsupportedSchema, "release minor version %q is unsupported", version)
	}
	return nil
}

func numericToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value == "0" || value[0] != '0'
}

func knownStatus(status string) bool {
	switch status {
	case StatusDesign, StatusExperimentalDark, StatusShadow,
		StatusReleased, StatusSuspended, StatusRetired:
		return true
	default:
		return false
	}
}

func validID(label, value string) error {
	if len(value) > maxIDLen || !idRE.MatchString(value) {
		return reject(ReasonInvalidField, "%s %q is not a bounded identifier", label, value)
	}
	return nil
}

func validSemver(label, value string) error {
	if !semverRE.MatchString(value) {
		return reject(ReasonInvalidField, "%s %q is not MAJOR.MINOR.PATCH", label, value)
	}
	return nil
}

func validTimestamp(label, value string) error {
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return reject(ReasonInvalidTimestamp, "%s %q is not RFC3339", label, value)
	}
	return nil
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}
