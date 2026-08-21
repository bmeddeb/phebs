package authorityvalidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalSupportsHistoricalAndCurrentAuthoritySchemas(t *testing.T) {
	for _, schema := range []string{SchemaV1, Schema} {
		t.Run(schema, func(t *testing.T) {
			value := sealedTestAuthority(schema)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Canonical(raw)
			if err != nil || !result.Usable || result.Repository != value.Repository || result.Digest != value.Digest {
				t.Fatalf("canonical result = %+v, %v", result, err)
			}
		})
	}
}

func TestCanonicalRejectsMalformedCurrentAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*authority)
	}{
		{name: "provenance digest", mutate: func(value *authority) { value.ProvenanceDigest = testDigest("0") }},
		{name: "unknown schema", mutate: func(value *authority) { value.Schema = "phebs-downstream-upstream-authority-v3" }},
		{name: "domain disposition", mutate: func(value *authority) { value.Domains[0].Disposition = "unknown" }},
		{name: "run identity", mutate: func(value *authority) { value.Domains[0].RunID = " leading" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := sealedTestAuthority(Schema)
			test.mutate(&value)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Canonical(raw); err == nil {
				t.Fatal("malformed current authority was accepted")
			}
		})
	}
}

func sealedTestAuthority(schema string) authority {
	value := authority{
		Schema: schema, Repository: "github.com/acme/authority-validation",
		Observation: observation{
			Version: ObservationVersionV2, Repository: "github.com/acme/authority-validation",
			SourceGenerationDigest: testDigest("1"), SourceRootDigest: testDigest("2"),
			ObservationGenerationDigest: testDigest("3"), ObservationRootDigest: testDigest("4"),
			PartitionPolicyDigest: testDigest("5"), ObservationPolicyDigest: testDigest("6"),
			InventoryPolicyDigest: testDigest("7"), RecordCount: 1, ObservedCount: 1,
		},
		Required: []domainIdentity{{Domain: "grpc-caller", Version: "1.0.0"}},
		Domains: []DomainAuthority{{
			Domain: "grpc-caller", Version: "1.0.0", PlanDigest: testDigest("8"),
			RootDigest: testDigest("9"), RunID: "run-1", Disposition: "empty",
			CandidateManifestDigest: testDigest("a"), CandidatePartitionRootDigest: testDigest("b"),
			CandidatePolicyDigest: testDigest("c"), SourceGenerationDigest: testDigest("1"),
			ObservationGenerationDigest: testDigest("3"), ExtractionPolicyDigest: testDigest("d"),
			DomainIndexDigest: testDigest("e"), DomainScheduleDigest: testDigest("f"),
		}},
	}
	if schema == Schema {
		provenance := value
		provenanceRaw, _ := json.Marshal(provenance)
		sum := sha256.Sum256(append([]byte(schema+"-provenance\x00"), provenanceRaw...))
		value.ProvenanceDigest = "sha256:" + hex.EncodeToString(sum[:])
	}
	identity := value
	identity.Digest = ""
	if schema == Schema {
		identity.ProvenanceDigest = ""
		identity.Domains = append([]DomainAuthority(nil), identity.Domains...)
		identity.Domains[0].RunID = ""
	}
	identityRaw, _ := json.Marshal(identity)
	sum := sha256.Sum256(append([]byte(schema+"\x00"), identityRaw...))
	value.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return value
}

func testDigest(value string) string { return "sha256:" + strings.Repeat(value, 64) }
