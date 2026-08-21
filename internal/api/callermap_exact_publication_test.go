package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/api"
	"github.com/bmeddeb/phebs/internal/callerexecute"
	"github.com/bmeddeb/phebs/internal/callerleaf"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/compat"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
	phebssync "github.com/bmeddeb/phebs/internal/sync"
)

const (
	exactCallerRepository = "github.com/acme/exact-callers"
	exactCallerLineage    = "provisional_repo_path_v1_" +
		"abababababababababababababababababababababababababababababababab"
	exactCallerOperation = "/acme.orders.v1.Orders/Get"
	exactDeclarationRun  = "run-exact-proto-declarations"
)

type exactCallerAPIStore struct {
	*proofAPIStore

	candidate   *store.CandidateManifestPublication
	resolver    *store.ResolverCatalogPublication
	publication *store.CallerGenerationPublication
	admission   *store.CallerGenerationAdmission
	// admissionAfterFirst simulates a pointerless admission transition between
	// the initial typed gap read and its result-time fence.
	admissionAfterFirst *store.CallerGenerationAdmission
	admissionReads      int
	progress            store.CallerLeafOutcomeProgress
	progressAfterFirst  *store.CallerLeafOutcomeProgress
	progressReads       int
	repoError           error
	callerJobState      store.JobProjectionState
	callerJob           *store.CallerJobProjection
	callerJobReads      int
	resolverJobState    store.JobProjectionState
	resolverJob         *store.ResolverJobProjection
	resolverJobReads    int

	currentRevision         uint64
	resolverCurrent         bool
	authorityReads          int
	publicationReads        int
	publicationSummaryReads int
	currentChecks           int
	authorityCurrentChecks  int
	afterAuthorityCurrent   func(int)
}

func (state *exactCallerAPIStore) GetCallerJobProjection(
	ctx context.Context,
	repository string,
) (store.JobProjectionState, *store.CallerJobProjection, error) {
	state.callerJobReads++
	repo, err := state.proofAPIStore.GetRepo(ctx, repository)
	if err != nil || repo == nil {
		return store.JobProjectionUnavailable, nil, err
	}
	projectionState := state.callerJobState
	if projectionState == "" {
		projectionState = store.JobProjectionUnavailable
	}
	if state.callerJob == nil {
		return projectionState, nil, nil
	}
	job := *state.callerJob
	return projectionState, &job, nil
}

func (state *exactCallerAPIStore) GetResolverJobProjection(
	ctx context.Context,
	repository string,
) (store.JobProjectionState, *store.ResolverJobProjection, error) {
	state.resolverJobReads++
	repo, err := state.proofAPIStore.GetRepo(ctx, repository)
	if err != nil || repo == nil {
		return store.JobProjectionUnavailable, nil, err
	}
	projectionState := state.resolverJobState
	if projectionState == "" {
		projectionState = store.JobProjectionUnavailable
	}
	if state.resolverJob == nil {
		return projectionState, nil, nil
	}
	job := *state.resolverJob
	return projectionState, &job, nil
}

func (state *exactCallerAPIStore) GetRepo(
	ctx context.Context,
	repository string,
) (*store.Repo, error) {
	if state.repoError != nil {
		return nil, state.repoError
	}
	return state.proofAPIStore.GetRepo(ctx, repository)
}

func (state *exactCallerAPIStore) GetCandidateManifestPublication(
	_ context.Context,
	repository string,
) (*store.CandidateManifestPublication, error) {
	state.authorityReads++
	if state.candidate == nil || state.candidate.Repository != repository {
		return nil, store.ErrNotFound
	}
	copyOfPointer := *state.candidate
	return &copyOfPointer, nil
}

func (state *exactCallerAPIStore) GetResolverCatalogPublication(
	_ context.Context,
	repository string,
) (*store.ResolverCatalogPublication, error) {
	state.authorityReads++
	if state.resolver == nil || state.resolver.Repository != repository {
		return nil, store.ErrNotFound
	}
	copyOfPointer := *state.resolver
	copyOfPointer.Declarations = slices.Clone(state.resolver.Declarations)
	copyOfPointer.ResolverPacks = slices.Clone(state.resolver.ResolverPacks)
	return &copyOfPointer, nil
}

func (state *exactCallerAPIStore) ResolverCatalogPublicationCurrent(
	_ context.Context,
	pointer store.ResolverCatalogPublication,
) (bool, error) {
	state.currentChecks++
	return state.resolverCurrent && state.resolver != nil &&
		pointer.GenerationDigest == state.resolver.GenerationDigest &&
		pointer.ManifestDigest == state.resolver.ManifestDigest &&
		pointer.ControlRevision == state.resolver.ControlRevision, nil
}

func (state *exactCallerAPIStore) GetCallerGenerationPublication(
	_ context.Context,
	repository string,
) (*store.CallerGenerationPublication, error) {
	state.publicationReads++
	if state.publication == nil ||
		state.publication.Generation.Repository != repository {
		return nil, store.ErrNotFound
	}
	copyOfPointer := *state.publication
	copyOfPointer.Pairs = append(
		[]store.CallerGenerationPairPublication(nil),
		state.publication.Pairs...,
	)
	return &copyOfPointer, nil
}

func (state *exactCallerAPIStore) GetCallerGenerationPublicationSummary(
	_ context.Context,
	repository string,
) (*store.CallerGenerationPublicationSummary, error) {
	state.publicationSummaryReads++
	if state.publication == nil ||
		state.publication.Generation.Repository != repository {
		return nil, store.ErrNotFound
	}
	publication := state.publication
	return &store.CallerGenerationPublicationSummary{
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
	}, nil
}

func (state *exactCallerAPIStore) CallerGenerationPublicationSummaryCurrent(
	_ context.Context,
	summary store.CallerGenerationPublicationSummary,
) (bool, error) {
	state.currentChecks++
	return state.summaryCurrent(summary), nil
}

func (state *exactCallerAPIStore) CallerGenerationPublicationSummaryAuthorityCurrent(
	_ context.Context,
	summary store.CallerGenerationPublicationSummary,
) (bool, error) {
	state.currentChecks++
	state.authorityCurrentChecks++
	current := state.summaryCurrent(summary)
	if state.afterAuthorityCurrent != nil {
		state.afterAuthorityCurrent(state.authorityCurrentChecks)
	}
	return current, nil
}

func (state *exactCallerAPIStore) CallerGenerationPublicationSummariesAuthorityCurrent(
	_ context.Context,
	summaries []store.CallerGenerationPublicationSummary,
) (bool, error) {
	if len(summaries) < 1 || len(summaries) > 2 {
		return false, errors.New("caller publication joint fence requires one or two summaries")
	}
	state.currentChecks++
	state.authorityCurrentChecks++
	current := true
	for _, summary := range summaries {
		current = current && state.summaryCurrent(summary)
	}
	if state.afterAuthorityCurrent != nil {
		state.afterAuthorityCurrent(state.authorityCurrentChecks)
	}
	return current, nil
}

func (state *exactCallerAPIStore) summaryCurrent(
	summary store.CallerGenerationPublicationSummary,
) bool {
	publication := state.publication
	return publication != nil &&
		summary.Generation == publication.Generation &&
		summary.PairPayloadDigest == publication.PairPayloadDigest &&
		summary.PairSetDigest == publication.PairSetDigest &&
		summary.PairCount == publication.PairCount &&
		summary.ResultCount == publication.ResultCount &&
		summary.AbstentionCount == publication.AbstentionCount &&
		summary.ManifestDigest == publication.ManifestDigest &&
		summary.ManifestPath == publication.ManifestPath &&
		summary.PublicationRevision == state.currentRevision &&
		publication.PublicationRevision == state.currentRevision &&
		summary.PublicationIncarnation == publication.PublicationIncarnation
}

