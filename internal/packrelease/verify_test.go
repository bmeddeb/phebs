package packrelease

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	release.Canonicalization = Canonicalization{Algorithm: CanonicalAlgorithm, Version: CanonicalVersion}
	release.Signature = Signature{KeyID: "key-1", Algorithm: SignatureAlgorithmEd25519}
	if mutate != nil {
		mutate(release)
	}
	if err := sign(release, private); err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := CanonicalPayload(release)
	if err != nil {
		t.Fatalf("CanonicalPayload: %v", err)
	}
	return raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}}
}

func TestVerifyAcceptsSignedRelease(t *testing.T) {
	raw, opts := signed(t, nil)
	release, err := Verify(context.Background(), raw, opts)
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
		if _, err := Verify(context.Background(), raw, opts); err != nil {
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
			_, err := Verify(context.Background(), raw, opts)
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
			_, err := Verify(context.Background(), test.raw, opts)
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
		_, err := Verify(context.Background(), raw, Options{Now: fixedNow, Keys: KeyRing{"other": public}})
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
		_, err = Verify(context.Background(), bytes, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
		if reason, _ := ReasonOf(err); reason != ReasonInvalidSignature {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonInvalidSignature)
		}
	})

	t.Run("unapproved algorithm", func(t *testing.T) {
		tampered := *release
		tampered.Signature.Algorithm = "rsa-pss"
		bytes, _ := CanonicalPayload(&tampered)
		_, err := Verify(context.Background(), bytes, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
		if reason, _ := ReasonOf(err); reason != ReasonUnapprovedAlgorithm {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnapprovedAlgorithm)
		}
	})

	t.Run("unsupported canonicalization", func(t *testing.T) {
		tampered := *release
		tampered.Canonicalization.Algorithm = "something-else"
		bytes, _ := CanonicalPayload(&tampered)
		_, err := Verify(context.Background(), bytes, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
		if reason, _ := ReasonOf(err); reason != ReasonUnsupportedCanon {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnsupportedCanon)
		}
	})
}

type mapResolver map[string]string

func (m mapResolver) Resolve(_ context.Context, id string) (string, bool, error) {
	digest, ok := m[id]
	return digest, ok, nil
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
		if _, err := Verify(context.Background(), raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}, Resolver: resolver}); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("mismatched digest rejects", func(t *testing.T) {
		resolver := mapResolver{
			release.Card.ArtifactID:       testDigest("foreign"),
			release.Manifest.ArtifactID:   release.Manifest.Digest,
			release.Validation.ArtifactID: release.Validation.Digest,
		}
		_, err := Verify(context.Background(), raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}, Resolver: resolver})
		if reason, _ := ReasonOf(err); reason != ReasonDigestMismatch {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonDigestMismatch)
		}
	})

	t.Run("unresolved reference rejects", func(t *testing.T) {
		resolver := mapResolver{release.Card.ArtifactID: release.Card.Digest}
		_, err := Verify(context.Background(), raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}, Resolver: resolver})
		if reason, _ := ReasonOf(err); reason != ReasonUnresolvedReference {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnresolvedReference)
		}
	})
}

