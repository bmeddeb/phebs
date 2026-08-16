package callerexecute

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/callerpublicationid"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

type publicationReconcileTestStore struct {
	*workerTestStore
}

func TestCallerPublicationStateReconstructsPartitionedUpstreamAuthority(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	current, err := currentAuthority(
		t.Context(), harness.state, harness.worker.registry, harness.state.repo.Name,
	)
	if err != nil || current == nil {
		t.Fatalf("base authority = %+v, %v", current, err)
	}
	digest := func(fill string) string { return "sha256:" + strings.Repeat(fill, 64) }
	observation := observationpublication.DownstreamAuthority{
		Version:                observationpublication.DownstreamAuthorityV2,
		Repository:             harness.state.repo.Name,
		SourceGenerationDigest: digest("1"), SourceRootDigest: digest("2"),
		ObservationGenerationDigest: digest("3"), ObservationRootDigest: digest("4"),
		PartitionPolicyDigest: digest("5"), ObservationPolicyDigest: digest("6"),
		InventoryPolicyDigest: digest("7"), RecordCount: 1, ObservedCount: 1,
	}
	extractor := current.semantic.Extractors[0]
	domain := candidate.DownstreamDomainAuthority{
		Domain: extractor.Domain, Version: extractor.Version,
		PlanDigest: digest("8"), RootDigest: digest("9"), RunID: "partitioned-run",
		Disposition:                  candidate.PartitionResultEmpty,
		CandidateManifestDigest:      current.semantic.CandidateManifestDigest,
		CandidatePartitionRootDigest: digest("a"), CandidatePolicyDigest: digest("b"),
		SourceGenerationDigest:      observation.SourceGenerationDigest,
		ObservationGenerationDigest: observation.ObservationGenerationDigest,
		ExtractionPolicyDigest:      digest("c"), DomainIndexDigest: digest("d"),
		DomainScheduleDigest: digest("e"),
	}
	upstream, err := downstreamauthority.Build(observation, []candidate.DownstreamDomainAuthority{domain})
	if err != nil {
		t.Fatal(err)
	}
	if err := downstreamauthority.RequireUsable(upstream); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	semantic := current.semantic
	semantic.Upstream, semantic.UpstreamDigest = raw, upstream.Digest
	semantic, err = callerleaf.NewGenerationIdentity(semantic)
	if err != nil {
		t.Fatal(err)
	}
	stored := storeGeneration(semantic, *current.candidate, *current.resolver)
	manifestDigest := digest("f")
	summary := callerPublicationSummary(*harness.state.publication)
	summary.Generation = stored
	summary.Upstream = string(raw)
	summary.ManifestDigest = manifestDigest
	summary.ManifestPath = callerpublicationid.ManifestName(stored.Digest, manifestDigest)
	state, err := callerPublicationStateFromStore(
		summary,
		harness.worker.registry.ExtractorIdentities(),
	)
	if err != nil || !reflect.DeepEqual(state.Generation, semantic) {
		t.Fatalf("partitioned state = %+v, want %+v: %v", state.Generation, semantic, err)
	}
}

func TestCallerPublicationStateRejectsHistoricalV2AdapterForV3Runtime(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	current, err := currentAuthority(
		t.Context(), harness.state, harness.worker.registry, harness.state.repo.Name,
	)
	if err != nil || current == nil {
		t.Fatalf("current authority = %+v, %v", current, err)
	}
	v2Extractors := harness.worker.registry.ExtractorIdentities()
	for index := range v2Extractors {
		v2Extractors[index].LeafAdapterVersion = callerleaf.LeafAdapterV2
	}
	v2Semantic := current.semantic
	v2Semantic.Extractors = slices.Clone(v2Extractors)
	v2Semantic, err = callerleaf.NewGenerationIdentity(v2Semantic)
	if err != nil {
		t.Fatal(err)
	}
	summary := callerPublicationSummary(*harness.state.publication)
	summary.Generation = storeGeneration(v2Semantic, *current.candidate, *current.resolver)
	summary.ManifestPath = callerpublicationid.ManifestName(
		summary.Generation.Digest, summary.ManifestDigest,
	)
	if _, err := callerPublicationStateFromStore(summary, v2Extractors); err != nil {
		t.Fatalf("historical V2 summary no longer reconstructs with V2 semantics: %v", err)
	}
	if _, err := callerPublicationStateFromStore(
		summary, harness.worker.registry.ExtractorIdentities(),
	); !errors.Is(err, store.ErrInvalidCallerGenerationPublication) {
		t.Fatalf("V3 runtime accepted historical V2 generation: %v", err)
	}
}

