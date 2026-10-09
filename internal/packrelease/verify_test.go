package packrelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fixedNow sits after the sample approval (2026-07-17) and before the sample
// validation expiry (2026-12-31), so a well-formed released record is current.
var fixedNow = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

func testDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validRelease() *PackRelease {
	return &PackRelease{
		ReleaseSchemaVersion: ReleaseSchemaVersion,
		ReleaseID:            "rel-0001",
		ReleaseVersion:       "1.0.0",
		PackID:               "phebs.grpc.client-calls",
		PackClaimVersion:     "1.0.0",
		Card:                 ArtifactRef{ArtifactID: "card.grpc.client-calls", Digest: testDigest("card")},
		Manifest:             ArtifactRef{ArtifactID: "manifest.grpc.client-calls", Digest: testDigest("manifest")},
		Implementation: Implementation{
			PhebsSourceCommit:        strings.Repeat("c", 40),
			PhebsBinaryDigest:        testDigest("binary"),
			PackImplementationDigest: testDigest("pack-impl"),
			ToolchainDigest:          testDigest("toolchain"),
		},
		ReferencedArtifactsRootDigest: testDigest("root"),
		Validation: Validation{
			ArtifactID:    "validation.grpc.client-calls",
			Digest:        testDigest("validation"),
			Applies:       true,
			ExpiresAt:     "2026-12-31T23:59:59Z",
			ExpiryTrigger: nil,
		},
		DerivedStatus:   StatusReleased,
		ApprovedAt:      "2026-07-17T20:00:00Z",
		ApprovalRecords: []string{"approval-0001"},
	}
}

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return public, private
}

// signed encodes a validly signed release and the options that admit it.
func signed(t *testing.T, mutate func(*PackRelease)) ([]byte, Options) {
	t.Helper()
	public, private := testKey(t)
	release := validRelease()
	if mutate != nil {
		mutate(release)
	}
	if err := Sign(release, "key-1", private); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := CanonicalPayload(release)
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	return raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}}
}

func TestVerifyAcceptsSignedRelease(t *testing.T) {
	raw, opts := signed(t, nil)
	release, err := Verify(raw, opts)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if release.DerivedStatus != StatusReleased || release.PackID != "phebs.grpc.client-calls" {
		t.Fatalf("unexpected release: %#v", release)
	}
}

func TestVerifyAcceptsNonReleasedLifecycle(t *testing.T) {
	for _, status := range []string{StatusDesign, StatusExperimentalDark, StatusShadow, StatusSuspended, StatusRetired} {
		status := status
		raw, opts := signed(t, func(r *PackRelease) { r.DerivedStatus = status })
		if _, err := Verify(raw, opts); err != nil {
			t.Fatalf("Verify(%s): %v", status, err)
		}
	}
}

func TestCanonicalPayloadIsFrozenAndDeterministic(t *testing.T) {
	release := validRelease()
	_, private := testKey(t)
	if err := Sign(release, "key-1", private); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	first, err := CanonicalPayload(release)
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	second, err := CanonicalPayload(release)
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("canonical payload is not deterministic")
	}
	if !strings.HasSuffix(string(first), "}\n") || strings.Contains(string(first), "\n  ") {
		t.Fatalf("canonical payload is not compact-with-trailing-newline:\n%s", first)
	}
}

func TestSigningPayloadExcludesSignatureValue(t *testing.T) {
	release := validRelease()
	_, private := testKey(t)
	if err := Sign(release, "key-1", private); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if release.Signature.Value == "" {
		t.Fatal("signed release has empty signature value")
	}
	payload, err := SigningPayload(release)
	if err != nil {
		t.Fatalf("SigningPayload: %v", err)
	}
	if strings.Contains(string(payload), release.Signature.Value) {
		t.Fatal("signing payload leaks the signature value")
	}
	if !strings.Contains(string(payload), `"value":""`) {
		t.Fatalf("signing payload does not clear the value:\n%s", payload)
	}
}