func (state *exactCallerAPIStore) GetCallerGenerationAdmission(
	_ context.Context,
	generation store.CallerGenerationIdentity,
) (*store.CallerGenerationAdmission, error) {
	state.admissionReads++
	if state.admissionReads > 1 && state.admissionAfterFirst != nil {
		copyOfAdmission := *state.admissionAfterFirst
		state.admission = &copyOfAdmission
	}
	if state.admission == nil || state.admission.Generation != generation {
		return nil, store.ErrNotFound
	}
	copyOfAdmission := *state.admission
	return &copyOfAdmission, nil
}

func (state *exactCallerAPIStore) GetCallerLeafOutcomeProgress(
	_ context.Context,
	_ store.CallerGenerationIdentity,
) (store.CallerLeafOutcomeProgress, error) {
	state.progressReads++
	if state.progressReads > 1 && state.progressAfterFirst != nil {
		state.progress = *state.progressAfterFirst
	}
	return state.progress, nil
}

type exactCallerAPIFixture struct {
	dataDir                 string
	repository              string
	commit                  string
	objectID                string
	blobDigest              string
	source                  string
	query                   api.CallerMapQuery
	store                   *exactCallerAPIStore
	service                 *api.CallerMapService
	principal               string
	visible                 map[string]bool
	visibilityChecks        int
	revokeOnVisibilityCheck int
	publication             *callerpublication.Publication
	publications            *callerpublication.Registry
	reader                  *callerexecute.PublicationReader
}

type exactCallerImpactWorkbench struct {
	store.InvestigationWorkbench
	view          *store.WorkbenchView
	compatibility *store.WorkbenchCompatibilityAnalysis
}

func (workbench *exactCallerImpactWorkbench) Read(
	_ context.Context,
	_, _ string,
) (*store.WorkbenchView, error) {
	if workbench.view == nil {
		return nil, store.ErrNotFound
	}
	value := *workbench.view
	return &value, nil
}

func (workbench *exactCallerImpactWorkbench) ReadCompatibility(
	_ context.Context,
	_, investigationID, revisionID, runID string,
) (*store.WorkbenchCompatibilityAnalysis, error) {
	if workbench.compatibility == nil ||
		workbench.compatibility.InvestigationID != investigationID ||
		workbench.compatibility.RevisionID != revisionID ||
		workbench.compatibility.Run.ID != runID {
		return nil, store.ErrNotFound
	}
	value := *workbench.compatibility
	return &value, nil
}

func newExactCallerAPIFixture(t *testing.T, recordCount int) *exactCallerAPIFixture {
	return newExactCallerAPIFixtureWithDetail(t, recordCount, nil)
}

func newExactCallerAPIFixtureWithDetail(
	t *testing.T,
	recordCount int,
	mutate func(map[string]any),
) *exactCallerAPIFixture {
	return newExactCallerAPIFixtureWithMutators(t, recordCount, mutate, nil)
}

func newExactCallerAPIFixtureWithMutators(
	t *testing.T,
	recordCount int,
	mutateDetail func(map[string]any),
	mutateRecord func(*callerleaf.Record),
) *exactCallerAPIFixture {
	return newExactCallerAPIFixtureConfigured(
		t, recordCount, exactCallerRepository, t.TempDir(),
		mutateDetail, mutateRecord,
	)
}

