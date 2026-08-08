package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/reponame"
	"github.com/bmeddeb/phebs/internal/store"
)

type relationshipCoverageFixture struct {
	repositories []string
	coverage     RelationshipProofCoverage
}

type relationshipCitationAuthorizationStore struct {
	store.Store
	repository store.Repo
	calls      int
	onGet      func()
}

func (value *relationshipCitationAuthorizationStore) GetRepo(
	_ context.Context, name string,
) (*store.Repo, error) {
	value.calls++
	if value.onGet != nil {
		value.onGet()
	}
	if name != value.repository.Name {
		return nil, store.ErrNotFound
	}
	repository := value.repository
	return &repository, nil
}

func TestRelationshipCitationAuthorizesBeforeBindingLookup(t *testing.T) {
	repository := store.Repo{
		Name: "example.com/acme/visible", IndexedCommitHash: strings.Repeat("a", 40),
		EvidenceRevision: 3,
	}
	state := &relationshipCitationAuthorizationStore{repository: repository}
	service := NewRelationshipService(Options{
		Store: state, DataDir: t.TempDir(),
		Principal: func(context.Context) string { return "reader" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(store.Repo) bool { return false }
		},
	}, &relationshippublication.Cache{})
	if service == nil {
		t.Fatal("relationship service was not constructed")
	}
	binding := &relationshipBinding{
		id: "present", createdAt: time.Now(),
		sources: []relationshipSource{{repository: repository}},
	}
	service.bindings[binding.id] = binding
	usesAtAuthorization := -1
	state.onGet = func() { usesAtAuthorization = binding.uses }
	token := service.encodeSigned(relationshipCitationToken{
		Schema: relationshipCitationSchema, Binding: binding.id,
		Repository: repository.Name, Source: 0, Projection: "sha256:" + strings.Repeat("1", 64),
	})
	_, err := service.ReadCitation(t.Context(), token)
	if err == nil || state.calls != 1 || usesAtAuthorization != 0 || binding.uses != 0 {
		t.Fatalf("citation authorization order: calls=%d uses-at-auth=%d uses=%d err=%v",
			state.calls, usesAtAuthorization, binding.uses, err)
	}
}

func TestRelationshipReceiptsPreserveV1AndCompleteV2GenerationSeams(t *testing.T) {
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: "example.com/acme/one",
		SourceGenerationDigest: digest("1"), SourceRootDigest: digest("2"),
		ObservationGenerationDigest: digest("3"), ObservationRootDigest: digest("4"),
		PartitionPolicyDigest: digest("5"), ObservationPolicyDigest: digest("6"),
		InventoryPolicyDigest: digest("7"), RecordCount: 12, ObservedCount: 10,
	}
	domain := candidate.DownstreamDomainAuthority{
		Domain: "scip", Version: "v1", PlanDigest: digest("8"), RootDigest: digest("9"),
		RunID: "partition-run", Disposition: candidate.PartitionResultEmpty,
		CandidateManifestDigest: digest("a"), CandidatePartitionRootDigest: digest("b"),
		CandidatePolicyDigest: digest("c"), SourceGenerationDigest: observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      digest("d"), DomainIndexDigest: digest("e"),
		DomainScheduleDigest: digest("f"),
	}
	upstream, err := downstreamauthority.Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	v1 := relationshippublication.Root{
		Schema:           relationshippublication.RootSchema,
		Authority:        relationshippublication.Authority{Repository: "example.com/acme/legacy"},
		GenerationDigest: digest("1"), Digest: digest("2"), AuthorityDigest: digest("3"),
	}
	v2 := relationshippublication.Root{
		Schema: relationshippublication.RootSchemaV2,
		Authority: relationshippublication.Authority{
			Repository: "example.com/acme/one", Upstream: &upstream,
			ServiceStateSummaryDigest: digest("4"), ServiceStateControlRevision: 11,
		},
		GenerationDigest: digest("5"), Digest: digest("6"), AuthorityDigest: digest("7"),
	}
	receipts := relationshipReceipts([]relationshipSource{
		{repository: store.Repo{Name: "example.com/acme/legacy"},
			publication: new(relationshippublication.Publication), root: v1, state: "empty"},
		{repository: store.Repo{Name: "example.com/acme/one"},
			publication: new(relationshippublication.Publication), root: v2, state: "complete"},
	})
	if len(receipts) != 2 || receipts[0].RootSchema != relationshippublication.RootSchema ||
		receipts[0].Authority == nil || receipts[0].Authority.Upstream != nil {
		t.Fatalf("retained v1 receipt = %+v", receipts[0])
	}
	authority := receipts[1].Authority
	if receipts[1].RootSchema != relationshippublication.RootSchemaV2 || authority == nil ||
		authority.ServiceStateSummaryDigest != digest("4") ||
		authority.ServiceStateControlRevision != 11 || authority.Upstream == nil ||
		authority.Upstream.Digest != upstream.Digest ||
		authority.Upstream.Observation.ObservationRootDigest != observation.ObservationRootDigest ||
		authority.Upstream.RequiredDomainCount != 1 ||
		authority.Upstream.PublishedDomainCount != 1 || len(authority.Upstream.Gaps) != 0 {
		t.Fatalf("v2 receipt = %+v", receipts[1])
	}
}