// TestVerifyRejects covers every AC rejection class through struct-level
// mutations applied before signing.
func TestVerifyRejects(t *testing.T) {
	trigger := "suspension-recorded"
	past := fixedNow.Add(-time.Hour).Format(time.RFC3339)
	future := fixedNow.Add(time.Hour).Format(time.RFC3339)
	tests := []struct {
		name   string
		mutate func(*PackRelease)
		reason Reason
	}{
		{"unsupported schema major", func(r *PackRelease) { r.ReleaseSchemaVersion = "2.0" }, ReasonUnsupportedSchema},
		{"unsupported schema minor", func(r *PackRelease) { r.ReleaseSchemaVersion = "1.1" }, ReasonUnsupportedSchema},
		{"malformed schema version", func(r *PackRelease) { r.ReleaseSchemaVersion = "1" }, ReasonUnsupportedSchema},
		{"malformed card digest", func(r *PackRelease) { r.Card.Digest = "sha256:zz" }, ReasonMalformedDigest},
		{"malformed root digest", func(r *PackRelease) { r.ReferencedArtifactsRootDigest = "md5:" + strings.Repeat("a", 32) }, ReasonMalformedDigest},
		{"bad source commit", func(r *PackRelease) { r.Implementation.PhebsSourceCommit = "nothex" }, ReasonInvalidField},
		{"unknown derived status", func(r *PackRelease) { r.DerivedStatus = "bogus" }, ReasonUnknownEnum},
		{"bad release version", func(r *PackRelease) { r.ReleaseVersion = "1.0" }, ReasonInvalidField},
		{"empty pack id", func(r *PackRelease) { r.PackID = "" }, ReasonInvalidField},
		{"bad approved timestamp", func(r *PackRelease) { r.ApprovedAt = "yesterday" }, ReasonInvalidTimestamp},
		{"duplicate approval record", func(r *PackRelease) { r.ApprovalRecords = []string{"a", "a"} }, ReasonInvalidField},
		{"expired released", func(r *PackRelease) { r.Validation.ExpiresAt = past }, ReasonExpired},
		{"revoked released", func(r *PackRelease) { r.Validation.ExpiryTrigger = &trigger }, ReasonRevoked},
		{"unmeasured released", func(r *PackRelease) { r.Validation.Applies = false }, ReasonUnmeasuredClaim},
		{"missing approval on released", func(r *PackRelease) { r.ApprovalRecords = nil }, ReasonMissingApproval},
		{"future approval on released", func(r *PackRelease) { r.ApprovedAt = future }, ReasonFutureApproval},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			raw, opts := signed(t, test.mutate)
			_, err := Verify(raw, opts)
			if err == nil {
				t.Fatalf("Verify accepted an invalid release, want %s", test.reason)
			}
			reason, ok := ReasonOf(err)
			if !ok || reason != test.reason {
				t.Fatalf("reason = %q (%v), want %q", reason, err, test.reason)
			}
		})
	}
}

// TestVerifyRejectsRawBytes covers the byte-level defects that must be caught
// before or independent of the signature: duplicate keys, unknown fields,
// trailing JSON, non-canonical encoding, oversize, and empty input.
func TestVerifyRejectsRawBytes(t *testing.T) {
	valid, opts := signed(t, nil)

	duplicateKey := strings.Replace(string(valid), `"release_id":"rel-0001"`, `"release_id":"rel-0001","release_id":"rel-0002"`, 1)
	unknownField := strings.Replace(string(valid), `"release_id":"rel-0001"`, `"release_id":"rel-0001","surprise":1`, 1)
	trailing := string(valid) + "{}\n"
	// A single inserted space keeps the JSON semantically identical but breaks
	// byte-equality with the compact canonical re-encoding.
	nonCanonical := strings.Replace(string(valid), `"release_id":`, `"release_id": `, 1)

	oversized := append(append([]byte(nil), valid...), make([]byte, MaxReleaseBytes)...)

	tests := []struct {
		name   string
		raw    []byte
		reason Reason
	}{
		{"empty", nil, ReasonDecode},
		{"duplicate key", []byte(duplicateKey), ReasonDuplicateKey},
		{"unknown field", []byte(unknownField), ReasonUnknownField},
		{"trailing json", []byte(trailing), ReasonTrailingJSON},
		{"non canonical", []byte(nonCanonical), ReasonNonCanonical},
		{"oversized", oversized, ReasonOversized},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := Verify(test.raw, opts)
			if err == nil {
				t.Fatalf("Verify accepted invalid bytes, want %s", test.reason)
			}
			if reason, _ := ReasonOf(err); reason != test.reason {
				t.Fatalf("reason = %q (%v), want %q", reason, err, test.reason)
			}
		})
	}
}