func newExactCallerAPIFixtureConfigured(
	t *testing.T,
	recordCount int,
	repositoryName string,
	dataDir string,
	mutateDetail func(map[string]any),
	mutateRecord func(*callerleaf.Record),
) *exactCallerAPIFixture {
	t.Helper()
	if recordCount < 1 || recordCount > callerleaf.MaxResultRecordsPerPair {
		t.Fatalf("invalid exact caller fixture record count %d", recordCount)
	}
	fixture := &exactCallerAPIFixture{
		dataDir: dataDir, repository: repositoryName,
		principal: "user:exact-member",
		visible:   map[string]bool{exactCallerRepository: true},
		query: api.CallerMapQuery{Endpoint: api.CallerMapEndpoint{
			Protocol: "protobuf", Repository: exactCallerRepository,
			Lineage: exactCallerLineage, Operation: exactCallerOperation,
		}},
	}

	var source strings.Builder
	offsets := make([]int, recordCount+1)
	for index := range recordCount {
		offsets[index] = source.Len()
		_, _ = fmt.Fprintf(&source, "call%05d()\n", index)
	}
	offsets[recordCount] = source.Len()
	fixture.source = source.String()
	origin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(origin, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(origin, "src", "calls.go"), []byte(fixture.source), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(origin, "idl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(origin, "idl", "orders.proto"),
		[]byte("service Orders {}\n"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "init", "-b", "main")
	runGit(t, origin, "add", "--all")
	runGit(t, origin, "commit", "-m", "exact caller API fixture")
	fixture.commit = gitOutput(t, origin, "rev-parse", "HEAD")
	fixture.objectID = gitOutput(t, origin, "rev-parse", "HEAD:src/calls.go")
	contentDigest := sha256.Sum256([]byte(fixture.source))
	fixture.blobDigest = "sha256:" + hex.EncodeToString(contentDigest[:])
	if err := phebssync.Mirror(
		t.Context(), "file://"+origin,
		phebssync.RepoDir(fixture.dataDir, fixture.repository),
	); err != nil {
		t.Fatal(err)
	}

	digest := func(value byte) string {
		return "sha256:" + strings.Repeat(string([]byte{value}), 64)
	}
	candidatePointer := store.CandidateManifestPublication{
		Repository: fixture.repository, HeadCommit: fixture.commit,
		PolicyDigest: digest('a'), ManifestDigest: digest('b'),
		GenerationDigest: digest('c'), ManifestPath: "candidate-manifest.json",
		ControlRevision: 1, PublishedAt: time.Unix(1, 0).UTC(),
	}
	resolverPointer := store.ResolverCatalogPublication{
		Repository: fixture.repository, HeadCommit: fixture.commit,
		Declarations: []store.ResolverCatalogDeclarationPublication{{
			Domain: "proto-contract", RunID: exactDeclarationRun,
			GenerationDigest: digest('d'),
		}},
		DeclarationSetDigest:    digest('e'),
		CandidateManifestDigest: candidatePointer.ManifestDigest,
		SourceLanePolicy:        "candidate-source-lane-base-v1",
		ResolverPacks:           []store.ResolverCatalogPack{},
		ResolverPackSetDigest:   digest('f'),
		CatalogPolicyDigest:     digest('1'),
		GenerationDigest:        digest('2'),
		ManifestDigest:          digest('3'),
		AuthorityDigest:         digest('4'),
		ManifestPath:            "resolver-catalog-manifest.json",
		ControlRevision:         1,
		WriterSchema:            resolvercatalogid.WriterSchema,
		PublishedAt:             time.Unix(2, 0).UTC(),
	}
	adapters, err := callerexecute.NewRegistry([]sdk.Extractor{gocaller.NewGRPC()})
	if err != nil {
		t.Fatal(err)
	}
	repository := store.Repo{
		Name: fixture.repository, IndexedCommitHash: fixture.commit,
		CallerPublicationRevision: 1,
	}
	semantic, err := callerexecute.GenerationIdentity(
		callerexecute.GenerationAuthority{
			Repository: &repository, Candidate: &candidatePointer,
			Resolver: &resolverPointer,
		},
		adapters,
	)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := callerleaf.NewPairIdentity(
		semantic, "grpc-caller", "1.5.0", callerleaf.LeafDescriptor{
			Name: "candidate-caller-000000.ndjson", Ordinal: 0,
			Prefix: "00", PrefixBits: 2, RecordCount: 1,
			DeclaredBytes: int64(len(fixture.source)),
			ContentBytes:  int64(len(fixture.source)), ContentDigest: fixture.blobDigest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	root := callerexecute.Root(fixture.dataDir)
	stage, err := callerleaf.NewStage(root, semantic, pair)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Discard() })
	detailInput := map[string]any{
		"schema": "go-caller-detail-v1", "resolution": "direct-syntax",
		"protocol": "grpc", "resolver_catalog_digest": resolverPointer.AuthorityDigest,
		"unit_state": "resolved",
		"unit_candidates": []map[string]any{{
			"id":               "au_" + strings.Repeat("a", 64),
			"logical_services": []string{"orders"},
			"owners":           []string{"team-orders"},
		}},
	}
	if mutateDetail != nil {
		mutateDetail(detailInput)
	}
	detail, err := json.Marshal(detailInput)
	if err != nil {
		t.Fatal(err)
	}
	for index := recordCount - 1; index >= 0; index-- {
		start, end := offsets[index], offsets[index+1]-1
		record := callerleaf.Record{
			Schema: callerleaf.RecordSchema, Kind: callerleaf.RecordResult,
			Path: "src/calls.go", ObjectID: fixture.objectID,
			SourceLane: candidate.SourceLaneBase,
			Fact: &sdk.Fact{
				Path: "src/calls.go", StartLine: index + 1, EndLine: index + 1,
				Atom: sdk.AtomInput{
					SchemaVersion: "t30.6j-test", BlobDigest: fixture.blobDigest,
					StartByte: start, EndByte: end, RuleID: "go-caller-direct-syntax-v1",
					AdapterConfigDigest: digest('4'),
					FactFingerprint:     strconv.Itoa(index),
				},
				Assertion: sdk.AssertionInput{
					Predicate: "CALLS_OPERATION",
					Subject:   "src/calls.go:" + strconv.Itoa(start) + "-" + strconv.Itoa(end),
					Object:    exactCallerOperation, Lineage: exactCallerLineage,
					Tier: store.TierHeuristic, CodeRole: "production",
					Detail: string(detail),
				},
			},
		}
		if mutateRecord != nil {
			mutateRecord(&record)
		}
		if err := stage.Add(record); err != nil {
			t.Fatalf("add exact caller record %d: %v", index, err)
		}
	}
	preparedLeaf, err := stage.Seal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = preparedLeaf.Discard() })
	leaf, err := preparedLeaf.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := callerpublication.BuildManifest(
		semantic, []callerpublication.PairReceipt{{Pair: pair, Receipt: leaf.Receipt()}},
	)
	if err != nil {
		t.Fatal(err)
	}
	preparedPublication, err := callerpublication.PrepareWithValidated(
		t.Context(), root, manifest,
		map[string]*callerleaf.Publication{pair.Digest: leaf},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = preparedPublication.Discard() })
	fixture.publication, err = preparedPublication.Publish(
		t.Context(), func(context.Context, callerpublication.State) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}

	storedGeneration := store.CallerGenerationIdentity{
		Repository: semantic.Repository, HeadCommit: semantic.HeadCommit,
		UnitDigest: semantic.UnitDigest, DeclarationSetDigest: semantic.DeclarationSetDigest,
		CandidateManifestDigest:  semantic.CandidateManifestDigest,
		CandidatePolicyDigest:    semantic.CandidatePolicyDigest,
		CandidateControlRevision: candidatePointer.ControlRevision,
		ResolverGenerationDigest: semantic.ResolverGenerationDigest,
		ResolverManifestDigest:   semantic.ResolverManifestDigest,
		ResolverControlRevision:  resolverPointer.ControlRevision,
		ResolverWriterSchema:     resolverPointer.WriterSchema,
		SourceLanePolicy:         semantic.SourceLanePolicy,
		CallerPolicyDigest:       semantic.CallerPolicyDigest,
		ExtractorSetDigest:       semantic.ExtractorSetDigest, Digest: semantic.Digest,
	}
	receipt := leaf.Receipt()
	storePair := store.CallerLeafPair{
		Domain: pair.Domain, ExtractorVersion: pair.ExtractorVersion,
		LeafAdapterVersion: pair.LeafAdapterVersion,
		LeafOrdinal:        pair.Leaf.Ordinal, LeafPrefix: pair.Leaf.Prefix,
		LeafPrefixBits:         pair.Leaf.PrefixBits,
		CandidateMemberName:    pair.Leaf.Name,
		CandidateRecordCount:   pair.Leaf.RecordCount,
		CandidateDeclaredBytes: pair.Leaf.DeclaredBytes,
		CandidateContentBytes:  pair.Leaf.ContentBytes,
		CandidateContentDigest: pair.Leaf.ContentDigest,
		PairDigest:             pair.Digest,
	}
	storeReceipt := store.CallerLeafArtifactReceipt{
		ArtifactName: receipt.Name, ArtifactCount: 1,
		ResultCount: receipt.ResultCount, AbstentionCount: receipt.AbstentionCount,
		CanonicalBytes: receipt.ContentBytes, StagingBytes: receipt.StagingBytes,
		ContentDigest: receipt.ContentDigest, MetadataDigest: receipt.MetadataDigest,
		ExcludedGoTestRecords: receipt.ExcludedGoTestRecords,
		SourceBlobReads:       receipt.SourceBlobReads,
		SourceBlobBytes:       receipt.SourceBlobBytes,
		OutOfLeafReads:        receipt.OutOfLeafReads,
	}
	aggregate := manifest.Aggregate
	publicationPointer := &store.CallerGenerationPublication{
		Generation: storedGeneration,
		Pairs: []store.CallerGenerationPairPublication{{
			Pair: storePair, Receipt: storeReceipt,
			RecordCount: receipt.RecordCount,
		}},
		PairPayloadDigest: digest('5'), PairSetDigest: manifest.PairSetDigest,
		PairCount: aggregate.PairCount, ArtifactCount: aggregate.ArtifactCount,
		ResultCount: aggregate.ResultCount, AbstentionCount: aggregate.AbstentionCount,
		CanonicalBytes: aggregate.CanonicalBytes, StagingBytes: aggregate.StagingBytes,
		PeakOpenFiles:  aggregate.PeakOpenFiles,
		ManifestDigest: manifest.Digest, ManifestPath: manifest.State().Manifest,
		PublicationRevision:    1,
		PublicationIncarnation: digest('6'),
		WriterSchema:           store.CallerGenerationPublicationWriterSchema,
		PublishedAt:            time.Unix(3, 0).UTC(),
	}
	evidence := &proofAPIStore{
		repos: []store.Repo{repository}, assertions: make(map[string][]store.Assertion),
		resolutions: make(map[string]store.EvidenceResolution),
		runs:        make(map[string]store.ExtractionRun),
		attempts:    make(map[string]store.ExtractionAttempt),
	}
	declaration, resolution := catalogAssertion(
		fixture.repository, exactDeclarationRun, "exact-declaration",
		"DECLARES_OPERATION", "idl/orders.proto",
		strings.TrimPrefix(exactCallerOperation, "/"), exactCallerLineage,
		`{"schema":"proto-operation-detail-v1"}`,
	)
	resolution.Occurrences[0].Commit = fixture.commit
	putCatalogAssertion(evidence, declaration, resolution)
	fixture.store = &exactCallerAPIStore{
		proofAPIStore: evidence, candidate: &candidatePointer,
		resolver: &resolverPointer, publication: publicationPointer,
		currentRevision: publicationPointer.PublicationRevision,
		resolverCurrent: true,
	}
	fixture.publications = callerpublication.NewRegistry(root)
	t.Cleanup(func() { _ = fixture.publications.Close() })
	reader, err := callerexecute.NewPublicationReader(
		fixture.dataDir, fixture.store, adapters, fixture.publications,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.reader = reader
	probe, err := reader.Open(t.Context(), fixture.repository)
	if err != nil {
		t.Fatalf("open exact caller publication fixture: %v", err)
	}
	if probe.Availability != callerexecute.PublicationCurrent ||
		probe.Resolver == nil || probe.Publication == nil || probe.Summary == nil ||
		probe.State == nil || probe.Lease() == nil {
		t.Fatalf("serving publication fixture is structurally incomplete: %+v", probe)
	}
	if err := probe.Release(); err != nil {
		t.Fatalf("release exact caller publication fixture probe: %v", err)
	}
	fixture.store.authorityReads = 0
	fixture.store.publicationReads = 0
	fixture.store.currentChecks = 0
	fixture.store.authorityCurrentChecks = 0
	options := fixture.serviceOptions()
	options.CallerReader = reader
	fixture.service = api.NewCallerMapService(options)
	if fixture.service == nil {
		t.Fatal("exact Caller Map service is unavailable")
	}
	return fixture
}

