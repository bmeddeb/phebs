package t411

import (
	"bytes"
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
	if digest := SHA256(gotBytes); digest != "sha256:99ec8a3dc79537bf1db842234f6fe054abd03c9af7503987f78c5530fdfd525f" {
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
	if digest := SHA256(receiptBytes); digest != "sha256:c9a30ab63960fee682558a04e79b66f1d1fcf2b9a7f2bfc2e3a012139291dc55" {
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
