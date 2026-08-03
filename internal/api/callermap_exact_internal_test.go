package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/store"
)

const exactAuthorityTestLineage = "provisional_repo_path_v1_" +
	"abababababababababababababababababababababababababababababababab"

func exactCallerCensusPublication() *store.CallerGenerationPublication {
	pair := store.CallerGenerationPairPublication{
		Pair: store.CallerLeafPair{
			Domain: "grpc-caller", ExtractorVersion: "1.5.0",
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
			LeafOrdinal:        0, LeafPrefix: "00", LeafPrefixBits: 2,
			CandidateMemberName:    "candidate-caller-000000.ndjson",
			CandidateRecordCount:   5,
			CandidateDeclaredBytes: 500,
			CandidateContentBytes:  400,
			CandidateContentDigest: exactAuthorityDigest("a"),
			PairDigest:             exactAuthorityDigest("b"),
		},
		Receipt: store.CallerLeafArtifactReceipt{ExcludedGoTestRecords: 2},
	}
	copy := pair
	copy.Pair.Domain = "thrift-caller"
	copy.Pair.ExtractorVersion = "1.3.0"
	copy.Pair.PairDigest = exactAuthorityDigest("c")
	return &store.CallerGenerationPublication{
		Pairs: []store.CallerGenerationPairPublication{pair, copy},
	}
}

func TestExactCallerRecordCountsDeduplicateDomainCopies(t *testing.T) {
	publication := exactCallerCensusPublication()
	candidateRecords, excludedGoTestRecords, err :=
		exactCallerRecordCounts(publication)
	if err != nil {
		t.Fatal(err)
	}
	if candidateRecords != 5 || excludedGoTestRecords != 2 {
		t.Fatalf(
			"caller record census = candidate %d, excluded %d; want 5, 2",
			candidateRecords, excludedGoTestRecords,
		)
	}
}

func TestExactCallerRecordCountsRejectDisagreeingDomainCopies(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*store.CallerGenerationPairPublication)
	}{
		{
			name: "candidate count",
			mutate: func(pair *store.CallerGenerationPairPublication) {
				pair.Pair.CandidateRecordCount++
			},
		},
		{
			name: "excluded test count",
			mutate: func(pair *store.CallerGenerationPairPublication) {
				pair.Receipt.ExcludedGoTestRecords++
			},
		},
		{
			name: "leaf envelope",
			mutate: func(pair *store.CallerGenerationPairPublication) {
				pair.Pair.CandidateContentBytes++
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			publication := exactCallerCensusPublication()
			test.mutate(&publication.Pairs[1])
			if _, _, err := exactCallerRecordCounts(publication); err == nil {
				t.Fatal("disagreeing domain copies were accepted")
			}
		})
	}
}

type exactAuthorityGapStore struct {
	store.Store
	store.EvidenceStore
	repositories     map[string]store.Repo
	publications     map[string]*store.CallerGenerationPublication
	visible          map[string]bool
	repositoryReads  map[string]int
	publicationReads int
	summaryReads     int
	visibilityChecks int
	repositoryHook   func(string, int, *store.Repo)
}

func (state *exactAuthorityGapStore) LatestExtractionDomainOutcome(
	context.Context,
	store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	return nil, store.ErrNotFound
}

func (state *exactAuthorityGapStore) GetRepo(
	_ context.Context,
	repository string,
) (*store.Repo, error) {
	value, found := state.repositories[repository]
	if !found {
		return nil, store.ErrNotFound
	}
	state.repositoryReads[repository]++
	if state.repositoryHook != nil {
		state.repositoryHook(
			repository, state.repositoryReads[repository], &value,
		)
	}
	return &value, nil
}

func (*exactAuthorityGapStore) GetCandidateManifestPublication(
	context.Context,
	string,
) (*store.CandidateManifestPublication, error) {
	return nil, store.ErrNotFound
}

func (*exactAuthorityGapStore) GetResolverCatalogPublication(
	context.Context,
	string,
) (*store.ResolverCatalogPublication, error) {
	return nil, store.ErrNotFound
}

func (*exactAuthorityGapStore) ResolverCatalogPublicationCurrent(
	context.Context,
	store.ResolverCatalogPublication,
) (bool, error) {
	return false, nil
}

func (state *exactAuthorityGapStore) GetCallerGenerationPublication(
	_ context.Context,
	repository string,
) (*store.CallerGenerationPublication, error) {
	state.publicationReads++
	if publication := state.publications[repository]; publication != nil {
		owned := *publication
		owned.Pairs = append(
			[]store.CallerGenerationPairPublication(nil), publication.Pairs...,
		)
		return &owned, nil
	}
	return nil, store.ErrNotFound
}

func (state *exactAuthorityGapStore) GetCallerGenerationPublicationSummary(
	_ context.Context,
	repository string,
) (*store.CallerGenerationPublicationSummary, error) {
	state.summaryReads++
	publication := state.publications[repository]
	if publication == nil {
		return nil, store.ErrNotFound
	}
	summary := exactAuthorityPublicationSummary(*publication)
	return &summary, nil
}

func (*exactAuthorityGapStore) CallerGenerationPublicationSummaryCurrent(
	context.Context,
	store.CallerGenerationPublicationSummary,
) (bool, error) {
	return false, nil
}

