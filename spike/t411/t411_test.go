package t411

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestFrozenEnvelopeIsDeterministicAndExact(t *testing.T) {
	first, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := MarshalCanonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := MarshalCanonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("two T41.1 builds differ")
	}
	want := []struct {
		services, memberships, paths int
	}{
		{8_000, 48_000, 25_280},
		{10_000, 60_000, 31_600},
		{12_500, 75_000, 39_500},
	}
	for index, expected := range want {
		profile := first.Profiles[index]
		if profile.AcceptedServices != expected.services || profile.TotalServiceRecords != expected.services ||
			profile.Memberships != expected.memberships || profile.DistinctPaths != expected.paths ||
			profile.Fixture.RegularFiles != expected.paths || profile.MaxAcceptedPathFanout != 20 ||
			profile.MaxTotalClaimsPerPlacement != 20 {
			t.Fatalf("profile %d = %+v", expected.services, profile)
		}
	}
	if first.Boundary.MaxPlacement.UnbucketedRelationshipBytes <= relationshippublication.MaxProjectionBytes ||
		first.Boundary.MaxPlacement.MaxRelationshipBucketBytes > relationshippublication.MaxProjectionBytes ||
		first.Boundary.MaxPlacement.RelationshipBuckets != 8 ||
		first.Boundary.MaxPlacement.CatalogMember.Bytes > MaxCatalogMemberBytes ||
		first.Boundary.MaxService.Member.Bytes > MaxCatalogMemberBytes {
		t.Fatalf("maximum boundaries = %+v", first.Boundary)
	}
}

func TestEveryAggregateLimitAcceptsExactAndRefusesOneOver(t *testing.T) {
	tests := []struct {
		name  string
		limit int
	}{
		{"total_service_records", MaxTotalServices},
		{"memberships", MaxMemberships},
		{"distinct_paths", MaxDistinctPaths},
		{"successor_edges", MaxSuccessorEdges},
		{"service_successors", MaxServiceSuccessors},
		{"logical_bytes", MaxLogicalBytes},
		{"publication_bytes", MaxPublicationBytes},
		{"claims_per_placement", MaxClaimsPerPlacement},
		{"claims_per_bucket", MaxClaimsPerBucket},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := CheckLimit(testCase.name, testCase.limit); err != nil {
				t.Fatal(err)
			}
			if err := CheckLimit(testCase.name, testCase.limit+1); err == nil {
				t.Fatal("one-over value was accepted")
			}
		})
	}
}

func TestTransitionProfileUsesRealV2Semantics(t *testing.T) {
	envelope, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	transition := envelope.Transition
	if len(transition.Revisions) != 3 || len(transition.Cases) != 6 ||
		transition.Cases[0].Name != "proposal-to-accepted" ||
		transition.Cases[2].Successors[0] != "svc.alpha" ||
		transition.Cases[4].ExpectedIncarnation != 2 {
		t.Fatalf("transition profile = %+v", transition)
	}
	for _, revision := range transition.Revisions {
		raw, err := json.Marshal(revision.Catalog)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(`"unowned":null`)) {
			t.Fatalf("revision %q has null unowned placements", revision.Name)
		}
		if _, err := servicecatalog.Decode(raw); err != nil {
			t.Fatalf("revision %q is not wire-valid: %v", revision.Name, err)
		}
	}
}