func TestExactCallerMapAuthorizesBeforePublicationIO(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 3)
	fixture.visible[fixture.repository] = false
	_, err := fixture.service.List(t.Context(), fixture.query, 1, "")
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.authorityReads != 0 || fixture.store.publicationReads != 0 ||
		fixture.store.currentChecks != 0 {
		t.Fatalf(
			"hidden repository reached publication I/O: authority=%d publication=%d current=%d",
			fixture.store.authorityReads, fixture.store.publicationReads,
			fixture.store.currentChecks,
		)
	}
	for _, call := range fixture.store.calls {
		if strings.HasPrefix(call, "assertions:") ||
			strings.HasPrefix(call, "resolve:") || call == "list-repos" {
			t.Fatalf("hidden repository reached evidence inventory: %v", fixture.store.calls)
		}
	}
}

func TestExactCallerMapPreservesOperationalAuthorizationErrors(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 3)
	fixture.store.repoError = errors.New("repository store unavailable")
	_, err := fixture.service.List(t.Context(), fixture.query, 1, "")
	requireCatalogStatus(t, err, http.StatusInternalServerError)
	if fixture.store.authorityReads != 0 || fixture.store.publicationReads != 0 ||
		fixture.store.currentChecks != 0 {
		t.Fatalf(
			"authorization failure reached publication I/O: authority=%d publication=%d current=%d",
			fixture.store.authorityReads, fixture.store.publicationReads,
			fixture.store.currentChecks,
		)
	}
}

func TestExactCallerMapReportsCurrentAndTypedGaps(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 3)
	page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.MatchingRowsState != "exact" || page.Generation == nil ||
		page.Generation.State != "current" || page.TotalMatchingRows == nil ||
		*page.TotalMatchingRows != 3 ||
		len(page.Rows) != 3 || page.Coverage != nil || page.Scope == nil ||
		page.Scope.Repository != fixture.repository ||
		page.Scope.Commit != fixture.commit ||
		page.Scope.ScopePosture != "whole-repository" ||
		page.Scope.AnalysisUnit != nil ||
		page.Generation.RecordCounts == nil ||
		page.Generation.RecordCounts.CandidateRecords != 1 ||
		page.Generation.RecordCounts.BaseRecords != 1 ||
		page.Generation.RecordCounts.ExcludedGoTestRecords != 0 ||
		page.Generation.PartitionProgress == nil ||
		page.Generation.PartitionProgress.State != "complete" ||
		page.Generation.PartitionProgress.TotalPairCount == nil ||
		*page.Generation.PartitionProgress.TotalPairCount != 1 ||
		fixture.store.progressReads != 0 {
		t.Fatalf("current exact page = %+v", page)
	}
	for _, call := range fixture.store.calls {
		if call == "list-repos" || strings.HasPrefix(call, "latest:") ||
			strings.HasPrefix(call, "reverse:") {
			t.Fatalf("exact page consulted generic fleet coverage/evidence: %v", fixture.store.calls)
		}
	}
	basePointer := fixture.store.publication
	baseRevision := fixture.store.currentRevision
	baseAdmission := fixture.store.admission
	baseProgress := fixture.store.progress
	for _, test := range []struct {
		name         string
		availability string
		progress     string
		configure    func()
	}{
		{
			name: "missing", availability: "missing", progress: "unavailable",
			configure: func() { fixture.store.publication = nil },
		},
		{
			name: "terminal failure", availability: "failed", progress: "complete",
			configure: func() {
				fixture.store.publication = nil
				fixture.store.admission = &store.CallerGenerationAdmission{
					Generation:  basePointer.Generation,
					Disposition: store.CallerGenerationTerminalGenerationRefusal,
					PairCount:   1,
					RecordedAt:  time.Unix(4, 0).UTC(),
				}
				fixture.store.progress = store.CallerLeafOutcomeProgress{
					SettledCount: 1, RefusedCount: 1,
				}
			},
		},
		{
			name: "stale", availability: "stale", progress: "unavailable",
			configure: func() { fixture.store.currentRevision = baseRevision + 1 },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.store.publication = basePointer
			fixture.store.currentRevision = baseRevision
			fixture.store.admission = baseAdmission
			fixture.store.progress = baseProgress
			test.configure()
			gap, err := fixture.service.List(t.Context(), fixture.query, 100, "")
			if err != nil {
				t.Fatal(err)
			}
			if gap.MatchingRowsState != "unavailable" || gap.Generation == nil ||
				gap.Generation.State != test.availability || len(gap.Rows) != 0 ||
				gap.TotalMatchingRows != nil ||
				!gap.Pagination.Complete || gap.Coverage != nil ||
				gap.Scope == nil || gap.Scope.Repository != fixture.repository ||
				gap.Generation.RecordCounts != nil ||
				gap.Generation.PartitionProgress == nil ||
				gap.Generation.PartitionProgress.State != test.progress ||
				!strings.Contains(gap.Caveat, "totals and absence are unavailable") {
				t.Fatalf("%s gap = %+v", test.name, gap)
			}
		})
	}
	fixture.store.publication = basePointer
	fixture.store.currentRevision = baseRevision
	fixture.store.admission = baseAdmission
	fixture.store.progress = baseProgress
}

func TestExactCallerGenerationProgressDoesNotRequireEndpointDeclaration(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 1)
	fixture.store.assertions[fixture.repository] = nil
	fixture.store.callerJobState = store.JobProjectionExact
	fixture.store.callerJob = &store.CallerJobProjection{
		Status: store.StatusRunning, Attempts: 1,
	}

	_, err := fixture.service.List(t.Context(), fixture.query, 1, "")
	requireCatalogStatus(t, err, http.StatusNotFound)

	fixture.store.calls = nil
	progress, err := fixture.service.GenerationProgress(
		t.Context(), fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if progress.SchemaVersion != "caller-generation-progress-v3" ||
		progress.Generation.State != "current" ||
		progress.Generation.GenerationDigest != fixture.store.publication.Generation.Digest ||
		progress.Generation.PartitionProgress == nil ||
		progress.Generation.PartitionProgress.State != "complete" ||
		progress.Generation.PartitionProgress.TotalPairCount == nil ||
		*progress.Generation.PartitionProgress.TotalPairCount != 1 ||
		progress.Scope.Repository != fixture.repository ||
		progress.Scope.Commit != fixture.commit ||
		progress.CallerJobState != store.JobProjectionExact ||
		progress.CallerJob == nil || progress.CallerJob.Status != store.StatusRunning ||
		progress.CallerJob.Attempts != 1 || fixture.store.callerJobReads != 1 ||
		progress.ResolverJobState != store.JobProjectionUnavailable ||
		progress.ResolverJob != nil || fixture.store.resolverJobReads != 1 {
		t.Fatalf("declaration-independent caller progress = %+v", progress)
	}
	for _, call := range fixture.store.calls {
		if strings.HasPrefix(call, "assertions:") ||
			strings.HasPrefix(call, "resolve:") {
			t.Fatalf("caller progress consulted endpoint evidence: %v", fixture.store.calls)
		}
	}
}