func (*exactAuthorityGapStore) CallerGenerationPublicationSummaryAuthorityCurrent(
	context.Context,
	store.CallerGenerationPublicationSummary,
) (bool, error) {
	return false, nil
}

func (*exactAuthorityGapStore) CallerGenerationPublicationSummariesAuthorityCurrent(
	context.Context,
	[]store.CallerGenerationPublicationSummary,
) (bool, error) {
	return false, nil
}

func (*exactAuthorityGapStore) GetCallerGenerationAdmission(
	context.Context,
	store.CallerGenerationIdentity,
) (*store.CallerGenerationAdmission, error) {
	return nil, store.ErrNotFound
}

func (*exactAuthorityGapStore) GetCallerLeafOutcomeProgress(
	context.Context,
	store.CallerGenerationIdentity,
) (store.CallerLeafOutcomeProgress, error) {
	return store.CallerLeafOutcomeProgress{}, nil
}

func newExactAuthorityGapServices(
	t *testing.T,
	repositories ...string,
) (*exactAuthorityGapStore, *CallerMapService, *CallerComparisonService) {
	t.Helper()
	state := &exactAuthorityGapStore{
		repositories:    make(map[string]store.Repo, len(repositories)),
		publications:    make(map[string]*store.CallerGenerationPublication),
		visible:         make(map[string]bool, len(repositories)),
		repositoryReads: make(map[string]int, len(repositories)),
	}
	for _, repository := range repositories {
		state.repositories[repository] = store.Repo{
			Name: repository, CallerPublicationRevision: 7,
		}
		state.visible[repository] = true
	}
	adapters, err := callerexecute.NewRegistry(
		[]extract.Extractor{gocaller.NewGRPC()},
	)
	if err != nil {
		t.Fatal(err)
	}
	publications := callerpublication.NewRegistry(t.TempDir())
	t.Cleanup(func() { _ = publications.Close() })
	reader, err := callerexecute.NewPublicationReader(
		state, adapters, publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Store: state, Evidence: state, DataDir: t.TempDir(),
		CallerMapEnabled: true, CallerReader: reader,
		Principal: func(context.Context) string { return "user:authority" },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool {
				state.visibilityChecks++
				return state.visible[repository.Name]
			}
		},
		AuthorizationProvider: "authority-test-v1",
	}
	callerMap := NewCallerMapService(opts)
	if callerMap == nil {
		t.Fatal("exact caller map service is unavailable")
	}
	opts.CallerMap = callerMap
	comparison := NewCallerComparisonService(opts)
	if comparison == nil {
		t.Fatal("exact caller comparison service is unavailable")
	}
	return state, callerMap, comparison
}

func exactAuthorityPublicationSummary(
	publication store.CallerGenerationPublication,
) store.CallerGenerationPublicationSummary {
	return store.CallerGenerationPublicationSummary{
		Generation:             publication.Generation,
		PairPayloadDigest:      publication.PairPayloadDigest,
		PairSetDigest:          publication.PairSetDigest,
		PairCount:              publication.PairCount,
		ArtifactCount:          publication.ArtifactCount,
		ResultCount:            publication.ResultCount,
		AbstentionCount:        publication.AbstentionCount,
		CanonicalBytes:         publication.CanonicalBytes,
		StagingBytes:           publication.StagingBytes,
		PeakOpenFiles:          publication.PeakOpenFiles,
		ManifestDigest:         publication.ManifestDigest,
		ManifestPath:           publication.ManifestPath,
		PublicationRevision:    publication.PublicationRevision,
		PublicationIncarnation: publication.PublicationIncarnation,
		WriterSchema:           publication.WriterSchema,
		PublishedAt:            publication.PublishedAt,
	}
}

func exactAuthorityDigest(marker string) string {
	return "sha256:" + strings.Repeat(marker, 64)
}

func exactAuthorityStalePublication(
	repository string,
	generationMarker string,
	incarnationMarker string,
) *store.CallerGenerationPublication {
	return &store.CallerGenerationPublication{
		Generation: store.CallerGenerationIdentity{
			Repository: repository,
			Digest:     exactAuthorityDigest(generationMarker),
		},
		PairPayloadDigest:      exactAuthorityDigest("d"),
		PairSetDigest:          exactAuthorityDigest(generationMarker),
		ManifestDigest:         exactAuthorityDigest(generationMarker),
		PublicationRevision:    7,
		PublicationIncarnation: exactAuthorityDigest(incarnationMarker),
		PublishedAt:            time.Unix(1_700_000_000, 0).UTC(),
	}
}

