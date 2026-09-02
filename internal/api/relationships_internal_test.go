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

type relationshipSelectorStore struct {
	store.Store
	repositories   map[string]store.Repo
	selectors      map[string]store.ServiceRuntimeSelector
	calls          *[]string
	selectorReads  int
	selectorAtRead int
	selectorOnRead store.ServiceRuntimeSelector
}

func (value *relationshipSelectorStore) GetRepo(
	_ context.Context,
	name string,
) (*store.Repo, error) {
	if value.calls != nil {
		*value.calls = append(*value.calls, "authorize:"+name)
	}
	repository, present := value.repositories[name]
	if !present {
		return nil, store.ErrNotFound
	}
	return &repository, nil
}

func (value *relationshipSelectorStore) ListRepos(context.Context) ([]store.Repo, error) {
	result := make([]store.Repo, 0, len(value.repositories))
	for _, repository := range value.repositories {
		result = append(result, repository)
	}
	return result, nil
}

func (value *relationshipSelectorStore) GetServiceRuntimeSelector(
	_ context.Context,
	repository string,
) (store.ServiceRuntimeSelector, error) {
	value.selectorReads++
	if value.selectorAtRead > 0 && value.selectorReads >= value.selectorAtRead &&
		value.selectorOnRead.Repository == repository {
		return value.selectorOnRead, nil
	}
	selector, present := value.selectors[repository]
	if !present {
		return store.ServiceRuntimeSelector{}, store.ErrNotFound
	}
	return selector, nil
}

func (value *relationshipSelectorStore) ConfirmServiceRuntimeSelector(
	_ context.Context,
	selector store.ServiceRuntimeSelector,
) error {
	if value.calls != nil {
		*value.calls = append(*value.calls, "selector:"+selector.Repository)
	}
	current, present := value.selectors[selector.Repository]
	if !present || current != selector {
		return store.ErrConflict
	}
	return nil
}

type relationshipPublicationFenceFake struct {
	relationshipPublication
	calls *[]string
	name  string
}

