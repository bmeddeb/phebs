package downstreamauthority

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority/authorityvalidate"
	"github.com/bmeddeb/phebs/internal/observationpublication"
)

func TestMaximumAuthorityFitsSharedCanonicalBound(t *testing.T) {
	observation := testObservationAuthority()
	observation.Repository = "r/" + strings.Repeat("x", 1022)
	observation.RecordCount = int(^uint(0) >> 1)
	observation.ObservedCount = observation.RecordCount
	required := make([]DomainIdentity, 64)
	domains := make([]candidate.DownstreamDomainAuthority, 64)
	for index := range domains {
		domain := fmt.Sprintf("%03d%s", index, strings.Repeat("d", 125))
		version := strings.Repeat("v", 128)
		required[index] = DomainIdentity{Domain: domain, Version: version}
		domains[index] = testDomainAuthority(
			domain, candidate.PartitionResultUnavailablePrerequisite, observation,
		)
		domains[index].Version = version
		domains[index].RunID = strings.Repeat("r", 512)
	}
	authority, err := BuildRequired(observation, required, domains)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	const historicalCallerPublicationBound = 64 << 10
	if len(raw) <= historicalCallerPublicationBound {
		t.Fatalf(
			"maximum authority = %d bytes, want proof it crosses historical %d-byte bound",
			len(raw), historicalCallerPublicationBound,
		)
	}
	if len(raw) > authorityvalidate.MaxCanonicalBytes {
		t.Fatalf(
			"maximum authority = %d bytes, shared bound = %d",
			len(raw), authorityvalidate.MaxCanonicalBytes,
		)
	}
	t.Logf("maximum canonical authority bytes = %d", len(raw))
}

func TestAuthorityBindsExactRequiredRootsAndFailsClosed(t *testing.T) {
	observation := testObservationAuthority()
	available := testDomainAuthority("proto-contract", candidate.PartitionResultSuccess, observation)
	value, err := BuildRequired(
		observation,
		[]DomainIdentity{{Domain: "scip", Version: "v1"}, {Domain: available.Domain, Version: available.Version}},
		[]candidate.DownstreamDomainAuthority{available},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(value); err != nil {
		t.Fatal(err)
	}
	if err := RequireUsable(value); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing required root = %v, want unavailable", err)
	}

	failed := testDomainAuthority("scip", candidate.PartitionResultTerminalRefusal, observation)
	value, err = BuildRequired(observation, []DomainIdentity{
		{Domain: available.Domain, Version: available.Version},
		{Domain: failed.Domain, Version: failed.Version},
	}, []candidate.DownstreamDomainAuthority{failed, available})
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireUsable(value); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failed required root = %v, want unavailable", err)
	}

	failed.Disposition = candidate.PartitionResultEmpty
	value, err = BuildRequired(observation, []DomainIdentity{
		{Domain: available.Domain, Version: available.Version},
		{Domain: failed.Domain, Version: failed.Version},
	}, []candidate.DownstreamDomainAuthority{failed, available})
	if err != nil || RequireUsable(value) != nil {
		t.Fatalf("exact successful/empty roots = %+v, %v", value, err)
	}

	failed.Disposition = candidate.PartitionResultUnavailablePrerequisite
	value, err = BuildRequired(observation, []DomainIdentity{
		{Domain: available.Domain, Version: available.Version},
		{Domain: failed.Domain, Version: failed.Version},
	}, []candidate.DownstreamDomainAuthority{failed, available})
	if err != nil || RequireUsable(value) != nil {
		t.Fatalf("explicit prerequisite gap roots = %+v, %v", value, err)
	}
}

func TestAuthorityRejectsNonCanonicalControls(t *testing.T) {
	observation := testObservationAuthority()
	domain := testDomainAuthority("proto-contract", candidate.PartitionResultSuccess, observation)
	value, err := Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}

	badDigest := value
	badDigest.Observation.SourceRootDigest = "sha256:" + strings.Repeat("A", 64)
	badDigest.Digest = digest(badDigest)
	if err := Validate(badDigest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uppercase digest validation = %v", err)
	}

	badToken := value
	badToken.Required[0].Domain = "proto\x00contract"
	badToken.Digest = digest(badToken)
	if err := Validate(badToken); !errors.Is(err, ErrInvalid) {
		t.Fatalf("control token validation = %v", err)
	}
}

func TestRunIDIsProvenanceOutsideV2DownstreamAuthority(t *testing.T) {
	observation := testObservationAuthority()
	domain := testDomainAuthority("proto-contract", candidate.PartitionResultSuccess, observation)
	first, err := Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	domain.RunID = "reminted-run"
	second, err := Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("run ID changed v2 downstream authority: %q != %q", first.Digest, second.Digest)
	}
	if first.ProvenanceDigest == second.ProvenanceDigest {
		t.Fatal("run ID did not change exact downstream provenance digest")
	}
	tampered := first
	tampered.Domains = append([]candidate.DownstreamDomainAuthority{}, first.Domains...)
	tampered.Domains[0].RunID = "tampered-run"
	if err := Validate(tampered); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered run provenance = %v, want invalid", err)
	}
	domain.RootDigest = digestFor("0")
	changed, err := Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Digest == first.Digest {
		t.Fatal("semantic domain root change preserved downstream authority")
	}
}

func TestHistoricalV1DownstreamAuthorityRemainsValid(t *testing.T) {
	observation := testObservationAuthority()
	domain := testDomainAuthority("proto-contract", candidate.PartitionResultSuccess, observation)
	value := Authority{
		Schema: authorityvalidate.SchemaV1, Repository: observation.Repository,
		Observation: observation,
		Required:    []DomainIdentity{{Domain: domain.Domain, Version: domain.Version}},
		Domains:     []candidate.DownstreamDomainAuthority{domain},
	}
	value.Digest = digest(value)
	if err := Validate(value); err != nil {
		t.Fatalf("historical v1 downstream authority rejected: %v", err)
	}
	domain.RunID = "different-run"
	changed := value
	changed.Domains = []candidate.DownstreamDomainAuthority{domain}
	changed.Digest = digest(changed)
	if changed.Digest == value.Digest {
		t.Fatal("historical v1 authority stopped binding run provenance")
	}
}

func testObservationAuthority() observationpublication.DownstreamAuthority {
	return observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: "example.com/acme/mono",
		SourceGenerationDigest: digestFor("1"), SourceRootDigest: digestFor("2"),
		ObservationGenerationDigest: digestFor("3"), ObservationRootDigest: digestFor("4"),
		PartitionPolicyDigest: digestFor("5"), ObservationPolicyDigest: digestFor("6"),
		InventoryPolicyDigest: digestFor("7"), RecordCount: 8, ObservedCount: 7,
	}
}

func testDomainAuthority(
	domain, disposition string,
	observation observationpublication.DownstreamAuthority,
) candidate.DownstreamDomainAuthority {
	return candidate.DownstreamDomainAuthority{
		Domain: domain, Version: "v1", PlanDigest: digestFor("8"), RootDigest: digestFor("9"),
		RunID: "run-" + domain, Disposition: disposition,
		CandidateManifestDigest: digestFor("a"), CandidatePartitionRootDigest: digestFor("b"),
		CandidatePolicyDigest: digestFor("c"), SourceGenerationDigest: observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      digestFor("d"), DomainIndexDigest: digestFor("e"),
		DomainScheduleDigest: digestFor("f"),
	}
}

func digestFor(value string) string { return "sha256:" + strings.Repeat(value, 64) }