func exactAuthorityMaxFixture(
	t *testing.T,
	repository string,
	marker string,
) (callerexecute.PublicationBinding, callerexecute.PublicationDescriptor, CallerMapGeneration) {
	t.Helper()
	extractors := make([]callerleaf.ExtractorIdentity, callerleaf.MaxCallerDomains)
	for index := range extractors {
		prefix := fmt.Sprintf("%03d-", index)
		extractors[index] = callerleaf.ExtractorIdentity{
			Domain:             prefix + strings.Repeat("d", 128-len(prefix)),
			Version:            strings.Repeat("v", 64),
			LeafAdapterVersion: callerleaf.LeafAdapterV1,
		}
	}
	leafGeneration, err := callerleaf.NewGenerationIdentity(
		callerleaf.GenerationIdentity{
			Repository:               repository,
			HeadCommit:               strings.Repeat(marker, 40),
			DeclarationSetDigest:     exactAuthorityDigest(marker),
			CandidateManifestDigest:  exactAuthorityDigest(marker),
			CandidatePolicyDigest:    exactAuthorityDigest(marker),
			ResolverGenerationDigest: exactAuthorityDigest(marker),
			ResolverManifestDigest:   exactAuthorityDigest(marker),
			Extractors:               extractors,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication := exactAuthorityStalePublication(
		repository, marker, marker,
	)
	publication.Generation = store.CallerGenerationIdentity{
		Repository:               repository,
		HeadCommit:               leafGeneration.HeadCommit,
		DeclarationSetDigest:     leafGeneration.DeclarationSetDigest,
		CandidateManifestDigest:  leafGeneration.CandidateManifestDigest,
		CandidatePolicyDigest:    leafGeneration.CandidatePolicyDigest,
		ResolverGenerationDigest: leafGeneration.ResolverGenerationDigest,
		ResolverManifestDigest:   leafGeneration.ResolverManifestDigest,
		SourceLanePolicy:         leafGeneration.SourceLanePolicy,
		CallerPolicyDigest:       leafGeneration.CallerPolicyDigest,
		ExtractorSetDigest:       leafGeneration.ExtractorSetDigest,
		Digest:                   leafGeneration.Digest,
	}
	publication.PairSetDigest = exactAuthorityDigest(marker)
	publication.ManifestDigest = exactAuthorityDigest(marker)
	publicationState := (callerpublication.Manifest{
		Generation:    leafGeneration,
		PairSetDigest: publication.PairSetDigest,
		Aggregate: callerleaf.AggregateReceipt{
			PeakOpenFiles: callerleaf.MaxOpenFiles,
		},
		Digest: publication.ManifestDigest,
	}).State()
	if err := callerpublication.ValidateState(publicationState); err != nil {
		t.Fatalf("maximum publication state: %v", err)
	}
	summary := exactAuthorityPublicationSummary(*publication)
	read := &callerexecute.PublicationRead{
		Availability: callerexecute.PublicationStale,
		Summary:      &summary,
		Revision:     summary.PublicationRevision,
	}
	descriptor, err := read.Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	binding := callerexecute.PublicationBinding{
		Summary: summary,
		State:   publicationState,
	}
	generation := CallerMapGeneration{
		State: string(callerexecute.PublicationCurrent),
		Plane: exactCallerMapPlane, Repository: repository,
		Commit:               leafGeneration.HeadCommit,
		GenerationDigest:     leafGeneration.Digest,
		DeclarationSetDigest: leafGeneration.DeclarationSetDigest,
		CandidateManifest:    leafGeneration.CandidateManifestDigest,
		ResolverManifest:     leafGeneration.ResolverManifestDigest,
		ManifestDigest:       publication.ManifestDigest,
		PairSetDigest:        publication.PairSetDigest,
		PublicationRevision:  publication.PublicationRevision,
	}
	return binding, descriptor, generation
}

func exactAuthorityTestEndpoint(repository string) CallerMapEndpoint {
	return CallerMapEndpoint{
		Protocol: "protobuf", Repository: repository,
		Lineage:   exactAuthorityTestLineage,
		Operation: "/acme.orders.v1.Orders/Get",
	}
}

func requireExactAuthorityStatus(t *testing.T, err error, want int) {
	t.Helper()
	var statusError interface{ GetStatus() int }
	if !errors.As(err, &statusError) || statusError.GetStatus() != want {
		t.Fatalf("status error = %v, want %d", err, want)
	}
}

func TestExactCallerGapAuthorityConfirmsWithoutTransportExposure(t *testing.T) {
	const repository = "github.com/acme/authority-gap"
	state, service, _ := newExactAuthorityGapServices(t, repository)
	query := CallerMapQuery{Endpoint: exactAuthorityTestEndpoint(repository)}
	page, err := service.List(t.Context(), query, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.MatchingRowsState != "unavailable" || page.exactSnapshot == "" ||
		page.exactAuthority == "" || len(page.exactAuthority) > callerMapCursorLimit {
		t.Fatalf("gap authority page = %+v", page)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), page.exactSnapshot) ||
		strings.Contains(string(encoded), page.exactAuthority) {
		t.Fatalf("transport exposed hidden exact authority: %s", encoded)
	}
	confirmation, err := service.confirmWorkbenchCallerSnapshot(
		t.Context(), query, 10, page.exactAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.Snapshot != page.exactSnapshot ||
		confirmation.MatchingRowsState != "unavailable" ||
		!reflect.DeepEqual(confirmation.Generation, *page.Generation) {
		t.Fatalf("gap confirmation = %+v, page = %+v", confirmation, page)
	}
	_, err = service.confirmWorkbenchCallerSnapshot(
		t.Context(), query, 10, page.exactAuthority+"tampered",
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)

	_, err = service.confirmWorkbenchCallerSnapshot(
		t.Context(), query, 11, page.exactAuthority,
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
	state.visible[repository] = false
	state.publicationReads = 0
	_, err = service.confirmWorkbenchCallerSnapshot(
		t.Context(), query, 10, page.exactAuthority,
	)
	requireExactAuthorityStatus(t, err, http.StatusNotFound)
	if state.publicationReads != 0 {
		t.Fatalf("hidden authority reached publication I/O %d times", state.publicationReads)
	}
}

func TestExactCallerGapAuthorityRejectsFinalRevisionTransition(t *testing.T) {
	const repository = "github.com/acme/authority-transition"
	state, service, _ := newExactAuthorityGapServices(t, repository)
	query := CallerMapQuery{Endpoint: exactAuthorityTestEndpoint(repository)}
	page, err := service.List(t.Context(), query, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	baseline := state.repositoryReads[repository]
	state.repositoryHook = func(name string, read int, value *store.Repo) {
		if name == repository && read == baseline+4 {
			value.CallerPublicationRevision++
		}
	}
	_, err = service.confirmWorkbenchCallerSnapshot(
		t.Context(), query, 10, page.exactAuthority,
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
}

func TestExactCallerGapAuthorityRejectsABAIncarnation(t *testing.T) {
	const repository = "github.com/acme/authority-aba"
	state, service, _ := newExactAuthorityGapServices(t, repository)
	state.publications[repository] = exactAuthorityStalePublication(
		repository, "a", "1",
	)
	query := CallerMapQuery{Endpoint: exactAuthorityTestEndpoint(repository)}
	page, err := service.List(t.Context(), query, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Generation == nil ||
		page.Generation.State != string(callerexecute.PublicationStale) {
		t.Fatalf("initial stale generation = %+v", page.Generation)
	}
	state.publications[repository] = exactAuthorityStalePublication(
		repository, "b", "2",
	)
	state.publications[repository] = exactAuthorityStalePublication(
		repository, "a", "3",
	)
	_, err = service.confirmWorkbenchCallerSnapshot(
		t.Context(), query, 10, page.exactAuthority,
	)
	requireExactAuthorityStatus(t, err, http.StatusConflict)
}

func TestExactCallerAuthorityFitsMaximumPublicationIdentity(t *testing.T) {
	repository := "r" + strings.Repeat("a", 1023)
	binding, publication, generation := exactAuthorityMaxFixture(
		t, repository, "a",
	)
	totalPairs := generation.PairCount
	generation.PartitionProgress = &CallerMapPartitionProgress{
		State: "complete", SettledPairCount: totalPairs,
		SucceededPairCount: totalPairs, TotalPairCount: &totalPairs,
	}
	generation.RecordCounts = &CallerMapRecordCounts{}
	scope := AnalysisScopeProjection{
		Repository: repository, Commit: generation.Commit,
		ScopePosture: "whole-repository",
	}
	snapshot := exactCallerPageSnapshot(&exactCallerBinding{
		visibility: VisibilityContext{
			Principal: "user:max", AuthorizationProvider: "max-v1",
			PermissionSnapshot:         exactAuthorityDigest("b"),
			VisibleRepositorySetDigest: exactAuthorityDigest("c"),
		},
		publication: binding, generation: generation, scope: scope,
	})
	service := &exactCallerMapService{}
	authority := exactCallerAuthority{
		Schema: exactCallerAuthorityVersion, QueryDigest: exactAuthorityDigest("d"),
		Repository: repository,
		Visibility: VisibilityContext{
			Principal: "user:max", AuthorizationProvider: "max-v1",
			PermissionSnapshot:         exactAuthorityDigest("b"),
			VisibleRepositorySetDigest: exactAuthorityDigest("c"),
		},
		RepositoryRevision: generation.PublicationRevision,
		Snapshot:           snapshot, MatchingRowsState: "exact",
		Generation: generation, ScopeDigest: analysisScopeDigest(scope),
		Publication: &publication,
	}
	if !validExactCallerAuthority(authority) {
		t.Fatalf("maximum exact caller authority is invalid: %+v", authority)
	}
	encoded, err := service.encodeExactAuthority(authority)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > callerMapCursorLimit {
		t.Fatalf("maximum exact caller authority bytes = %d", len(encoded))
	}
}

func TestCallerAnalysisScopeProjectionClonesExactFocusedPaths(t *testing.T) {
	repository := "github.com/acme/monorepo"
	state, err := (analysisunit.Scope{
		Repository: repository, Name: "orders-api",
		Primary: []string{"services/orders"},
		Supporting: []string{
			"api/orders.proto", "gen/orders/orders_grpc.pb.go",
		},
	}).State()
	if err != nil {
		t.Fatal(err)
	}
	projection := callerAnalysisScope(store.Repo{
		Name: repository, IndexedCommitHash: strings.Repeat("a", 40),
		IndexedAnalysisUnit: state,
	})
	generation := CallerMapGeneration{
		Repository: repository, Commit: strings.Repeat("a", 40),
		UnitDigest: state.Digest,
	}
	if !validCallerAnalysisScope(projection, generation) ||
		projection.ScopePosture != analysisunit.SearchIndexFocused ||
		projection.AnalysisUnit == nil ||
		!reflect.DeepEqual(
			projection.AnalysisUnit.PrimaryPaths,
			[]string{"services/orders"},
		) || !reflect.DeepEqual(
		projection.AnalysisUnit.SupportingPaths,
		[]string{"api/orders.proto", "gen/orders/orders_grpc.pb.go"},
	) {
		t.Fatalf("focused caller analysis scope = %+v", projection)
	}
	state.PrimaryPaths[0] = "mutated"
	if projection.AnalysisUnit.PrimaryPaths[0] != "services/orders" ||
		!validAnalysisScopeDigest(analysisScopeDigest(projection)) {
		t.Fatalf("caller analysis scope aliased repository state: %+v", projection)
	}
}

func TestExactCallerNegativeIndexesReuseTheEightSlotCache(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure exactCallerIndexFailure
	}{
		{name: "semantic", failure: exactCallerIndexSemanticFailure},
		{name: "limit", failure: exactCallerIndexLimitFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := test.failure
			service := &exactCallerMapService{
				indexes:  make(map[string]*exactCallerIndex),
				bindings: make(map[string]*exactCallerBinding),
			}
			negative := &exactCallerIndex{key: "negative", failure: failure}
			if err := service.retainIndex(negative); err != nil {
				t.Fatal(err)
			}
			service.releaseIndex(negative)

			for attempt := 0; attempt < 2; attempt++ {
				cached := service.acquireIndex(negative.key)
				if cached != negative {
					t.Fatalf("attempt %d cache miss: %+v", attempt, cached)
				}
				result, err := service.indexResult(cached)
				if result != nil || err == nil {
					t.Fatalf("attempt %d negative result = %+v, %v", attempt, result, err)
				}
				if failure == exactCallerIndexSemanticFailure &&
					!errors.Is(err, errExactCallerProjectionInvalid) {
					t.Fatalf("semantic cache error = %v", err)
				}
				if failure == exactCallerIndexLimitFailure {
					var statusError interface{ GetStatus() int }
					if !errors.As(err, &statusError) ||
						statusError.GetStatus() != http.StatusUnprocessableEntity {
						t.Fatalf("limit cache error = %v, want 422", err)
					}
				}
			}
			if negative.uses != 0 || len(service.indexes) != 1 {
				t.Fatalf("negative cache state = %+v / %d", negative, len(service.indexes))
			}
		})
	}
}

func TestExactCallerIndexScanErrorPrefersPhysicalValidation(t *testing.T) {
	physical := errors.New("physical tail changed")
	semantic := errors.New("semantic projection invalid")
	limit := errors.New("identity limit")
	if got := exactCallerIndexScanError(physical, semantic, limit); got != physical {
		t.Fatalf("physical scan failure = %v, want %v", got, physical)
	}
	if got := exactCallerIndexScanError(nil, semantic, limit); got != semantic {
		t.Fatalf("semantic failure = %v, want %v", got, semantic)
	}
	if got := exactCallerIndexScanError(nil, nil, limit); got != limit {
		t.Fatalf("limit failure = %v, want %v", got, limit)
	}
}

func TestExactCallerIdentityBudgetExactBoundaryAndFixedCharge(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	record := exactCallerRecord{
		reference: callerpublication.RecordReference{
			PairDigest: digest,
			ArtifactName: "leaf-v1-" + strings.Repeat("b", 64) + "-" +
				strings.Repeat("c", 64) + ".ndjson",
			Record: callerleaf.RecordReference{Digest: digest},
		},
		recordID: digest,
	}
	const fixedReferenceAndIDBytes = 357
	if got := exactCallerRecordIdentityBytes(record); got != fixedReferenceAndIDBytes {
		t.Fatalf("fixed identity charge = %d, want %d", got, fixedReferenceAndIDBytes)
	}
	if !exactCallerIdentityAdmits(
		exactCallerMapMaxIdentityBytes-fixedReferenceAndIDBytes,
		fixedReferenceAndIDBytes,
	) {
		t.Fatal("exact identity cap was refused")
	}
	if exactCallerIdentityAdmits(
		exactCallerMapMaxIdentityBytes-fixedReferenceAndIDBytes,
		fixedReferenceAndIDBytes+1,
	) {
		t.Fatal("identity cap plus one was admitted")
	}
}

func TestBoundedUnitAttributionCopiesOnlySerializedCandidates(t *testing.T) {
	candidates := make([]CallerMapUnitCandidate, 25_000)
	bounded := boundedUnitAttribution("ambiguous", "many", candidates)
	if len(bounded.Candidates) != callerMapUnitCandidateLimit ||
		cap(bounded.Candidates) != callerMapUnitCandidateLimit ||
		bounded.CandidateTotal != len(candidates) {
		t.Fatalf(
			"bounded candidates = len:%d cap:%d total:%d",
			len(bounded.Candidates), cap(bounded.Candidates), bounded.CandidateTotal,
		)
	}
}

func TestExactCallerCitationEnvelopeIsIndependentOfGenerationFieldBounds(t *testing.T) {
	service := &exactCallerMapService{}
	binding := &exactCallerBinding{
		id: strings.Repeat("b", 24),
		// These maximum-shaped retained fields deliberately do not enter the
		// signed citation. They are recovered from the process-local binding.
		generation: CallerMapGeneration{
			Repository: strings.Repeat("r", 1024),
			Commit:     strings.Repeat("c", 64),
		},
		visibility: VisibilityContext{
			Principal:             strings.Repeat("p", 4096),
			AuthorizationProvider: strings.Repeat("v", 4096),
		},
	}
	record := exactCallerRecord{
		recordID:  "sha256:" + strings.Repeat("f", 64),
		path:      strings.Repeat("x", 4096),
		operation: "/" + strings.Repeat("o", 1023),
		lineage:   strings.Repeat("l", 1024),
	}
	encoded, err := service.encodeCitation(
		binding, exactCallerMapMaxRecords-1, record,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 4096 || len(encoded) > callerMapCursorLimit {
		t.Fatalf("compact citation bytes = %d", len(encoded))
	}
}

func TestExactCallerCitationReadsRefuseBeyondProcessLimit(t *testing.T) {
	service := &exactCallerMapService{
		citationRead: make(chan struct{}, exactCallerCitationReadLimit),
	}
	releases := make([]func(), 0, exactCallerCitationReadLimit)
	for range exactCallerCitationReadLimit {
		release, err := service.beginCitationRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := service.beginCitationRead(t.Context()); err == nil {
		t.Fatal("citation read above the process limit was admitted")
	} else {
		var statusError interface{ GetStatus() int }
		if !errors.As(err, &statusError) ||
			statusError.GetStatus() != http.StatusServiceUnavailable {
			t.Fatalf("citation capacity error = %v", err)
		}
	}
	for _, release := range releases {
		release()
	}
	if len(service.citationRead) != 0 {
		t.Fatalf("citation read slots after release = %d", len(service.citationRead))
	}
}

func TestExactCallerRequestsRefuseBeyondProcessLimit(t *testing.T) {
	service := &exactCallerMapService{
		exactRead: make(chan struct{}, exactCallerReadLimit),
	}
	releases := make([]func(), 0, exactCallerReadLimit)
	for range exactCallerReadLimit {
		release, err := service.beginExactRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := service.beginExactRead(t.Context()); err == nil {
		t.Fatal("exact caller request above the process limit was admitted")
	} else {
		var statusError interface{ GetStatus() int }
		if !errors.As(err, &statusError) ||
			statusError.GetStatus() != http.StatusServiceUnavailable {
			t.Fatalf("exact caller capacity error = %v", err)
		}
	}
	for _, release := range releases {
		release()
	}
	if len(service.exactRead) != 0 {
		t.Fatalf("exact caller slots after release = %d", len(service.exactRead))
	}
}

func TestExactCallerCitationRepositoryPreservesOperationalFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "repos"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exactCallerCitationRepositoryDir(
		root, "github.com/acme/orders",
	); err == nil {
		t.Fatal("operational repository path failure was hidden")
	} else {
		var statusError interface{ GetStatus() int }
		if !errors.As(err, &statusError) ||
			statusError.GetStatus() != http.StatusInternalServerError {
			t.Fatalf("operational repository path error = %v", err)
		}
	}
	if _, err := exactCallerCitationRepositoryDir(root, "../escape"); err == nil {
		t.Fatal("invalid repository path was accepted")
	} else {
		var statusError interface{ GetStatus() int }
		if !errors.As(err, &statusError) ||
			statusError.GetStatus() != http.StatusNotFound {
			t.Fatalf("invalid repository path error = %v", err)
		}
	}
}

func TestExactCallerBindingPinsItsIndexWithoutPerPageCopy(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	pinned := &exactCallerIndex{key: "pinned"}
	if err := service.retainIndex(pinned); err != nil {
		t.Fatal(err)
	}
	service.releaseIndex(pinned)
	binding := &exactCallerBinding{
		createdAt: time.Now(), indexKey: pinned.key,
		records: append(make([]int, 0, 1_000), 1, 3, 5),
	}
	if err := service.retainBinding(binding); err != nil {
		t.Fatal(err)
	}
	if cap(binding.records) != len(binding.records) {
		t.Fatalf("retained binding capacity = %d, want %d", cap(binding.records), len(binding.records))
	}
	retained := service.acquireBinding(binding.id)
	if pinned.bindings != 1 || retained != binding {
		t.Fatalf("retained binding/index = %+v / %+v", binding, pinned)
	}
	service.releaseBinding(retained)

	// Fill the remaining slots and force one more admission. The active
	// cursor's index remains; an unbound index is the only legal victim.
	for index := 1; index <= exactCallerMapMaxIndexes; index++ {
		candidate := &exactCallerIndex{key: "index-" + string(rune('a'+index))}
		if err := service.retainIndex(candidate); err != nil {
			t.Fatal(err)
		}
		service.releaseIndex(candidate)
	}
	if service.indexes[pinned.key] != pinned {
		t.Fatal("active cursor index was evicted")
	}
	if len(service.indexes) != exactCallerMapMaxIndexes {
		t.Fatalf("retained indexes = %d", len(service.indexes))
	}
	service.dropBinding(binding.id)
	if pinned.bindings != 0 || service.bindingRefs != 0 || service.bindingCount != 0 {
		t.Fatalf(
			"released binding/index = %+v / refs=%d count=%d",
			pinned, service.bindingRefs, service.bindingCount,
		)
	}
}

func TestExactCallerIndexPressureRetiresOnlyIdleBindingPins(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	indexes := make([]*exactCallerIndex, 0, exactCallerMapMaxIndexes)
	bindings := make([]*exactCallerBinding, 0, exactCallerMapMaxIndexes)
	for position := range exactCallerMapMaxIndexes {
		index := &exactCallerIndex{key: "index-" + string(rune('a'+position))}
		if err := service.retainIndex(index); err != nil {
			t.Fatal(err)
		}
		service.releaseIndex(index)
		binding := &exactCallerBinding{
			createdAt: time.Now().Add(time.Duration(position) * time.Second),
			indexKey:  index.key,
			records:   []int{position},
		}
		var err error
		if position == 0 {
			err = service.retainActiveBinding(binding)
		} else {
			err = service.retainBinding(binding)
		}
		if err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, index)
		bindings = append(bindings, binding)
	}
	replacement := &exactCallerIndex{key: "replacement"}
	if err := service.retainIndex(replacement); err != nil {
		t.Fatalf("retain replacement index: %v", err)
	}
	service.releaseIndex(replacement)
	if service.indexes[indexes[0].key] != indexes[0] ||
		service.bindings[bindings[0].id] != bindings[0] {
		t.Fatal("active index binding was pressure-retired")
	}
	if service.indexes[indexes[1].key] != nil ||
		service.bindings[bindings[1].id] != nil ||
		service.indexes[replacement.key] != replacement ||
		len(service.indexes) != exactCallerMapMaxIndexes {
		t.Fatalf(
			"index pressure = indexes:%d bindings:%d old:%t new:%t",
			len(service.indexes), len(service.bindings),
			service.indexes[indexes[1].key] != nil,
			service.indexes[replacement.key] == replacement,
		)
	}
	service.releaseBinding(bindings[0])
}

func TestExactCallerBindingPressureRetiresOldestIdle(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	index := &exactCallerIndex{key: "shared"}
	if err := service.retainIndex(index); err != nil {
		t.Fatal(err)
	}
	service.releaseIndex(index)
	createdAt := time.Now().Add(-time.Minute)
	bindings := make([]*exactCallerBinding, 0, exactCallerMapMaxBindings)
	for position := range exactCallerMapMaxBindings {
		binding := &exactCallerBinding{
			createdAt: createdAt.Add(time.Duration(position) * time.Second),
			indexKey:  index.key,
			records:   []int{position},
		}
		if err := service.retainBinding(binding); err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, binding)
	}
	replacement := &exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key, records: []int{99},
	}
	if err := service.retainBinding(replacement); err != nil {
		t.Fatalf("retain replacement: %v", err)
	}
	if service.acquireBinding(bindings[0].id) != nil {
		t.Fatal("oldest idle binding survived pressure retirement")
	}
	if service.bindings[replacement.id] != replacement ||
		service.bindingCount != exactCallerMapMaxBindings ||
		service.bindingRefs != exactCallerMapMaxBindings ||
		index.bindings != exactCallerMapMaxBindings {
		t.Fatalf(
			"pressure admission = visible:%d count:%d refs:%d pins:%d",
			len(service.bindings), service.bindingCount, service.bindingRefs,
			index.bindings,
		)
	}
}

func TestExactCallerBindingPressureReclaimsCountAndReferences(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	index := &exactCallerIndex{key: "shared"}
	if err := service.retainIndex(index); err != nil {
		t.Fatal(err)
	}
	service.releaseIndex(index)
	createdAt := time.Now().Add(-time.Minute)
	for position := range 2 {
		binding := &exactCallerBinding{
			createdAt: createdAt.Add(time.Duration(position) * time.Second),
			indexKey:  index.key,
			records:   make([]int, exactCallerMapBindingRefs/2),
		}
		if err := service.retainBinding(binding); err != nil {
			t.Fatal(err)
		}
	}
	replacement := &exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key,
		records: make([]int, exactCallerMapBindingRefs*3/4),
	}
	if err := service.retainBinding(replacement); err != nil {
		t.Fatalf("retain replacement: %v", err)
	}
	if len(service.bindings) != 1 ||
		service.bindings[replacement.id] != replacement ||
		service.bindingCount != 1 ||
		service.bindingRefs != len(replacement.records) || index.bindings != 1 {
		t.Fatalf(
			"reference-pressure admission = visible:%d count:%d refs:%d pins:%d",
			len(service.bindings), service.bindingCount, service.bindingRefs,
			index.bindings,
		)
	}
}

func TestExactCallerBindingPressurePreservesActiveAndPreflightsFailure(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	index := &exactCallerIndex{key: "shared"}
	if err := service.retainIndex(index); err != nil {
		t.Fatal(err)
	}
	service.releaseIndex(index)
	active := &exactCallerBinding{
		createdAt: time.Now().Add(-time.Minute), indexKey: index.key,
		records: make([]int, exactCallerMapBindingRefs-1),
	}
	if err := service.retainActiveBinding(active); err != nil {
		t.Fatal(err)
	}
	service.dropBinding(active.id)
	idle := &exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key, records: []int{1},
	}
	if err := service.retainBinding(idle); err != nil {
		t.Fatal(err)
	}
	replacement := &exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key, records: []int{2, 3},
	}
	if err := service.retainBinding(replacement); err == nil {
		t.Fatal("unreclaimable in-flight reference pressure was admitted")
	}
	if service.bindings[idle.id] != idle || service.bindingCount != 2 ||
		service.bindingRefs != exactCallerMapBindingRefs || index.bindings != 2 {
		t.Fatalf(
			"failed admission mutated live tokens = visible:%d count:%d refs:%d pins:%d",
			len(service.bindings), service.bindingCount, service.bindingRefs,
			index.bindings,
		)
	}
	service.releaseBinding(active)
	if err := service.retainBinding(replacement); err != nil {
		t.Fatalf("retain after active release: %v", err)
	}
}

func TestExactCallerBindingPressureRefusesWhenEveryBindingIsActive(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	index := &exactCallerIndex{key: "shared"}
	if err := service.retainIndex(index); err != nil {
		t.Fatal(err)
	}
	service.releaseIndex(index)
	active := make([]*exactCallerBinding, 0, exactCallerMapMaxBindings)
	for position := range exactCallerMapMaxBindings {
		binding := &exactCallerBinding{
			createdAt: time.Now(), indexKey: index.key, records: []int{position},
		}
		if err := service.retainActiveBinding(binding); err != nil {
			t.Fatal(err)
		}
		active = append(active, binding)
	}
	replacement := &exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key, records: []int{99},
	}
	if err := service.retainBinding(replacement); err == nil {
		t.Fatal("active binding capacity was evicted")
	}
	if len(service.bindings) != exactCallerMapMaxBindings ||
		service.bindingCount != exactCallerMapMaxBindings ||
		index.bindings != exactCallerMapMaxBindings {
		t.Fatalf(
			"active refusal = visible:%d count:%d pins:%d",
			len(service.bindings), service.bindingCount, index.bindings,
		)
	}
	for _, binding := range active {
		service.releaseBinding(binding)
	}
	if err := service.retainBinding(replacement); err != nil {
		t.Fatalf("retain after active requests completed: %v", err)
	}
}

