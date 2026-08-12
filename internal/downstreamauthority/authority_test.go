package downstreamauthority

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/observationpublication"
)

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