func TestRelationshipV2AuthorityInventoryLeavesBoundedResponseHeadroom(t *testing.T) {
	digest := func(value byte) string { return "sha256:" + strings.Repeat(string(value), 64) }
	observation := observationpublication.DownstreamAuthority{
		Version: observationpublication.DownstreamAuthorityV2, Repository: "example.com/acme/one",
		SourceGenerationDigest: digest('1'), SourceRootDigest: digest('2'),
		ObservationGenerationDigest: digest('3'), ObservationRootDigest: digest('4'),
		PartitionPolicyDigest: digest('5'), ObservationPolicyDigest: digest('6'),
		InventoryPolicyDigest: digest('7'), RecordCount: 2_000_000, ObservedCount: 2_000_000,
	}
	domains := make([]candidate.DownstreamDomainAuthority, 64)
	for index := range domains {
		domainPrefix := fmt.Sprintf("domain-%02d-", index)
		runPrefix := fmt.Sprintf("run-%02d-", index)
		domains[index] = candidate.DownstreamDomainAuthority{
			Domain:     domainPrefix + strings.Repeat("d", 128-len(domainPrefix)),
			Version:    strings.Repeat("v", 128),
			PlanDigest: digest('8'), RootDigest: digest('9'),
			RunID:                   runPrefix + strings.Repeat("r", 512-len(runPrefix)),
			Disposition:             candidate.PartitionResultTerminalRefusal,
			CandidateManifestDigest: digest('a'), CandidatePartitionRootDigest: digest('b'),
			CandidatePolicyDigest: digest('c'), SourceGenerationDigest: observation.SourceGenerationDigest,
			ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ExtractionPolicyDigest:      digest('d'), DomainIndexDigest: digest('e'),
			DomainScheduleDigest: digest('f'),
		}
	}
	upstream, err := downstreamauthority.Build(observation, domains)
	if err != nil {
		t.Fatal(err)
	}
	sources := make([]relationshipSource, relationshipMaxRepositories)
	for index := range sources {
		repositoryPrefix := fmt.Sprintf("example.com/repository-%02d/", index)
		repository := repositoryPrefix + strings.Repeat("p", reponame.MaxBytes-len(repositoryPrefix))
		rootUpstream := upstream
		rootUpstream.Repository, rootUpstream.Observation.Repository = repository, repository
		authority := relationshippublication.Authority{
			Repository: repository, CatalogGenerationDigest: digest('1'), CatalogDigest: digest('2'),
			CatalogSourceGeneration: digest('3'), ServiceStateSetDigest: digest('4'),
			ServiceStateSummaryDigest: digest('5'), ServiceStateControlRevision: ^uint64(0),
			ObservationGenerationDigest: digest('6'), ObservationManifestDigest: digest('7'),
			ObservationSourceDigest: digest('8'), ResolverGenerationDigest: digest('9'),
			ResolverRootDigest: digest('a'), RPCGenerationDigest: digest('b'), RPCRootDigest: digest('c'),
			KafkaGenerationDigest: digest('d'), KafkaRootDigest: digest('e'), PolicyDigest: digest('f'),
			Upstream: &rootUpstream,
		}
		sources[index] = relationshipSource{
			repository: store.Repo{Name: repository}, publication: new(relationshippublication.Publication),
			root: relationshippublication.Root{
				Schema:           relationshippublication.RootSchemaV2,
				Authority:        authority,
				GenerationDigest: digest('1'), Digest: digest('2'), AuthorityDigest: digest('3'),
			},
			receipt: relationshippublication.ServiceReceipt{ServiceKey: strings.Repeat("s", relationshippublication.MaxTextBytes)},
			state:   "empty",
		}
	}
	raw, err := json.Marshal(relationshipReceipts(sources))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > relationshipResponseBytes/2 {
		t.Fatalf("maximum v2 root inventory = %d bytes, want at most %d bytes of the response envelope",
			len(raw), relationshipResponseBytes/2)
	}
}

func (fixture *relationshipCoverageFixture) RootCoverage(
	_ context.Context,
	repositories []string,
) (*RelationshipProofCoverage, error) {
	fixture.repositories = slices.Clone(repositories)
	value := fixture.coverage
	value.Roots = slices.Clone(value.Roots)
	return &value, nil
}

func TestWorkbenchRelationshipCoverageBindsExactRootDigest(t *testing.T) {
	fixture := &relationshipCoverageFixture{coverage: RelationshipProofCoverage{
		SchemaVersion: "phebs-relationship-proof-coverage-v1", State: "exact",
		ExactRootCount: 2, Digest: "sha256:exact-root-set",
		Roots: []RelationshipRootReceipt{
			{Repository: "example.com/acme/one", State: "complete", Generation: "sha256:one"},
			{Repository: "example.com/acme/two", State: "complete", Generation: "sha256:two"},
		},
	}}
	coverage, err := workbenchRelationshipCoverage(t.Context(), fixture, []store.ChangeBriefContractSelection{
		{Repository: "example.com/acme/two"},
		{Repository: "example.com/acme/one"},
		{Repository: "example.com/acme/two"},
	})
	if err != nil || coverage.Digest != "sha256:exact-root-set" ||
		!slices.Equal(fixture.repositories, []string{"example.com/acme/one", "example.com/acme/two"}) {
		t.Fatalf("workbench relationship coverage = %+v repositories=%v err=%v", coverage, fixture.repositories, err)
	}
	cursor := workbenchImpactCursor{}
	if err := bindWorkbenchImpactDigest(
		"relationship coverage", &cursor.RelationshipDigest, coverage.Digest, false,
	); err != nil || cursor.RelationshipDigest != coverage.Digest {
		t.Fatalf("relationship cursor digest = %q, %v", cursor.RelationshipDigest, err)
	}
	if err := bindWorkbenchImpactDigest(
		"relationship coverage", &cursor.RelationshipDigest, "sha256:changed", true,
	); err == nil {
		t.Fatal("changed relationship root set did not invalidate the Workbench cursor")
	}
}