func TestExactCallerGenerationProgressPreservesTypedTerminalAndAuthorization(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 1)
	generation := fixture.store.publication.Generation
	fixture.store.publication = nil
	fixture.store.admission = &store.CallerGenerationAdmission{
		Generation:  generation,
		Disposition: store.CallerGenerationTerminalGenerationRefusal,
		PairCount:   1,
		Refusals: []store.CallerGenerationRefusalSummary{{
			Refusal: pipelinerefusal.Receipt{
				Schema:         pipelinerefusal.Schema,
				Stage:          pipelinerefusal.StageCallerGenerationAdmission,
				GenerationKind: pipelinerefusal.GenerationCaller,
				Classification: pipelinerefusal.ClassificationLimit,
				Dimension:      pipelinerefusal.DimensionCallerGenerationAbstentions,
				Observed:       callerleaf.MaxAggregateAbstentionRecords + 1,
				Limit:          callerleaf.MaxAggregateAbstentionRecords,
			},
			OutcomeCount: 1,
		}},
	}
	fixture.store.progress = store.CallerLeafOutcomeProgress{
		SettledCount: 1, RefusedCount: 1,
	}
	progress, err := fixture.service.GenerationProgress(t.Context(), fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	partition := progress.Generation.PartitionProgress
	if progress.Generation.State != "failed" || partition == nil ||
		partition.State != "complete" || partition.TotalPairCount == nil ||
		*partition.TotalPairCount != 1 || partition.RefusedPairCount != 1 ||
		len(partition.Refusals) != 1 ||
		partition.Refusals[0].Dimension !=
			pipelinerefusal.DimensionCallerGenerationAbstentions {
		t.Fatalf("typed terminal caller progress = %+v", progress)
	}

	publicationReads := fixture.store.publicationReads
	authorityReads := fixture.store.authorityReads
	fixture.visible[fixture.repository] = false
	_, err = fixture.service.GenerationProgress(t.Context(), fixture.repository)
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.publicationReads != publicationReads ||
		fixture.store.authorityReads != authorityReads {
		t.Fatalf(
			"unauthorized caller progress reached publication I/O: publication=%d/%d authority=%d/%d",
			fixture.store.publicationReads, publicationReads,
			fixture.store.authorityReads, authorityReads,
		)
	}
}

func TestExactCallerMapRejectsGapTransitionAtResultFence(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 1)
	generation := fixture.store.publication.Generation
	fixture.store.publication = nil
	fixture.store.admission = nil
	fixture.store.admissionReads = 0
	fixture.store.admissionAfterFirst = &store.CallerGenerationAdmission{
		Generation:  generation,
		Disposition: store.CallerGenerationTerminalGenerationRefusal,
		RecordedAt:  time.Unix(4, 0).UTC(),
	}

	_, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	requireCatalogStatus(t, err, http.StatusConflict)
	if fixture.store.admissionReads != 2 {
		t.Fatalf(
			"gap result fence admission reads = %d, want 2",
			fixture.store.admissionReads,
		)
	}

	fixture.store.admissionAfterFirst = nil
	fixture.store.admissionReads = 0
	page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Generation == nil || page.Generation.State != "failed" ||
		page.MatchingRowsState != "unavailable" {
		t.Fatalf("settled terminal gap = %+v", page)
	}

	fixture.store.admission = nil
	fixture.store.admissionReads = 0
	fixture.store.progress = store.CallerLeafOutcomeProgress{}
	fixture.store.progressReads = 0
	fixture.store.progressAfterFirst = &store.CallerLeafOutcomeProgress{
		SettledCount: 1, SucceededCount: 1,
	}
	_, err = fixture.service.List(t.Context(), fixture.query, 100, "")
	requireCatalogStatus(t, err, http.StatusConflict)
	if fixture.store.progressReads != 2 {
		t.Fatalf(
			"gap result fence progress reads = %d, want 2",
			fixture.store.progressReads,
		)
	}
}

func TestExactCallerMapReportsPartialPartitionProgressWithoutInventingTotal(
	t *testing.T,
) {
	fixture := newExactCallerAPIFixture(t, 1)
	fixture.store.publication = nil
	fixture.store.admission = nil
	fixture.store.progress = store.CallerLeafOutcomeProgress{
		SettledCount: 1, SucceededCount: 1,
	}

	page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Generation == nil || page.Generation.PartitionProgress == nil ||
		page.Generation.PartitionProgress.State != "partial" ||
		page.Generation.PartitionProgress.SettledPairCount != 1 ||
		page.Generation.PartitionProgress.SucceededPairCount != 1 ||
		page.Generation.PartitionProgress.RefusedPairCount != 0 ||
		page.Generation.PartitionProgress.TotalPairCount != nil ||
		page.Generation.RecordCounts != nil || fixture.store.progressReads != 2 {
		t.Fatalf("partial caller partition page = %+v", page)
	}
}