func TestVerifyRejectsSignatureDefects(t *testing.T) {
	public, private := testKey(t)
	release := validRelease()
	if err := Sign(release, "key-1", private); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	t.Run("unknown key", func(t *testing.T) {
		raw, _ := CanonicalPayload(release)
		_, err := Verify(raw, Options{Now: fixedNow, Keys: KeyRing{"other": public}})
		if reason, _ := ReasonOf(err); reason != ReasonUnknownKey {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnknownKey)
		}
	})

	t.Run("tampered signature value", func(t *testing.T) {
		tampered := *release
		raw, err := base64.StdEncoding.DecodeString(release.Signature.Value)
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}
		raw[0] ^= 0xff
		tampered.Signature.Value = base64.StdEncoding.EncodeToString(raw)
		bytes, _ := CanonicalPayload(&tampered)
		_, err = Verify(bytes, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
		if reason, _ := ReasonOf(err); reason != ReasonInvalidSignature {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonInvalidSignature)
		}
	})

	t.Run("unapproved algorithm", func(t *testing.T) {
		tampered := *release
		tampered.Signature.Algorithm = "rsa-pss"
		bytes, _ := CanonicalPayload(&tampered)
		_, err := Verify(bytes, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
		if reason, _ := ReasonOf(err); reason != ReasonUnapprovedAlgorithm {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnapprovedAlgorithm)
		}
	})

	t.Run("unsupported canonicalization", func(t *testing.T) {
		tampered := *release
		tampered.Canonicalization.Algorithm = "something-else"
		bytes, _ := CanonicalPayload(&tampered)
		_, err := Verify(bytes, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
		if reason, _ := ReasonOf(err); reason != ReasonUnsupportedCanon {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnsupportedCanon)
		}
	})
}

type mapResolver map[string]string

func (m mapResolver) Resolve(id string) (string, bool) {
	digest, ok := m[id]
	return digest, ok
}

func TestVerifyReferenceResolution(t *testing.T) {
	release := validRelease()
	_, private := testKey(t)
	if err := Sign(release, "key-1", private); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, _ := CanonicalPayload(release)
	public := private.Public().(ed25519.PublicKey)

	t.Run("matching resolver admits", func(t *testing.T) {
		resolver := mapResolver{
			release.Card.ArtifactID:       release.Card.Digest,
			release.Manifest.ArtifactID:   release.Manifest.Digest,
			release.Validation.ArtifactID: release.Validation.Digest,
		}
		if _, err := Verify(raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}, Resolver: resolver}); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("mismatched digest rejects", func(t *testing.T) {
		resolver := mapResolver{
			release.Card.ArtifactID:       testDigest("foreign"),
			release.Manifest.ArtifactID:   release.Manifest.Digest,
			release.Validation.ArtifactID: release.Validation.Digest,
		}
		_, err := Verify(raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}, Resolver: resolver})
		if reason, _ := ReasonOf(err); reason != ReasonDigestMismatch {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonDigestMismatch)
		}
	})

	t.Run("unresolved reference rejects", func(t *testing.T) {
		resolver := mapResolver{release.Card.ArtifactID: release.Card.Digest}
		_, err := Verify(raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}, Resolver: resolver})
		if reason, _ := ReasonOf(err); reason != ReasonUnresolvedReference {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnresolvedReference)
		}
	})
}

// TestSchemaMatchesStruct guards against drift between the normative JSON
// schema and the Go record: the root required set, the property set, and the
// derived_status enum must match exactly.
func TestSchemaMatchesStruct(t *testing.T) {
	path := filepath.Join("..", "..", "schemas", "pack-release-v1.0.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		AdditionalProperties bool           `json:"additionalProperties"`
		Required             []string       `json:"required"`
		Properties           map[string]any `json:"properties"`
		Defs                 map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("schema root must set additionalProperties:false")
	}

	structTags := map[string]struct{}{}
	recordType := reflect.TypeOf(PackRelease{})
	for i := 0; i < recordType.NumField(); i++ {
		tag := recordType.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		structTags[name] = struct{}{}
	}
	required := map[string]struct{}{}
	for _, name := range schema.Required {
		required[name] = struct{}{}
	}
	if !reflect.DeepEqual(required, structTags) {
		t.Fatalf("schema required set %v != struct tags %v", schema.Required, structTags)
	}
	properties := map[string]struct{}{}
	for name := range schema.Properties {
		properties[name] = struct{}{}
	}
	if !reflect.DeepEqual(properties, structTags) {
		t.Fatalf("schema property set != struct tags")
	}

	statusProp, ok := schema.Properties["derived_status"].(map[string]any)
	if !ok {
		t.Fatal("derived_status property missing")
	}
	enumValues, _ := statusProp["enum"].([]any)
	enum := map[string]struct{}{}
	for _, value := range enumValues {
		if s, ok := value.(string); ok {
			enum[s] = struct{}{}
		}
	}
	wantStatuses := map[string]struct{}{
		StatusDesign: {}, StatusExperimentalDark: {}, StatusShadow: {},
		StatusReleased: {}, StatusSuspended: {}, StatusRetired: {},
	}
	if !reflect.DeepEqual(enum, wantStatuses) {
		t.Fatalf("schema derived_status enum %v != %v", enum, wantStatuses)
	}
}