func TestExactCallerExpiredInFlightBindingRemainsFullyAccounted(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	index := &exactCallerIndex{key: "pinned"}
	if err := service.retainIndex(index); err != nil {
		t.Fatal(err)
	}
	service.releaseIndex(index)
	binding := &exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key,
		records: make([]int, exactCallerMapBindingRefs),
	}
	if err := service.retainBinding(binding); err != nil {
		t.Fatal(err)
	}
	active := service.acquireBinding(binding.id)
	if active != binding {
		t.Fatal("failed to acquire retained binding")
	}
	service.mu.Lock()
	binding.createdAt = time.Now().Add(-exactCallerMapBindingLifetime - time.Second)
	service.mu.Unlock()
	if err := service.retainBinding(&exactCallerBinding{
		createdAt: time.Now(), indexKey: index.key, records: []int{1},
	}); err == nil {
		t.Fatal("expired in-flight binding released its live position budget")
	}
	if service.bindingRefs != exactCallerMapBindingRefs ||
		service.bindingCount != 1 || index.bindings != 1 ||
		len(service.bindings) != 0 {
		t.Fatalf(
			"in-flight expiry = refs:%d count:%d pins:%d visible:%d",
			service.bindingRefs, service.bindingCount, index.bindings,
			len(service.bindings),
		)
	}
	service.releaseBinding(active)
	if service.bindingRefs != 0 || service.bindingCount != 0 ||
		index.bindings != 0 {
		t.Fatalf(
			"final in-flight release = refs:%d count:%d pins:%d",
			service.bindingRefs, service.bindingCount, index.bindings,
		)
	}
}