func TestExactCallerMapReportsSemanticArtifactFailureAsGap(t *testing.T) {
	fixture := newExactCallerAPIFixtureWithDetail(
		t, 1, func(detail map[string]any) {
			detail["resolver_catalog_digest"] = "sha256:" + strings.Repeat("9", 64)
		},
	)
	page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Generation == nil || page.Generation.State != "failed" ||
		page.MatchingRowsState != "unavailable" || len(page.Rows) != 0 ||
		page.TotalMatchingRows != nil || page.Declaration != nil ||
		page.Generation.RecordCounts == nil ||
		page.Generation.RecordCounts.CandidateRecords != 1 ||
		page.Generation.RecordCounts.BaseRecords != 1 {
		t.Fatalf("semantic artifact gap = %+v", page)
	}

	// Preserve the descriptor identity but make a rescan fail generic artifact
	// validation. The second request must reuse the exact-key negative entry;
	// otherwise it would reopen these bytes and return a physical-artifact
	// conflict instead of the already settled semantic failure.
	manifest := fixture.publication.Manifest()
	artifactPath, err := callerleaf.ArtifactPath(
		callerexecute.Root(fixture.dataDir), fixture.repository,
		manifest.Pairs[0].Receipt,
	)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(artifactPath)
	if err != nil || len(raw) == 0 {
		t.Fatalf("read semantic fixture artifact: bytes=%d err=%v", len(raw), err)
	}
	raw[0] ^= 0x01
	if err := os.WriteFile(artifactPath, raw, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(
		artifactPath, info.ModTime(), info.ModTime(),
	); err != nil {
		t.Fatal(err)
	}

	second, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	if err != nil {
		t.Fatalf("negative-cached semantic gap: %v", err)
	}
	if second.Generation == nil || second.Generation.State != "failed" ||
		second.MatchingRowsState != "unavailable" {
		t.Fatalf("negative-cached semantic gap = %+v", second)
	}
}

func TestExactCallerMapRefusesPersistedContractContradictions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*callerleaf.Record)
	}{
		{
			name: "oversized noncanonical operation",
			mutate: func(record *callerleaf.Record) {
				record.Fact.Assertion.Object = "/" + strings.Repeat("s", 1_023) + "/Call"
			},
		},
		{
			name: "result envelope carrying an abstention",
			mutate: func(record *callerleaf.Record) {
				record.Fact.Assertion.Predicate = "UNRESOLVED_CALLER"
				record.Fact.Assertion.Lineage = ""
				record.Fact.Assertion.Tier = store.TierUnresolved
			},
		},
		{
			name: "direct syntax result claiming exact confidence",
			mutate: func(record *callerleaf.Record) {
				record.Fact.Assertion.Tier = store.TierExact
			},
		},
		{
			name: "direct syntax result with no code role",
			mutate: func(record *callerleaf.Record) {
				record.Fact.Assertion.CodeRole = ""
			},
		},
		{
			name: "source byte range exceeds direct source bound",
			mutate: func(record *callerleaf.Record) {
				record.Fact.Atom.EndByte = int(callerleaf.MaxDirectSourceBytes) + 1
				record.Fact.Assertion.Subject = record.Path + ":" +
					strconv.Itoa(record.Fact.Atom.StartByte) + "-" +
					strconv.Itoa(record.Fact.Atom.EndByte)
			},
		},
		{
			name: "source line range exceeds direct source bound",
			mutate: func(record *callerleaf.Record) {
				record.Fact.StartLine = int(callerleaf.MaxDirectSourceBytes) + 2
				record.Fact.EndLine = record.Fact.StartLine
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExactCallerAPIFixtureWithMutators(
				t, 1, nil, test.mutate,
			)
			page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
			if err != nil {
				t.Fatal(err)
			}
			if page.Generation == nil || page.Generation.State != "failed" ||
				page.MatchingRowsState != "unavailable" || len(page.Rows) != 0 ||
				page.TotalMatchingRows != nil {
				t.Fatalf("invalid semantic contract generation = %+v", page)
			}
		})
	}
}

func TestExactCallerMapRefusesUnitCandidateStructuralAmplification(t *testing.T) {
	fixture := newExactCallerAPIFixtureWithDetail(
		t, 1, func(detail map[string]any) {
			candidates := make([]map[string]any, 25_001)
			for index := range candidates {
				candidates[index] = map[string]any{}
			}
			detail["unit_candidates"] = candidates
		},
	)
	page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Generation == nil || page.Generation.State != "failed" ||
		page.MatchingRowsState != "unavailable" || len(page.Rows) != 0 ||
		page.TotalMatchingRows != nil {
		t.Fatalf("amplified unit-candidate generation = %+v", page)
	}
}

func TestExactCallerMapTraversesTenThousandRowsWithoutLegacyCoverage(t *testing.T) {
	const recordCount = 10_005
	fixture := newExactCallerAPIFixture(t, recordCount)
	var (
		cursor       string
		seen         int
		previousByte = -1
	)
	for pageNumber := 0; ; pageNumber++ {
		page, err := fixture.service.List(
			t.Context(), fixture.query, 100, cursor,
		)
		if err != nil {
			t.Fatalf("page %d: %v", pageNumber, err)
		}
		if page.TotalMatchingRows == nil || *page.TotalMatchingRows != recordCount ||
			page.MatchingRowsState != "exact" ||
			page.Generation == nil || page.Generation.ResultCount != recordCount ||
			page.Coverage != nil {
			t.Fatalf("page %d envelope = %+v", pageNumber, page)
		}
		for _, row := range page.Rows {
			if row.Source.StartByte <= previousByte ||
				row.Source.Repository != fixture.repository ||
				row.Source.Commit != fixture.commit ||
				row.Source.ObjectID != fixture.objectID ||
				row.Source.BlobDigest != fixture.blobDigest ||
				row.Source.Plane != "repository-overlay" || row.Source.Citation == "" {
				t.Fatalf("row %d is unordered or unbound: %+v", seen, row)
			}
			previousByte = row.Source.StartByte
			seen++
		}
		if page.Pagination.Complete {
			if page.Pagination.NextCursor != "" {
				t.Fatalf("complete page retained cursor %q", page.Pagination.NextCursor)
			}
			break
		}
		if page.Pagination.NextCursor == "" {
			t.Fatalf("page %d omitted continuation", pageNumber)
		}
		cursor = page.Pagination.NextCursor
	}
	if seen != recordCount {
		t.Fatalf("traversed %d rows, want %d", seen, recordCount)
	}
	for _, call := range fixture.store.calls {
		if call == "list-repos" || strings.HasPrefix(call, "latest:") ||
			strings.HasPrefix(call, "reverse:") {
			t.Fatalf("scale traversal consulted legacy fleet state: %v", fixture.store.calls)
		}
	}
	if fixture.store.publicationReads != 1 {
		t.Fatalf(
			"scale traversal materialized full publication %d times, want once",
			fixture.store.publicationReads,
		)
	}
}

func TestExactCallerMapCursorBindsTamperQueryPrincipalAndRevision(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 5)
	first, err := fixture.service.List(t.Context(), fixture.query, 2, "")
	if err != nil || first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	cursor := first.Pagination.NextCursor
	publicationReads := fixture.store.publicationReads
	currentChecks := fixture.store.currentChecks
	fixture.visible[fixture.repository] = false
	_, err = fixture.service.List(t.Context(), fixture.query, 2, cursor)
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.publicationReads != publicationReads ||
		fixture.store.currentChecks != currentChecks {
		t.Fatalf(
			"revoked cursor reached publication I/O: publication=%d/%d current=%d/%d",
			fixture.store.publicationReads, publicationReads,
			fixture.store.currentChecks, currentChecks,
		)
	}
	fixture.visible[fixture.repository] = true

	tampered := cursor[:len(cursor)-1] + map[bool]string{
		true: "A", false: "B",
	}[cursor[len(cursor)-1] != 'A']
	_, err = fixture.service.List(t.Context(), fixture.query, 2, tampered)
	requireCatalogStatus(t, err, http.StatusBadRequest)

	changedQuery := fixture.query
	changedQuery.PathPrefix = "src/"
	_, err = fixture.service.List(t.Context(), changedQuery, 2, cursor)
	requireCatalogStatus(t, err, http.StatusBadRequest)

	fixture.principal = "user:another-member"
	_, err = fixture.service.List(t.Context(), fixture.query, 2, cursor)
	requireCatalogStatus(t, err, http.StatusConflict)
	fixture.principal = "user:exact-member"

	// Repository deletion may reset its local revision and republish identical
	// semantic bytes within the same store timestamp second. The store-owned
	// writer-job incarnation still invalidates the old process binding.
	originalIncarnation := fixture.store.publication.PublicationIncarnation
	fixture.store.publication.PublicationIncarnation = "sha256:" + strings.Repeat("7", 64)
	_, err = fixture.service.List(t.Context(), fixture.query, 2, cursor)
	requireCatalogStatus(t, err, http.StatusConflict)
	fixture.store.publication.PublicationIncarnation = originalIncarnation

	// The immutable A bytes are current again, but the repository-local
	// visibility revision records A -> B -> A and must invalidate A's cursor.
	fixture.store.publication.PublicationRevision += 2
	fixture.store.currentRevision = fixture.store.publication.PublicationRevision
	_, err = fixture.service.List(t.Context(), fixture.query, 2, cursor)
	requireCatalogStatus(t, err, http.StatusConflict)
}