func TestReusableTargetAndTransitionCorporaMatchFrozenEnvelope(t *testing.T) {
	envelope, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	target, err := BuildTargetCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(target.Profile, envelope.Profiles[1]) {
		t.Fatal("reusable target profile differs from the frozen envelope")
	}
	wantTargetDigest, err := ProfileDigest(envelope.Profiles[1])
	if err != nil {
		t.Fatal(err)
	}
	if target.ProfileDigest != wantTargetDigest ||
		target.Catalog.Schema != servicecatalog.Schema ||
		len(target.Catalog.Services) != AcceptedServiceTarget ||
		len(target.Catalog.Memberships) != 6*AcceptedServiceTarget ||
		len(target.Files) != target.Profile.Fixture.RegularFiles {
		t.Fatalf("reusable target corpus is incomplete: profile=%s services=%d memberships=%d files=%d",
			target.ProfileDigest, len(target.Catalog.Services), len(target.Catalog.Memberships), len(target.Files))
	}
	identity := TargetProfileIdentity()
	if identity.SHA256 != target.ProfileDigest ||
		identity.AcceptedServices != target.Profile.AcceptedServices ||
		identity.TotalServiceRecords != target.Profile.TotalServiceRecords ||
		identity.Memberships != target.Profile.Memberships ||
		identity.DistinctPaths != target.Profile.DistinctPaths ||
		identity.RegularFiles != target.Profile.Fixture.RegularFiles ||
		identity.FixtureContentBytes != target.Profile.Fixture.ContentBytes {
		t.Fatalf("allocation-free target identity = %+v", identity)
	}
	fixtureHash := sha256.New()
	var framedLength [8]byte
	for index, file := range target.Files {
		if index > 0 && target.Files[index-1].Path >= file.Path {
			t.Fatalf("fixture files are not strictly ordered at %q", file.Path)
		}
		if !bytes.Equal(file.Content, []byte("t411-neutral-fixture-v1\n"+file.Path+"\n")) {
			t.Fatalf("fixture content differs at %q", file.Path)
		}
		binary.BigEndian.PutUint64(framedLength[:], uint64(len(file.Path)))
		_, _ = fixtureHash.Write(framedLength[:])
		_, _ = fixtureHash.Write([]byte(file.Path))
		binary.BigEndian.PutUint64(framedLength[:], uint64(len(file.Content)))
		_, _ = fixtureHash.Write(framedLength[:])
		_, _ = fixtureHash.Write(file.Content)
	}
	if got := "sha256:" + hex.EncodeToString(fixtureHash.Sum(nil)); got != target.Profile.Fixture.SHA256 {
		t.Fatalf("reusable fixture digest = %s, want %s", got, target.Profile.Fixture.SHA256)
	}

	transition, err := BuildTransitionCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(transition.Profile, envelope.Transition) {
		t.Fatal("reusable transition profile differs from the frozen envelope")
	}
	wantTransitionDigest, err := TransitionProfileDigest(envelope.Transition)
	if err != nil {
		t.Fatal(err)
	}
	if transition.ProfileDigest != wantTransitionDigest {
		t.Fatalf("transition digest = %s, want %s", transition.ProfileDigest, wantTransitionDigest)
	}
	if transition.ProfileDigest != RetainedTransitionProfileSHA256 {
		t.Fatalf("retained transition digest = %s", transition.ProfileDigest)
	}
}

func TestMeasureBindsPreservedT323AndClosedCosts(t *testing.T) {
	envelope, receipt, err := Measure(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(receipt, envelope); err != nil {
		t.Fatal(err)
	}
	if receipt.Decision.ChangesProductionCaps || receipt.Decision.AcceptedTarget != 10_000 ||
		receipt.Decision.MaxTotalServiceRecords != 12_500 ||
		receipt.Decision.RelationshipRepresentation != "placement-claim-buckets-v1" {
		t.Fatalf("decision = %+v", receipt.Decision)
	}
	for _, measurement := range receipt.Profiles {
		if measurement.WallMicros < 1 || measurement.GoAllocatedBytes < 1 ||
			measurement.StoreTransaction.ImmutableRows < 1 ||
			measurement.Filesystem.RegularFiles < 1 || measurement.Lifecycle.CollectRows < 1 {
			t.Fatalf("measurement = %+v", measurement)
		}
	}
}

func TestRetainedArtifactsMatchFrozenEnvelope(t *testing.T) {
	want, err := BuildEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := MarshalCanonical(want)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := os.ReadFile("envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatal("retained envelope differs from frozen generator")
	}
	if digest := SHA256(gotBytes); digest != RetainedEnvelopeSHA256 {
		t.Fatalf("retained envelope digest = %s", digest)
	}
	decoded, err := DecodeStrict[Envelope](gotBytes)
	if err != nil || ValidateEnvelope(decoded) != nil {
		t.Fatalf("retained envelope validation = %v", err)
	}
	receiptBytes, err := os.ReadFile("receipt.json")
	if err != nil {
		t.Fatal(err)
	}
	if digest := SHA256(receiptBytes); digest != RetainedReceiptSHA256 {
		t.Fatalf("retained receipt digest = %s", digest)
	}
	receipt, err := DecodeStrict[Receipt](receiptBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(receipt, decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt.Boundary, decoded.Boundary) {
		t.Fatal("retained receipt boundary differs from envelope")
	}
}