func (state *publicationReconcileTestStore) ListCallerPublicationRepositoriesPage(
	_ context.Context,
	after string,
	limit int,
) ([]string, error) {
	if limit < 1 || state.repo.Name <= after ||
		state.repo.IndexedCommitHash == "" || state.repo.Deleting {
		return nil, nil
	}
	return []string{state.repo.Name}, nil
}

func (state *publicationReconcileTestStore) CallerPublicationRepositoryEligible(
	_ context.Context,
	repository string,
) (bool, error) {
	return repository == state.repo.Name && state.repo.IndexedCommitHash != "" &&
		!state.repo.Deleting, nil
}

func (state *publicationReconcileTestStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	if kind != store.JobCallerLeaf || target != state.repo.Name || !force {
		return nil, errors.New("unexpected caller reconciliation enqueue")
	}
	state.events = append(state.events, "enqueue-pending:true")
	return &store.Job{
		Kind: kind, Target: target, Force: force, Status: store.StatusPending,
	}, nil
}

func TestReconcilePublicationsQueuesBeforeRetiringRuntimeExtractorDrift(
	t *testing.T,
) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	if harness.state.publication == nil {
		t.Fatal("worker did not publish the complete generation")
	}
	repositoryDirectory := filepath.Join(
		Root(harness.worker.dataDir),
		callerleafid.RepositoryDirectory(harness.state.repo.Name),
	)
	if _, err := os.Stat(repositoryDirectory); err != nil {
		t.Fatalf("stat published repository directory: %v", err)
	}

	retiredRegistry := &Registry{
		adapters: slices.Clone(harness.worker.registry.adapters),
	}
	retiredRegistry.adapters[0].Version = "1.5.1"
	before := len(harness.state.events)
	report, err := ReconcilePublications(
		t.Context(), harness.worker.dataDir,
		&publicationReconcileTestStore{workerTestStore: harness.state},
		retiredRegistry,
		callerpublication.NewRegistry(Root(harness.worker.dataDir)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events[before:], []string{
		"enqueue-pending:true", "clear-publication",
	}; !slices.Equal(got, want) {
		t.Fatalf("retirement order = %v, want %v", got, want)
	}
	if report.ReplacementsQueued != 1 || report.PointersCleared != 1 {
		t.Fatalf("reconciliation report = %+v", report)
	}
	if harness.state.publication != nil {
		t.Fatalf("retired publication remains: %+v", harness.state.publication)
	}
	if _, err := os.Stat(repositoryDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired repository directory still exists: %v", err)
	}
}

func TestReconcilePublicationsRepairsInvalidPairPayload(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	if harness.state.publication == nil {
		t.Fatal("worker did not publish the complete generation")
	}
	harness.state.summaryPayloadInvalid = true
	before := len(harness.state.events)
	report, err := ReconcilePublications(
		t.Context(), harness.worker.dataDir,
		&publicationReconcileTestStore{workerTestStore: harness.state},
		harness.worker.registry,
		callerpublication.NewRegistry(Root(harness.worker.dataDir)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events[before:], []string{
		"enqueue-pending:true", "clear-publication",
	}; !slices.Equal(got, want) {
		t.Fatalf("pair-payload repair order = %v, want %v", got, want)
	}
	if report.ReplacementsQueued != 1 || report.PointersCleared != 1 {
		t.Fatalf("reconciliation report = %+v", report)
	}
	if harness.state.publication != nil {
		t.Fatalf("invalid publication remains: %+v", harness.state.publication)
	}
}

func TestReconcilePublicationsQueuesBeforeRetiringDigestOnlyUpstream(t *testing.T) {
	harness := newWorkerHarness(t, 1)
	harness.settle(t)
	if harness.state.publication == nil {
		t.Fatal("worker did not publish the complete generation")
	}
	generation := harness.state.publication.Generation
	generation.UpstreamDigest = "sha256:" + strings.Repeat("1", 64)
	generation.Digest = store.ComputeCallerGenerationDigest(generation)
	harness.state.publication.Generation = generation
	harness.state.publication.ManifestPath = callerpublicationid.ManifestName(
		generation.Digest, harness.state.publication.ManifestDigest,
	)
	before := len(harness.state.events)
	report, err := ReconcilePublications(
		t.Context(), harness.worker.dataDir,
		&publicationReconcileTestStore{workerTestStore: harness.state},
		harness.worker.registry,
		callerpublication.NewRegistry(Root(harness.worker.dataDir)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := harness.state.events[before:], []string{
		"enqueue-pending:true", "clear-publication",
	}; !slices.Equal(got, want) {
		t.Fatalf("digest-only repair order = %v, want %v", got, want)
	}
	if report.ReplacementsQueued != 1 || report.PointersCleared != 1 {
		t.Fatalf("reconciliation report = %+v", report)
	}
	if harness.state.publication != nil {
		t.Fatalf("digest-only publication remains: %+v", harness.state.publication)
	}
}