func (value relationshipPublicationFenceFake) ConfirmCurrent() error {
	*value.calls = append(*value.calls, "current:"+value.name)
	return nil
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

func TestRelationshipRuntimeSelectionDefaultsAndDoesNotFallback(t *testing.T) {
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	repositories := map[string]store.Repo{
		"example.com/acme/legacy": {
			Name: "example.com/acme/legacy", IndexedCommitHash: strings.Repeat("1", 40),
		},
		"example.com/acme/v2": {
			Name: "example.com/acme/v2", IndexedCommitHash: strings.Repeat("3", 40),
		},
		"example.com/acme/selected": {
			Name: "example.com/acme/selected", IndexedCommitHash: strings.Repeat("2", 40),
		},
	}
	selector := store.ServiceRuntimeSelector{
		Schema: store.ServiceRuntimeSelectorSchema, Repository: "example.com/acme/selected",
		Backend: store.ServiceRuntimeV3, CatalogRootDigest: digest("1"),
		CatalogControlRevision: 1, StateControlRevision: 2,
		StateSummaryDigest: digest("2"), SearchGenerationDigest: digest("3"),
		RelationshipGenerationDigest: digest("4"), RelationshipRootDigest: digest("5"),
		ControlRevision: 1, Digest: digest("6"), ChangedAt: time.Unix(1, 0).UTC(),
	}
	v2Selector := selector
	v2Selector.Repository = "example.com/acme/v2"
	v2Selector.Backend = store.ServiceRuntimeV2
	v2Selector.CatalogGenerationDigest = digest("7")
	v2Selector.CatalogRootDigest = ""
	v2Selector.Digest = digest("8")
	state := &relationshipSelectorStore{
		repositories: repositories,
		selectors: map[string]store.ServiceRuntimeSelector{
			selector.Repository: selector, v2Selector.Repository: v2Selector,
		},
	}
	service := NewRuntimeRelationshipService(Options{
		Store: state, DataDir: t.TempDir(),
	}, &relationshippublication.Cache{}, &relationshippublication.CacheV3{})
	if service == nil {
		t.Fatal("runtime relationship service was not constructed")
	}
	legacy, err := service.relationshipRuntime(t.Context(), "example.com/acme/legacy")
	if err != nil || legacy.backend != store.ServiceRuntimeV2 || legacy.selector != nil {
		t.Fatalf("legacy runtime = %+v, %v", legacy, err)
	}
	v2, err := service.relationshipRuntime(t.Context(), v2Selector.Repository)
	if err != nil || v2.backend != store.ServiceRuntimeV2 ||
		v2.selector == nil || *v2.selector != v2Selector {
		t.Fatalf("selected v2 runtime = %+v, %v", v2, err)
	}
	selected, err := service.relationshipRuntime(t.Context(), selector.Repository)
	if err != nil || selected.backend != store.ServiceRuntimeV3 ||
		selected.selector == nil || *selected.selector != selector {
		t.Fatalf("selected runtime = %+v, %v", selected, err)
	}
	source, err := service.openRuntimeRoot(
		t.Context(), repositories[selector.Repository], selected,
	)
	if err == nil || source.publication != nil {
		t.Fatalf("missing selected v3 root fell back to legacy: source=%+v err=%v", source, err)
	}
}

func TestRelationshipV3AuthorityNormalizesAndMatchesSelector(t *testing.T) {
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	repository := "example.com/acme/selected"
	value := relationshippublication.RootV3{
		Schema: relationshippublication.RootSchemaV3,
		Authority: relationshippublication.AuthorityV3{
			Repository: repository, CatalogRootDigest: digest("1"),
			CatalogLogicalDigest: digest("2"), CatalogSourceGeneration: digest("3"),
			CatalogControlRevision: 6,
			ServiceStateSetDigest:  digest("4"), ServiceStateSummaryDigest: digest("5"),
			ServiceStateControlRevision: 7, ObservationGenerationDigest: digest("6"),
			ObservationManifestDigest: digest("7"), ObservationSourceDigest: digest("8"),
			ResolverGenerationDigest: digest("9"), ResolverRootDigest: digest("a"),
			RPCGenerationDigest: digest("b"), RPCRootDigest: digest("c"),
			KafkaGenerationDigest: digest("d"), KafkaRootDigest: digest("e"),
			PolicyDigest: digest("f"),
		},
		AuthorityDigest: digest("a"), RepositoryComplete: true, AllServicesComplete: true,
		GenerationDigest: digest("b"), Digest: digest("c"),
	}
	root := normalizeRelationshipRootV3(value)
	selector := store.ServiceRuntimeSelector{
		Repository: repository, Backend: store.ServiceRuntimeV3,
		CatalogRootDigest:            value.Authority.CatalogRootDigest,
		CatalogControlRevision:       value.Authority.CatalogControlRevision,
		StateSummaryDigest:           value.Authority.ServiceStateSummaryDigest,
		StateControlRevision:         value.Authority.ServiceStateControlRevision,
		RelationshipGenerationDigest: value.GenerationDigest,
		RelationshipRootDigest:       value.Digest,
	}
	if root.Schema != relationshippublication.RootSchemaV3 ||
		root.Authority.CatalogGenerationDigest != value.Authority.CatalogRootDigest ||
		root.Authority.CatalogDigest != value.Authority.CatalogLogicalDigest ||
		root.Authority.Upstream == nil || !relationshipRootMatchesSelector(
		root, value.Authority.CatalogControlRevision, selector,
	) {
		t.Fatalf("normalized v3 relationship root = %+v", root)
	}
}

func TestRelationshipSelectorConfirmationIsTheFinalFence(t *testing.T) {
	repositories := map[string]store.Repo{
		"example.com/acme/legacy": {
			Name: "example.com/acme/legacy", IndexedCommitHash: strings.Repeat("1", 40),
		},
		"example.com/acme/selected": {
			Name: "example.com/acme/selected", IndexedCommitHash: strings.Repeat("2", 40),
		},
	}
	selector := store.ServiceRuntimeSelector{
		Repository: "example.com/acme/selected", Backend: store.ServiceRuntimeV3,
		Digest: "sha256:" + strings.Repeat("a", 64),
	}
	var calls []string
	state := &relationshipSelectorStore{
		repositories: repositories,
		selectors:    map[string]store.ServiceRuntimeSelector{selector.Repository: selector},
		calls:        &calls,
	}
	service := NewRuntimeRelationshipService(Options{
		Store: state, DataDir: t.TempDir(),
	}, &relationshippublication.Cache{}, &relationshippublication.CacheV3{})
	resolved, authorization, err := service.authorizeRepositories(
		t.Context(), []string{"example.com/acme/legacy", "example.com/acme/selected"},
	)
	if err != nil {
		t.Fatal(err)
	}
	calls = nil
	binding := &relationshipBinding{
		authorization: authorization, repositories: resolved,
		sources: []relationshipSource{
			{repository: repositories["example.com/acme/legacy"], current: true,
				publication: relationshipPublicationFenceFake{calls: &calls, name: "legacy"}},
			{repository: repositories["example.com/acme/selected"], selector: &selector},
		},
	}
	if err := service.confirmBinding(t.Context(), binding, authorization); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"authorize:example.com/acme/legacy", "authorize:example.com/acme/selected",
		"current:legacy", "selector:example.com/acme/selected",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("fence order = %v, want %v", calls, want)
	}
}

func TestRelationshipImplicitV2RefusesSelectorActivation(t *testing.T) {
	repository := store.Repo{
		Name: "example.com/acme/legacy", IndexedCommitHash: strings.Repeat("1", 40),
	}
	state := &relationshipSelectorStore{
		repositories:   map[string]store.Repo{repository.Name: repository},
		selectors:      map[string]store.ServiceRuntimeSelector{},
		selectorAtRead: 2,
		selectorOnRead: store.ServiceRuntimeSelector{
			Repository: repository.Name, Backend: store.ServiceRuntimeV3,
		},
	}
	service := NewRuntimeRelationshipService(Options{
		Store: state, DataDir: t.TempDir(),
	}, &relationshippublication.Cache{}, &relationshippublication.CacheV3{})
	runtime, err := service.relationshipRuntime(t.Context(), repository.Name)
	if err != nil || runtime.selector != nil {
		t.Fatalf("initial implicit v2 runtime = %+v, %v", runtime, err)
	}
	resolved, authorization, err := service.authorizeRepositories(
		t.Context(), []string{repository.Name},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := &relationshipBinding{
		authorization: authorization, repositories: resolved,
		sources: []relationshipSource{{
			repository: repository, current: true,
			publication: relationshipPublicationFenceFake{name: repository.Name, calls: new([]string)},
		}},
	}
	if err := service.confirmBinding(t.Context(), binding, authorization); humaStatus(err) != 409 {
		t.Fatalf("implicit v2 selector activation = %v", err)
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