// TestSchemaMatchesStruct guards against drift between the normative JSON
// schema and the Go record: the root required set, the property set, the
// derived_status enum, and additionalProperties:false on every object. The
// value bounds are held by TestVerifyEnforcesSchemaBounds.
func TestSchemaMatchesStruct(t *testing.T) {
	path := filepath.Join("..", "..", "schemas", "pack-release-v1.0.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Required             []string       `json:"required"`
		Properties           map[string]any `json:"properties"`
		Defs                 map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("schema root must set additionalProperties:false")
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch value := node.(type) {
		case map[string]any:
			if value["type"] == "object" && value["additionalProperties"] != false {
				t.Errorf("schema object %s must set additionalProperties:false", path)
			}
			for key, child := range value {
				walk(path+"/"+key, child)
			}
		case []any:
			for index, child := range value {
				walk(fmt.Sprintf("%s/%d", path, index), child)
			}
		}
	}
	walk("#", generic)

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

type countingResolver struct{ calls int }

func (r *countingResolver) Resolve(context.Context, string) (string, bool, error) {
	r.calls++
	return "", false, nil
}

type failingResolver struct{}

func (failingResolver) Resolve(context.Context, string) (string, bool, error) {
	return "", false, errors.New("artifact store unavailable")
}

// TestVerifyChecksSignatureFirst: an unsigned record learns nothing about its
// own expiry and never reaches the resolver.
func TestVerifyChecksSignatureFirst(t *testing.T) {
	raw, _ := signed(t, func(r *PackRelease) { r.Validation.ExpiresAt = "2020-01-01T00:00:00Z" })
	resolver := &countingResolver{}
	_, err := Verify(context.Background(), raw, Options{Now: fixedNow, Resolver: resolver})
	if reason, _ := ReasonOf(err); reason != ReasonUnknownKey || resolver.calls != 0 {
		t.Fatalf("reason = %q (%v), resolver calls = %d; want %q and 0", reason, err, resolver.calls, ReasonUnknownKey)
	}
}

// TestVerifyRejectsNonCanonicalSignatureEncoding: the value is outside the
// signed payload, so a line break or stray padding bits must not yield a
// second record that verifies.
func TestVerifyRejectsNonCanonicalSignatureEncoding(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for name, respell := range map[string]func(string) string{
		"line break": func(v string) string { return v[:10] + "\n" + v[10:] },
		"padding bits": func(v string) string {
			chars := []byte(v)
			last := len(chars) - 3 // the character before "=="
			chars[last] = alphabet[strings.IndexByte(alphabet, chars[last])|1]
			return string(chars)
		},
	} {
		t.Run(name, func(t *testing.T) {
			public, private := testKey(t)
			release := validRelease()
			if err := Sign(release, "key-1", private); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			release.Signature.Value = respell(release.Signature.Value)
			raw, _ := CanonicalPayload(release)
			_, err := Verify(context.Background(), raw, Options{Now: fixedNow, Keys: KeyRing{"key-1": public}})
			if reason, _ := ReasonOf(err); reason != ReasonInvalidSignature {
				t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonInvalidSignature)
			}
		})
	}
}

// TestVerifyEnforcesSchemaBounds holds the Go verifier to the value bounds of
// schemas/pack-release-v1.0.json.
func TestVerifyEnforcesSchemaBounds(t *testing.T) {
	longTrigger := strings.Repeat("x", 257)
	tests := []struct {
		name   string
		mutate func(*PackRelease)
		reason Reason
	}{
		{"uppercase digest", func(r *PackRelease) { r.Card.Digest = "sha256:" + strings.ToUpper(r.Card.Digest[7:]) }, ReasonMalformedDigest},
		{"65 approval records", func(r *PackRelease) {
			r.ApprovalRecords = nil
			for i := 0; i < 65; i++ {
				r.ApprovalRecords = append(r.ApprovalRecords, fmt.Sprintf("approval-%d", i))
			}
		}, ReasonInvalidField},
		{"257-character expiry trigger", func(r *PackRelease) {
			r.DerivedStatus = StatusSuspended
			r.Validation.ExpiryTrigger = &longTrigger
		}, ReasonInvalidField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, opts := signed(t, test.mutate)
			_, err := Verify(context.Background(), raw, opts)
			if reason, _ := ReasonOf(err); reason != test.reason {
				t.Fatalf("reason = %q (%v), want %q", reason, err, test.reason)
			}
		})
	}

	t.Run("null approval records", func(t *testing.T) {
		raw, opts := signed(t, func(r *PackRelease) { r.DerivedStatus = StatusSuspended; r.ApprovalRecords = nil })
		if !strings.Contains(string(raw), `"approval_records":[]`) {
			t.Fatalf("absent approvals are not canonically []:\n%s", raw)
		}
		null := strings.Replace(string(raw), `"approval_records":[]`, `"approval_records":null`, 1)
		_, err := Verify(context.Background(), []byte(null), opts)
		if reason, _ := ReasonOf(err); reason != ReasonNonCanonical {
			t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonNonCanonical)
		}
	})
}