func TestExactCallerMapSuppressesPageWhenAuthorizationIsLostMidRequest(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 2)
	fixture.revokeOnVisibilityCheck = 2
	page, err := fixture.service.List(t.Context(), fixture.query, 1, "")
	if page != nil {
		t.Fatalf("authorization-revoked request returned a page: %+v", page)
	}
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.visibilityChecks != 2 {
		t.Fatalf("visibility checks = %d, want pre-read and result-time", fixture.visibilityChecks)
	}
}

func TestExactCallerMapAuthorizationIsTheLastResultFence(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 2)
	fixture.store.afterAuthorityCurrent = func(check int) {
		// The first scalar check belongs to Open. The second runs inside the
		// final PublicationReader.Current sweep after rows have been built.
		if check == 2 {
			fixture.visible[fixture.repository] = false
		}
	}
	page, err := fixture.service.List(t.Context(), fixture.query, 1, "")
	if page != nil {
		t.Fatalf("authorization revoked during final authority fence returned a page: %+v", page)
	}
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.authorityCurrentChecks != 2 || fixture.visibilityChecks != 2 {
		t.Fatalf(
			"result fences = authority:%d visibility:%d, want 2/2",
			fixture.store.authorityCurrentChecks, fixture.visibilityChecks,
		)
	}
}

func TestExactCallerMapCompletedTraversalRetainsPreviousPageCursor(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 5)
	first, err := fixture.service.List(t.Context(), fixture.query, 2, "")
	if err != nil || first.Pagination.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := fixture.service.List(
		t.Context(), fixture.query, 2, first.Pagination.NextCursor,
	)
	if err != nil || second.Pagination.NextCursor == "" {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	last, err := fixture.service.List(
		t.Context(), fixture.query, 2, second.Pagination.NextCursor,
	)
	if err != nil || !last.Pagination.Complete || len(last.Rows) != 1 {
		t.Fatalf("last page = %+v, %v", last, err)
	}
	previous, err := fixture.service.List(
		t.Context(), fixture.query, 2, first.Pagination.NextCursor,
	)
	if err != nil || len(previous.Rows) != 2 ||
		previous.Rows[0].Source.StartByte != second.Rows[0].Source.StartByte ||
		previous.Pagination.NextCursor == "" {
		t.Fatalf("previous page after completion = %+v, %v", previous, err)
	}
}

func TestExactCallerMapRepeatedFirstPagesPressureRetireOldestBinding(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 1)
	var firstCitation, latestCitation string
	// The process cap is eight. A ninth ordinary first-page read must replace
	// the oldest idle request binding rather than disabling every HTTP/MCP/UI
	// caller until wall-clock expiry.
	for request := range 9 {
		page, err := fixture.service.List(t.Context(), fixture.query, 100, "")
		if err != nil || len(page.Rows) != 1 {
			t.Fatalf("first-page request %d = %+v, %v", request, page, err)
		}
		latestCitation = page.Rows[0].Source.Citation
		if request == 0 {
			firstCitation = latestCitation
		}
	}
	if _, err := fixture.service.ReadCitation(t.Context(), firstCitation); err == nil {
		t.Fatal("pressure-retired citation remained live")
	} else {
		requireCatalogStatus(t, err, http.StatusConflict)
	}
	if citation, err := fixture.service.ReadCitation(
		t.Context(), latestCitation,
	); err != nil || citation.Content == "" {
		t.Fatalf("latest citation = %+v, %v", citation, err)
	}
}

func TestExactCallerMapCitationReadsOnlyBoundImmutableRange(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 3)
	page, err := fixture.service.List(t.Context(), fixture.query, 1, "")
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("caller page = %+v, %v", page, err)
	}
	row := page.Rows[0]
	citation, err := fixture.service.ReadCitation(t.Context(), row.Source.Citation)
	if err != nil {
		t.Fatal(err)
	}
	want := fixture.source[row.Source.StartByte:row.Source.EndByte]
	if citation.Content != want || citation.Content != "call00000()" ||
		citation.Source.Repository != fixture.repository ||
		citation.Source.Commit != fixture.commit ||
		citation.Source.Path != "src/calls.go" ||
		citation.Source.ObjectID != fixture.objectID ||
		citation.Source.BlobDigest != fixture.blobDigest ||
		citation.Source.StartByte != row.Source.StartByte ||
		citation.Source.EndByte != row.Source.EndByte ||
		strings.Contains(citation.Content, "call00001") {
		t.Fatalf("exact citation = %+v, want content %q", citation, want)
	}

	tampered := row.Source.Citation[:len(row.Source.Citation)-1] + "A"
	if tampered == row.Source.Citation {
		tampered = row.Source.Citation[:len(row.Source.Citation)-1] + "B"
	}
	_, err = fixture.service.ReadCitation(t.Context(), tampered)
	requireCatalogStatus(t, err, http.StatusBadRequest)

	publicationReads := fixture.store.publicationReads
	currentChecks := fixture.store.currentChecks
	fixture.visible[fixture.repository] = false
	_, err = fixture.service.ReadCitation(t.Context(), row.Source.Citation)
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.publicationReads != publicationReads ||
		fixture.store.currentChecks != currentChecks {
		t.Fatalf(
			"revoked citation reached publication I/O: publication=%d/%d current=%d/%d",
			fixture.store.publicationReads, publicationReads,
			fixture.store.currentChecks, currentChecks,
		)
	}
	fixture.visible[fixture.repository] = true
	fixture.principal = ""
	_, err = fixture.service.ReadCitation(t.Context(), row.Source.Citation)
	requireCatalogStatus(t, err, http.StatusNotFound)
	if fixture.store.publicationReads != publicationReads ||
		fixture.store.currentChecks != currentChecks {
		t.Fatalf(
			"unauthenticated citation reached publication I/O: publication=%d/%d current=%d/%d",
			fixture.store.publicationReads, publicationReads,
			fixture.store.currentChecks, currentChecks,
		)
	}
}

func TestExactCallerMapHTTPExposesExactProgressAndCitationRoutes(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 2)
	options := fixture.serviceOptions()
	options.CallerMap = fixture.service
	handler := api.New(options)
	values := url.Values{
		"protocol": {"protobuf"}, "repository": {fixture.repository},
		"lineage": {exactCallerLineage}, "operation": {exactCallerOperation},
		"page_size": {"1"},
	}
	var page api.CallerMapPage
	code, body := catalogHTTP(
		t, handler, "/api/contract_callers?"+values.Encode(), &page,
	)
	if code != http.StatusOK || len(page.Rows) != 1 {
		t.Fatalf("HTTP exact page = %d %s / %+v", code, body, page)
	}
	var progress api.CallerGenerationProgress
	code, body = catalogHTTP(
		t, handler, api.CallerGenerationProgressPath+"?repository="+
			url.QueryEscape(fixture.repository), &progress,
	)
	if code != http.StatusOK || progress.Generation.State != "current" ||
		progress.Generation.GenerationDigest != page.Generation.GenerationDigest {
		t.Fatalf("HTTP caller generation progress = %d %s / %+v", code, body, progress)
	}
	var citation api.CallerMapCitation
	code, body = catalogHTTP(
		t, handler, "/api/contract_callers/citation?citation="+
			url.QueryEscape(page.Rows[0].Source.Citation), &citation,
	)
	if code != http.StatusOK || citation.Content != "call00000()" {
		t.Fatalf("HTTP exact citation = %d %s / %+v", code, body, citation)
	}
	if strings.Contains(body, "call00001") {
		t.Fatalf("HTTP citation leaked unrelated source: %s", body)
	}
}