func TestExactCallerExpiredBindingsReleaseIndexAdmission(t *testing.T) {
	service := &exactCallerMapService{
		indexes:  make(map[string]*exactCallerIndex),
		bindings: make(map[string]*exactCallerBinding),
	}
	for position := range exactCallerMapMaxIndexes {
		index := &exactCallerIndex{key: "pinned-" + string(rune('a'+position))}
		if err := service.retainIndex(index); err != nil {
			t.Fatal(err)
		}
		service.releaseIndex(index)
		binding := &exactCallerBinding{
			createdAt: time.Now(), indexKey: index.key, records: []int{position},
		}
		if err := service.retainBinding(binding); err != nil {
			t.Fatal(err)
		}
	}
	service.mu.Lock()
	for _, binding := range service.bindings {
		binding.createdAt = time.Now().Add(-exactCallerMapBindingLifetime - time.Second)
	}
	service.mu.Unlock()

	ninth := &exactCallerIndex{key: "replacement"}
	if err := service.retainIndex(ninth); err != nil {
		t.Fatalf("retain index after binding expiry: %v", err)
	}
	service.releaseIndex(ninth)
	if service.indexes[ninth.key] != ninth ||
		len(service.indexes) != exactCallerMapMaxIndexes ||
		len(service.bindings) != 0 || service.bindingRefs != 0 ||
		service.bindingCount != 0 {
		t.Fatalf(
			"expired admission = indexes:%d bindings:%d refs:%d replacement:%t",
			len(service.indexes), len(service.bindings), service.bindingRefs,
			service.indexes[ninth.key] == ninth,
		)
	}
}

func TestExactCallerGapOmitsUnknownTotalsAndLegacyDigests(t *testing.T) {
	page := CallerMapPage{
		SchemaVersion: exactCallerMapSchemaVersion,
		Rows:          []CallerMapRow{}, Pagination: CallerMapPagination{Complete: true},
		Generation: &CallerMapGeneration{
			State: "missing", Plane: exactCallerMapPlane,
			Repository: "github.com/acme/orders",
		},
		MatchingRowsState: "unavailable",
		Caveat:            "caller totals unavailable",
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"declaration"`, `"total_matching_rows"`, `"coverage_digest"`,
		`"attribution_digest"`, `"coverage"`,
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("gap serialized %s: %s", forbidden, raw)
		}
	}

	zero := 0
	page.TotalMatchingRows = &zero
	page.MatchingRowsState = "exact"
	raw, err = json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"total_matching_rows":0`) {
		t.Fatalf("exact zero total was omitted: %s", raw)
	}
}