func TestVerifyBindings(t *testing.T) {
	raw, opts := signed(t, nil)
	release := validRelease()
	foreign := release.Implementation
	foreign.PhebsBinaryDigest = testDigest("other-binary")
	tests := []struct {
		name   string
		adjust func(*Options)
		reason Reason
	}{
		{"foreign implementation", func(o *Options) { o.Implementation = &foreign }, ReasonDigestMismatch},
		{"foreign artifact root", func(o *Options) { o.ReferencedArtifactsRootDigest = testDigest("other-root") }, ReasonDigestMismatch},
		{"withdrawn release", func(o *Options) { o.Revoked = map[string]struct{}{release.ReleaseID: {}} }, ReasonRevoked},
		{"resolver failure", func(o *Options) { o.Resolver = failingResolver{} }, ReasonUnresolvedReference},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adjusted := opts
			test.adjust(&adjusted)
			_, err := Verify(context.Background(), raw, adjusted)
			if reason, _ := ReasonOf(err); reason != test.reason {
				t.Fatalf("reason = %q (%v), want %q", reason, err, test.reason)
			}
		})
	}
}

func TestVerifyForLoad(t *testing.T) {
	release := validRelease()
	bound := func(opts Options) Options {
		opts.Implementation = &release.Implementation
		opts.ReferencedArtifactsRootDigest = release.ReferencedArtifactsRootDigest
		opts.Resolver = mapResolver{
			release.Card.ArtifactID:       release.Card.Digest,
			release.Manifest.ArtifactID:   release.Manifest.Digest,
			release.Validation.ArtifactID: release.Validation.Digest,
		}
		return opts
	}

	raw, opts := signed(t, nil)
	if _, err := VerifyForLoad(context.Background(), raw, bound(opts)); err != nil {
		t.Fatalf("VerifyForLoad: %v", err)
	}
	if _, err := VerifyForLoad(context.Background(), raw, opts); err == nil {
		t.Fatal("VerifyForLoad admitted a release without its bindings")
	} else if reason, _ := ReasonOf(err); reason != ReasonUnresolvedReference {
		t.Fatalf("reason = %q (%v), want %q", reason, err, ReasonUnresolvedReference)
	}
	for _, status := range []string{StatusDesign, StatusExperimentalDark, StatusShadow, StatusSuspended, StatusRetired} {
		raw, opts := signed(t, func(r *PackRelease) { r.DerivedStatus = status })
		_, err := VerifyForLoad(context.Background(), raw, bound(opts))
		if reason, _ := ReasonOf(err); reason != ReasonNotReleased {
			t.Fatalf("VerifyForLoad(%s): reason = %q (%v), want %q", status, reason, err, ReasonNotReleased)
		}
	}
}

func TestSignRefusesRecordsVerifyRejects(t *testing.T) {
	_, private := testKey(t)
	tests := []struct {
		name   string
		mutate func(*PackRelease)
		reason Reason
	}{
		{"released without approvals", func(r *PackRelease) { r.ApprovalRecords = nil }, ReasonMissingApproval},
		{"malformed digest", func(r *PackRelease) { r.Manifest.Digest = "sha256:zz" }, ReasonMalformedDigest},
		{"unsupported schema", func(r *PackRelease) { r.ReleaseSchemaVersion = "2.0" }, ReasonUnsupportedSchema},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release := validRelease()
			test.mutate(release)
			err := Sign(release, "key-1", private)
			if reason, _ := ReasonOf(err); reason != test.reason || release.Signature.Value != "" {
				t.Fatalf("Sign reason = %q (%v), value %q; want %q and no value",
					reason, err, release.Signature.Value, test.reason)
			}
		})
	}
}

func TestReleaseErrorUnwrapsCause(t *testing.T) {
	raw, opts := signed(t, func(r *PackRelease) { r.ApprovedAt = "yesterday" })
	_, err := Verify(context.Background(), raw, opts)
	var parseErr *time.ParseError
	if !errors.Is(err, ErrInvalidRelease) || !errors.As(err, &parseErr) {
		t.Fatalf("err = %v; want ErrInvalidRelease wrapping a *time.ParseError", err)
	}
}