func TestWorkbenchImpactConfirmsCompletedCurrentCallerSnapshot(t *testing.T) {
	const (
		investigationID = "01J00000000000000000000306"
		revisionID      = "ivr_exact_caller_confirmation"
		compatibilityID = "ir_exact_caller_confirmation"
		fieldDomain     = "scip-proto-field"
		fieldLineage    = "contract_scip_package_v1_shop_cart"
		fieldMessage    = "shop.Cart"
	)
	fixture := newExactCallerAPIFixture(t, 1)
	declarationRun := proofRun(
		fixture.repository,
		"proto-contract",
		exactDeclarationRun,
	)
	declarationRun.Commit = fixture.commit
	declarationRun.Coverage.Protocols = []string{"protobuf", "protobuf-shapes"}
	fixture.store.runs[proofScope(fixture.repository, "proto-contract")] =
		declarationRun
	field := compat.FieldIdentity{
		Lineage: fieldLineage,
		Message: fieldMessage,
		Number:  1,
	}
	fieldRun := proofRun(
		fixture.repository,
		fieldDomain,
		"run-exact-caller-confirmation-fields",
	)
	fieldRun.Commit = fixture.commit
	fieldRun.Coverage.AssertionCount = 2
	fieldRun.Coverage.AtomCount = 2
	fixture.store.runs[proofScope(fixture.repository, fieldDomain)] = fieldRun
	for _, marker := range []string{"a", "b"} {
		assertion, resolution := proofAssertion(
			fixture.repository,
			fieldRun.ID,
			"exact-caller-confirmation-field-"+marker,
			"REFERENCES_PROTO_FIELD",
			fieldMessage+"#1",
			fieldLineage,
			"field-"+marker,
		)
		resolution.Occurrences[0].Commit = fixture.commit
		fixture.store.assertions[fixture.repository] = append(
			fixture.store.assertions[fixture.repository],
			assertion,
		)
		fixture.store.resolutions[proofEvidenceScope(
			fixture.repository,
			fieldRun.ID,
			assertion.Supporting[0],
		)] = resolution
	}

	workbench := &exactCallerImpactWorkbench{
		view: &store.WorkbenchView{
			Investigation: store.Investigation{
				ID: investigationID, CurrentRevisionID: revisionID,
			},
			Revision: store.Revision{
				ID: revisionID, InvestigationID: investigationID,
				ContentDigest: "sha256:" + strings.Repeat("7", 64),
			},
			Brief: store.ChangeBrief{
				ID:              "icb_exact_caller_confirmation",
				InvestigationID: investigationID, RevisionID: revisionID,
				TicketKind: store.ChangeBriefModify,
				What: store.ChangeBriefWhat{
					Selections: []store.ChangeBriefContractSelection{{
						Role:               store.ChangeBriefCurrent,
						Protocol:           "protobuf",
						Repository:         fixture.repository,
						DeclarationLineage: exactCallerLineage,
						CanonicalOperation: exactCallerOperation,
					}},
				},
				ContentDigest: "sha256:" + strings.Repeat("8", 64),
			},
		},
		compatibility: &store.WorkbenchCompatibilityAnalysis{
			SchemaVersion:   store.WorkbenchCompatibilityAnalysisSchemaVersion,
			Status:          "published",
			InvestigationID: investigationID,
			RevisionID:      revisionID,
			Run:             store.Run{ID: compatibilityID, RevisionID: revisionID},
			Artifact: store.RunArtifact{
				RunID:         compatibilityID,
				ContentDigest: "sha256:" + strings.Repeat("9", 64),
			},
			Compatibility: &compat.CompatibilityResult{
				Compatible: true, AffectedFields: []compat.FieldIdentity{field},
				Violations: []compat.Violation{},
			},
		},
	}
	opts := fixture.serviceOptions()
	opts.CallerReader = fixture.reader
	opts.CallerMap = fixture.service
	opts.Workbench = workbench
	service := api.NewWorkbenchImpactService(opts)
	if service == nil {
		t.Fatal("exact Workbench Impact service is unavailable")
	}
	request := api.WorkbenchImpactRequest{
		InvestigationID:  investigationID,
		RevisionID:       revisionID,
		CompatibilityRun: compatibilityID,
		PageSize:         1,
	}
	first, err := service.Read(t.Context(), fixture.principal, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Pagination.Complete || first.Pagination.NextCursor == "" ||
		len(first.Callers) != 1 || !first.Callers[0].Pagination.Complete ||
		first.FieldReferences == nil ||
		first.FieldReferences.Pagination.Complete {
		t.Fatalf("first Workbench Impact page = %+v", first)
	}

	request.Cursor = first.Pagination.NextCursor
	fullReads := fixture.store.publicationReads
	summaryReads := fixture.store.publicationSummaryReads
	authorityChecks := fixture.store.authorityCurrentChecks
	second, err := service.Read(t.Context(), fixture.principal, request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Pagination.Complete || second.Pagination.NextCursor != "" ||
		len(second.Callers) != 0 || second.FieldReferences == nil ||
		!second.FieldReferences.Pagination.Complete {
		t.Fatalf("confirmed Workbench Impact page = %+v", second)
	}
	if got := fixture.store.publicationReads - fullReads; got != 0 {
		t.Fatalf("current confirmation full publication reads = %d, want 0", got)
	}
	if got := fixture.store.publicationSummaryReads - summaryReads; got != 1 {
		t.Fatalf("current confirmation summary reads = %d, want 1", got)
	}
	if got := fixture.store.authorityCurrentChecks - authorityChecks; got != 3 {
		t.Fatalf("current confirmation authority checks = %d, want 3", got)
	}

	fixture.store.publication.PublicationIncarnation =
		"sha256:" + strings.Repeat("f", 64)
	fullReads = fixture.store.publicationReads
	summaryReads = fixture.store.publicationSummaryReads
	_, err = service.Read(t.Context(), fixture.principal, request)
	requireCatalogStatus(t, err, http.StatusConflict)
	if got := fixture.store.publicationReads - fullReads; got != 0 {
		t.Fatalf("ABA confirmation full publication reads = %d, want 0", got)
	}
	if got := fixture.store.publicationSummaryReads - summaryReads; got != 1 {
		t.Fatalf("ABA confirmation summary reads = %d, want 1", got)
	}
}

func (fixture *exactCallerAPIFixture) serviceOptions() api.Options {
	return api.Options{
		Version: "test", Store: fixture.store, Evidence: fixture.store,
		DataDir: fixture.dataDir, CallerMapEnabled: true,
		Principal: func(context.Context) string { return fixture.principal },
		Visible: func(context.Context) func(store.Repo) bool {
			return func(repository store.Repo) bool {
				fixture.visibilityChecks++
				if fixture.revokeOnVisibilityCheck > 0 &&
					fixture.visibilityChecks >= fixture.revokeOnVisibilityCheck {
					return false
				}
				return fixture.visible[repository.Name]
			}
		},
		AuthorizationProvider: "exact-caller-test-permissions-v1",
	}
}

var _ callerexecute.PublicationReadStore = (*exactCallerAPIStore)(nil)

// Keep the test fake honest when store.ErrNotFound becomes wrapped by a real
// adapter: every absence classification must remain errors.Is-compatible.
func TestExactCallerAPIFixtureAbsenceErrorsAreTyped(t *testing.T) {
	fixture := newExactCallerAPIFixture(t, 1)
	fixture.store.publication = nil
	_, err := fixture.store.GetCallerGenerationPublication(
		t.Context(), fixture.repository,
	)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("publication absence = %v", err)
	}
}
