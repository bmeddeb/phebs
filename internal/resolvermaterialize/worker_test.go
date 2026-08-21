package resolvermaterialize

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"github.com/bmeddeb/phebs/internal/resolvercatalogid"
	"github.com/bmeddeb/phebs/internal/store"
	reposync "github.com/bmeddeb/phebs/internal/sync"
)

const workerTestCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type workerTestExtractor struct {
	domain  string
	version string
}

func (extractor workerTestExtractor) Domain() string  { return extractor.domain }
func (extractor workerTestExtractor) Version() string { return extractor.version }
func (workerTestExtractor) Candidate(string) bool     { return false }
func (workerTestExtractor) Extract(
	context.Context,
	sdk.Corpus,
	sdk.Emit,
) (sdk.Coverage, error) {
	return sdk.Coverage{}, nil
}

type workerTestManifest struct {
	identity   string
	files      []extract.CandidateManifestFile
	iterations int
}

func (manifest *workerTestManifest) Identity() string { return manifest.identity }
func (manifest *workerTestManifest) CorpusFileCount() int {
	return len(manifest.files)
}
func (*workerTestManifest) GitlinkBoundaries() extract.CandidateManifestGitlinks {
	return extract.CandidateManifestGitlinks{Digest: workerTestDigest('0')}
}
func (manifest *workerTestManifest) DomainScope(
	_, _ string,
) (extract.CandidateManifestScope, error) {
	return extract.CandidateManifestScope{
		ManifestDigest: manifest.identity,
	}, nil
}
func (*workerTestManifest) TypedInput(
	_, _, _ string,
) (extract.CandidateManifestTypedInput, error) {
	return extract.CandidateManifestTypedInput{}, nil
}
func (*workerTestManifest) PathInScope(string) bool { return false }
func (manifest *workerTestManifest) ForEachRepositoryFile(
	ctx context.Context,
	_, _ string,
	visit func(extract.CandidateManifestFile) error,
) error {
	manifest.iterations++
	for _, file := range manifest.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(file); err != nil {
			return err
		}
	}
	return nil
}

type workerTestManifestProvider struct {
	pointer          extract.CandidateManifestPointerIdentity
	manifest         extract.CandidateManifest
	generationErr    error
	openErr          error
	generationCalls  int
	openCalls        int
	generationInputs []extract.CandidateManifestRequest
	openInputs       []extract.CandidateManifestRequest
}

func (provider *workerTestManifestProvider) CandidateManifestGeneration(
	_ context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateManifestPointerIdentity, error) {
	provider.generationCalls++
	provider.generationInputs = append(provider.generationInputs, request)
	if provider.generationErr != nil {
		return extract.CandidateManifestPointerIdentity{}, provider.generationErr
	}
	return provider.pointer, nil
}

func (provider *workerTestManifestProvider) OpenCandidateManifest(
	_ context.Context,
	request extract.CandidateManifestRequest,
) (extract.CandidateManifest, error) {
	provider.openCalls++
	provider.openInputs = append(provider.openInputs, request)
	if provider.openErr != nil {
		return nil, provider.openErr
	}
	return provider.manifest, nil
}

type workerTestStore struct {
	repository  *store.Repo
	pointer     *store.ResolverCatalogPublication
	outcomes    map[string]*store.ExtractionDomainOutcome
	partitioned map[string]*store.PartitionedExtractionDomain
	assertions  []store.Assertion

	getRepoErr       error
	pointerErr       error
	outcomeErr       error
	assertionErr     error
	enqueueErr       error
	enqueueCommitErr error
	publishErr       error
	clearErr         error
	authorityErr     error
	authorityCurrent bool
	publishHook      func(context.Context, store.ResolverCatalogPublication) error

	repoCalls      int
	pointerCalls   int
	outcomeCalls   int
	assertionCalls int
	enqueueCalls   int
	publishCalls   int
	clearCalls     int
	authorityCalls int
	published      []store.ResolverCatalogPublication
	cleared        []string
	outcomeScopes  []store.ExtractionScope
	queries        []store.AssertionQuery
	enqueued       []store.Job
	events         []string
}

func (state *workerTestStore) GetRepo(
	context.Context,
	string,
) (*store.Repo, error) {
	state.repoCalls++
	if state.getRepoErr != nil {
		return nil, state.getRepoErr
	}
	if state.repository == nil {
		return nil, store.ErrNotFound
	}
	result := *state.repository
	return &result, nil
}

func (state *workerTestStore) GetResolverCatalogPublication(
	context.Context,
	string,
) (*store.ResolverCatalogPublication, error) {
	state.pointerCalls++
	if state.pointerErr != nil {
		return nil, state.pointerErr
	}
	if state.pointer == nil {
		return nil, store.ErrNotFound
	}
	result := cloneWorkerTestPublication(*state.pointer)
	return &result, nil
}

func (state *workerTestStore) PublishResolverCatalog(
	ctx context.Context,
	publication store.ResolverCatalogPublication,
) error {
	state.publishCalls++
	publication = cloneWorkerTestPublication(publication)
	state.published = append(state.published, publication)
	if state.publishHook != nil {
		if err := state.publishHook(ctx, publication); err != nil {
			return err
		}
	}
	if state.publishErr != nil {
		return state.publishErr
	}
	state.pointer = &publication
	return nil
}

func (state *workerTestStore) EnsureJobSuccessor(
	_ context.Context,
	active store.Job,
	force bool,
) (*store.Job, error) {
	state.enqueueCalls++
	state.events = append(state.events, "enqueue")
	if state.enqueueErr != nil {
		return nil, state.enqueueErr
	}
	job := store.Job{
		Kind: active.Kind, Target: active.Target,
		Force: force, Status: store.StatusPending,
	}
	state.enqueued = append(state.enqueued, job)
	if state.enqueueCommitErr != nil {
		return &job, state.enqueueCommitErr
	}
	return &job, nil
}

func (state *workerTestStore) ClearResolverCatalogPublication(
	_ context.Context,
	repository string,
) error {
	state.clearCalls++
	state.events = append(state.events, "clear")
	state.cleared = append(state.cleared, repository)
	if state.clearErr != nil {
		return state.clearErr
	}
	state.pointer = nil
	return nil
}

func (state *workerTestStore) ResolverCatalogPublicationCurrent(
	context.Context,
	store.ResolverCatalogPublication,
) (bool, error) {
	state.authorityCalls++
	if state.authorityErr != nil {
		return false, state.authorityErr
	}
	return state.authorityCurrent, nil
}

func (state *workerTestStore) LatestExtractionDomainOutcome(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	state.outcomeCalls++
	state.outcomeScopes = append(state.outcomeScopes, scope)
	if state.outcomeErr != nil {
		return nil, state.outcomeErr
	}
	outcome := state.outcomes[scope.Domain]
	if outcome == nil {
		return nil, store.ErrNotFound
	}
	result := *outcome
	return &result, nil
}

func (state *workerTestStore) GetPartitionedExtractionDomain(
	_ context.Context,
	_ string,
	domain string,
) (*store.PartitionedExtractionDomain, error) {
	publication := state.partitioned[domain]
	if publication == nil {
		return nil, store.ErrNotFound
	}
	result := *publication
	return &result, nil
}

func (state *workerTestStore) ListAssertions(
	_ context.Context,
	query store.AssertionQuery,
) ([]store.Assertion, error) {
	state.assertionCalls++
	state.queries = append(state.queries, query)
	if state.assertionErr != nil {
		return nil, state.assertionErr
	}
	return slices.Clone(state.assertions), nil
}

type workerFixture struct {
	t          *testing.T
	dataDir    string
	repository string
	digest     string
	registry   *Registry
	manifest   *workerTestManifest
	provider   *workerTestManifestProvider
	state      *workerTestStore
	worker     *Worker
	blobReads  int
	unexpected error
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	registry := workerTestRegistry(t)
	digest := workerTestDigest('1')
	manifest := &workerTestManifest{identity: digest}
	provider := &workerTestManifestProvider{
		pointer: extract.CandidateManifestPointerIdentity{
			ManifestDigest: digest, PolicyDigest: workerTestDigest('2'),
			ControlRevision: 1,
		},
		manifest: manifest,
	}
	repository := "github.com/acme/resolver-worker"
	state := &workerTestStore{
		repository: &store.Repo{
			Name: repository, IndexedCommitHash: workerTestCommit,
		},
		outcomes:         make(map[string]*store.ExtractionDomainOutcome),
		authorityCurrent: true,
	}
	dataDir := t.TempDir()
	worker, err := NewWorker(dataDir, state, provider, registry)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &workerFixture{
		t: t, dataDir: dataDir, repository: repository, digest: digest,
		registry: registry, manifest: manifest, provider: provider,
		state: state, worker: worker,
		unexpected: errors.New("unexpected resolver input blob read"),
	}
	worker.readBlob = func(
		context.Context, string, string, int64,
	) ([]byte, error) {
		fixture.blobReads++
		return nil, fixture.unexpected
	}
	return fixture
}

func (fixture *workerFixture) publishDeclaration(candidateDigest string) {
	fixture.t.Helper()
	generation := store.ExtractionGenerationIdentity{
		CandidateManifestDigest:  candidateDigest,
		CandidatePolicyDigest:    fixture.provider.pointer.PolicyDigest,
		CandidateControlRevision: fixture.provider.pointer.ControlRevision,
		Extractor:                "1.0.0",
		InventoryPolicy:          "candidate-manifest-v4-worker-test",
		DependencyDigest:         workerTestDigest('3'),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	fixture.state.outcomes["proto-contract"] = &store.ExtractionDomainOutcome{
		Scope: store.ExtractionScope{
			Repository: fixture.repository, Commit: workerTestCommit,
			Domain: "proto-contract",
		},
		Disposition: store.DomainOutcomePublished,
		Generation:  generation,
		RunID:       "run-proto-1",
	}
}

func (fixture *workerFixture) identity() resolvercatalog.Identity {
	fixture.t.Helper()
	outcome := fixture.state.outcomes["proto-contract"]
	if outcome == nil {
		fixture.t.Fatal("resolver identity requires a declaration outcome")
	}
	identity, err := resolvercatalog.NewIdentity(
		fixture.repository, workerTestCommit, "", fixture.digest,
		[]resolvercatalog.DeclarationPublication{{
			Domain: "proto-contract", RunID: outcome.RunID,
			GenerationDigest: store.ComputeExtractionGenerationDigest(
				outcome.Generation,
			),
		}},
		fixture.registry.Packs(),
	)
	if err != nil {
		fixture.t.Fatal(err)
	}
	return identity
}

func (fixture *workerFixture) handle(force bool) error {
	fixture.t.Helper()
	return fixture.worker.Handle(fixture.t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository, Force: force,
	})
}

func workerTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry([]extract.Extractor{
		workerTestExtractor{domain: "proto-contract", version: "1.0.0"},
		workerTestExtractor{domain: "grpc-caller", version: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func workerTestDigest(fill byte) string {
	return "sha256:" + strings.Repeat(string(fill), 64)
}

func cloneWorkerTestPublication(
	publication store.ResolverCatalogPublication,
) store.ResolverCatalogPublication {
	publication.Declarations = slices.Clone(publication.Declarations)
	publication.ResolverPacks = slices.Clone(publication.ResolverPacks)
	return publication
}

func workerTestPriorPointer(
	t *testing.T,
	repository string,
	registry *Registry,
) store.ResolverCatalogPublication {
	t.Helper()
	identity, err := resolvercatalog.NewIdentity(
		repository, workerTestCommit, "", workerTestDigest('9'), nil,
		registry.Packs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return store.ResolverCatalogPublication{
		Repository: repository, HeadCommit: workerTestCommit,
		Declarations:            []store.ResolverCatalogDeclarationPublication{},
		DeclarationSetDigest:    identity.DeclarationSetDigest,
		CandidateManifestDigest: identity.CandidateManifestDigest,
		SourceLanePolicy:        identity.SourceLanePolicy,
		ResolverPacks: func() []store.ResolverCatalogPack {
			result := make([]store.ResolverCatalogPack, len(identity.ResolverPacks))
			for index, pack := range identity.ResolverPacks {
				result[index] = store.ResolverCatalogPack{
					Name: pack.Name, Version: pack.Version,
				}
			}
			return result
		}(),
		ResolverPackSetDigest: identity.ResolverPackSetDigest,
		CatalogPolicyDigest:   identity.CatalogPolicyDigest,
		GenerationDigest:      identity.GenerationDigest,
		ManifestDigest:        workerTestDigest('8'),
		AuthorityDigest:       workerTestDigest('7'),
		ManifestPath:          resolvercatalogid.ManifestName(repository),
	}
}

func TestRegistryOrdersIdentitiesAndRequiresDeclarationDependency(t *testing.T) {
	registry, err := NewRegistry([]extract.Extractor{
		workerTestExtractor{domain: "proto-contract", version: "2.0.0"},
		workerTestExtractor{domain: "grpc-caller", version: "1.5.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := registry.CandidateDomains(), []extract.CandidateManifestDomain{
		{Domain: "grpc-caller", Version: "1.5.0"},
		{Domain: "proto-contract", Version: "2.0.0"},
	}; !slices.Equal(got, want) {
		t.Fatalf("candidate identities = %+v; want %+v", got, want)
	}
	if got, want := registry.Packs(), []resolvercatalog.ResolverPack{
		{Name: GoModulePackName, Version: PackVersion},
		{Name: GRPCGeneratedPackName, Version: PackVersion},
	}; !slices.Equal(got, want) {
		t.Fatalf("pack identities = %+v; want %+v", got, want)
	}
	if len(registry.adapters) != 1 ||
		registry.adapters[0].callerDomain != "grpc-caller" ||
		registry.adapters[0].callerVersion != "1.5.0" ||
		registry.adapters[0].declarationDomain != "proto-contract" {
		t.Fatalf("adapter identity = %+v", registry.adapters)
	}

	_, err = NewRegistry([]extract.Extractor{
		workerTestExtractor{domain: "grpc-caller", version: "1.5.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "requires declaration domain") {
		t.Fatalf("missing declaration dependency = %v", err)
	}
}

func TestWorkerCandidateBeforeDeclarationDefers(t *testing.T) {
	fixture := newWorkerFixture(t)
	if err := fixture.handle(false); !store.IsDeferral(err) {
		t.Fatalf("candidate-first result = %v, want dependency deferral", err)
	}
	if fixture.provider.generationCalls != 1 || fixture.state.outcomeCalls != 1 {
		t.Fatalf(
			"preflight calls: generation=%d outcome=%d",
			fixture.provider.generationCalls, fixture.state.outcomeCalls,
		)
	}
	if fixture.provider.openCalls != 0 || fixture.blobReads != 0 ||
		fixture.state.pointerCalls != 0 || fixture.state.publishCalls != 0 ||
		fixture.state.clearCalls != 0 {
		t.Fatalf(
			"no-op performed strict work: opens=%d blobs=%d pointer=%d publish=%d clear=%d",
			fixture.provider.openCalls, fixture.blobReads,
			fixture.state.pointerCalls, fixture.state.publishCalls,
			fixture.state.clearCalls,
		)
	}
	if _, err := os.Lstat(fixture.worker.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate-first no-op created catalog root: %v", err)
	}
	// The worker leaves queue state to the runner's lease-fenced delayed
	// transition; no crash-recovery successor is created in the handler.
	if fixture.state.enqueueCalls != 0 {
		t.Fatalf("deferred worker successor calls = %d, want 0", fixture.state.enqueueCalls)
	}
}

func TestWorkerClassifiesCandidatePublicationReadiness(t *testing.T) {
	for _, test := range []struct {
		name          string
		generationErr error
		wantDeferral  bool
	}{
		{name: "missing", generationErr: store.ErrNotFound, wantDeferral: true},
		{name: "publishing", generationErr: candidate.ErrPublishing, wantDeferral: true},
		{name: "stale", generationErr: extract.ErrCandidateManifestStale, wantDeferral: true},
		{name: "malformed", generationErr: candidate.ErrInvalidManifest},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			fixture.provider.generationErr = test.generationErr
			err := fixture.handle(false)
			if test.wantDeferral != store.IsDeferral(err) {
				t.Fatalf("candidate result = %v, want deferral %t", err, test.wantDeferral)
			}
			if !test.wantDeferral && !errors.Is(err, test.generationErr) {
				t.Fatalf("candidate result = %v, want %v", err, test.generationErr)
			}
			if fixture.state.enqueueCalls != 0 || fixture.provider.openCalls != 0 {
				t.Fatalf(
					"candidate readiness work = successor %d opens %d, want none",
					fixture.state.enqueueCalls, fixture.provider.openCalls,
				)
			}
		})
	}
}

func TestWorkerInactiveRepositoryCompletesWithoutDeferral(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*store.Repo)
	}{
		{name: "deleting", configure: func(repo *store.Repo) { repo.Deleting = true }},
		{name: "unindexed", configure: func(repo *store.Repo) { repo.IndexedCommitHash = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			test.configure(fixture.state.repository)
			if err := fixture.handle(false); err != nil {
				t.Fatalf("inactive repository result = %v, want successful retirement", err)
			}
			if fixture.provider.generationCalls != 0 || fixture.state.enqueueCalls != 0 {
				t.Fatalf(
					"inactive work = generation %d successor %d, want none",
					fixture.provider.generationCalls, fixture.state.enqueueCalls,
				)
			}
		})
	}
}

func TestWorkerSettledEmptyDeclarationPublishesEmptyAuthority(t *testing.T) {
	fixture := newWorkerFixture(t)
	generation := store.ExtractionGenerationIdentity{
		CandidateManifestDigest:  fixture.digest,
		CandidatePolicyDigest:    fixture.provider.pointer.PolicyDigest,
		CandidateControlRevision: fixture.provider.pointer.ControlRevision,
		Extractor:                "1.0.0",
		InventoryPolicy:          "candidate-manifest-v4-worker-test",
		DependencyDigest:         workerTestDigest('4'),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	fixture.state.outcomes["proto-contract"] = &store.ExtractionDomainOutcome{
		Scope: store.ExtractionScope{
			Repository: fixture.repository, Commit: workerTestCommit,
			Domain: "proto-contract",
		},
		Disposition: store.DomainOutcomeUnavailablePrerequisite,
		Generation:  generation,
	}
	published := 0
	fixture.worker.OnPublished = func(
		_ context.Context, repository string,
	) error {
		if repository != fixture.repository {
			fixture.t.Fatalf("published repository = %q", repository)
		}
		published++
		return nil
	}

	if err := fixture.handle(false); err != nil {
		t.Fatal(err)
	}
	if fixture.state.pointer == nil || fixture.state.publishCalls != 1 || published != 1 {
		t.Fatalf(
			"empty authority: pointer=%+v publish calls=%d callback=%d",
			fixture.state.pointer, fixture.state.publishCalls, published,
		)
	}
	if len(fixture.state.pointer.Declarations) != 0 {
		t.Fatalf("empty authority declarations = %+v", fixture.state.pointer.Declarations)
	}
	publication, err := resolvercatalog.Open(
		t.Context(), fixture.worker.root, StateFromStore(*fixture.state.pointer),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(publication.Manifest().Members); got != 2 {
		t.Fatalf("empty authority members = %d, want module plus grpc", got)
	}
}

func TestWorkerFirstPublish(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	if err := fixture.handle(false); err != nil {
		t.Fatal(err)
	}
	if fixture.state.pointer == nil || fixture.state.publishCalls != 1 {
		t.Fatalf(
			"publication pointer = %+v, calls=%d",
			fixture.state.pointer, fixture.state.publishCalls,
		)
	}
	if fixture.provider.openCalls != 1 || fixture.blobReads != 0 {
		t.Fatalf(
			"materialization work: strict opens=%d blob reads=%d",
			fixture.provider.openCalls, fixture.blobReads,
		)
	}
	if resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("successful publication left its marker")
	}
	publication, err := resolvercatalog.Open(
		fixture.t.Context(), fixture.worker.root,
		StateFromStore(*fixture.state.pointer),
	)
	if err != nil {
		t.Fatalf("open first publication: %v", err)
	}
	if got := len(publication.Manifest().Members); got != 2 {
		t.Fatalf("published members = %d; want module plus grpc", got)
	}
	if len(fixture.provider.openInputs) != 1 || !slices.Equal(
		fixture.provider.openInputs[0].Domains,
		fixture.registry.CandidateDomains(),
	) {
		t.Fatalf("strict candidate request = %+v", fixture.provider.openInputs)
	}
}

func TestWorkerFinalAttemptQueuesSuccessorBeforePublicationMarker(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	commitFailure := errors.New("resolver catalog store commit unavailable")
	fixture.state.publishErr = commitFailure

	err := fixture.worker.Handle(t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository,
		Force: true, Attempts: 2,
	})
	if !errors.Is(err, commitFailure) || !store.IsSuccessorRetry(err) {
		t.Fatalf("Handle = %v, want successor-marked commit failure", err)
	}
	if len(fixture.state.enqueued) != 1 ||
		fixture.state.enqueued[0].Kind != store.JobResolverCatalog ||
		fixture.state.enqueued[0].Target != fixture.repository ||
		fixture.state.enqueued[0].Force {
		t.Fatalf("publication successor = %+v, want one non-forced pending job",
			fixture.state.enqueued)
	}
	if !resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("post-install commit failure did not retain its marker")
	}
	if fixture.state.pointer != nil {
		t.Fatalf("failed store commit published pointer %+v", fixture.state.pointer)
	}

	fixture.state.publishErr = nil
	if err := fixture.handle(false); err != nil {
		t.Fatalf("successor marker recovery: %v", err)
	}
	if fixture.state.pointer == nil ||
		resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatalf(
			"successor did not recover marker: pointer=%+v marker=%v",
			fixture.state.pointer,
			resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository),
		)
	}
}

func TestWorkerWarmCachedNoOpSkipsStrictInputs(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	if err := fixture.handle(false); err != nil {
		t.Fatal(err)
	}
	baselineOpens := fixture.provider.openCalls
	baselinePublishes := fixture.state.publishCalls
	baselineAssertions := fixture.state.assertionCalls
	fixture.provider.openErr = errors.New("warm path opened strict candidate")
	fixture.state.assertionErr = errors.New("warm path read declaration assertions")

	if err := fixture.handle(false); err != nil {
		t.Fatalf("warm cached no-op: %v", err)
	}
	if fixture.provider.openCalls != baselineOpens || fixture.blobReads != 0 ||
		fixture.state.publishCalls != baselinePublishes ||
		fixture.state.assertionCalls != baselineAssertions {
		t.Fatalf(
			"warm work changed: opens=%d/%d blobs=%d publishes=%d/%d assertions=%d/%d",
			fixture.provider.openCalls, baselineOpens, fixture.blobReads,
			fixture.state.publishCalls, baselinePublishes,
			fixture.state.assertionCalls, baselineAssertions,
		)
	}
	if fixture.state.authorityCalls != 1 || fixture.worker.cached(fixture.repository) == nil {
		t.Fatalf(
			"warm authority/cache = %d, %v",
			fixture.state.authorityCalls,
			fixture.worker.cached(fixture.repository),
		)
	}
}

func TestWorkerForceRebuildsCurrentPublication(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	if err := fixture.handle(false); err != nil {
		t.Fatal(err)
	}
	if err := fixture.handle(true); err != nil {
		t.Fatalf("forced rebuild: %v", err)
	}
	if fixture.provider.openCalls != 2 || fixture.state.publishCalls != 2 ||
		fixture.blobReads != 0 {
		t.Fatalf(
			"forced work: opens=%d publishes=%d blobs=%d",
			fixture.provider.openCalls, fixture.state.publishCalls,
			fixture.blobReads,
		)
	}
	if fixture.state.authorityCalls != 0 {
		t.Fatalf("force consulted current authority %d times", fixture.state.authorityCalls)
	}
	if resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("forced rebuild left its marker")
	}
}

func TestWorkerRecoversMarkedPublicationWhileHoldingRepositoryLock(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	if err := os.MkdirAll(fixture.worker.root, 0o700); err != nil {
		t.Fatal(err)
	}
	prepared, err := Build(t.Context(), BuildRequest{
		Root:          fixture.worker.root,
		RepositoryDir: reposync.RepoDir(fixture.dataDir, fixture.repository),
		Identity:      fixture.identity(), Registry: fixture.registry,
		Manifest: fixture.manifest,
		Declarations: []DeclarationInput{{
			Protocol: ProtocolGRPC, Domain: "proto-contract",
			RunID: "run-proto-1",
			GenerationDigest: store.ComputeExtractionGenerationDigest(
				fixture.state.outcomes["proto-contract"].Generation,
			),
		}},
		Assertions: fixture.state, ReadBlob: fixture.worker.readBlob,
	})
	if err != nil {
		t.Fatal(err)
	}
	markedState, err := prepared.Install(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("fixture publication is not marked")
	}
	fixture.state.assertionCalls = 0
	fixture.blobReads = 0
	lockObserved := false
	fixture.state.publishHook = func(
		_ context.Context,
		_ store.ResolverCatalogPublication,
	) error {
		probeCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		unlock, lockErr := repowork.LockContext(
			probeCtx, reposync.RepoDir(fixture.dataDir, fixture.repository),
		)
		if lockErr == nil {
			unlock()
			return errors.New("marked recovery store commit ran without repository lock")
		}
		if !errors.Is(lockErr, context.DeadlineExceeded) {
			return lockErr
		}
		lockObserved = true
		return nil
	}

	if err := fixture.handle(false); err != nil {
		t.Fatal(err)
	}
	if !lockObserved || fixture.state.publishCalls != 1 ||
		fixture.state.pointer == nil {
		t.Fatalf(
			"marked recovery: lock=%v publishes=%d pointer=%+v",
			lockObserved, fixture.state.publishCalls, fixture.state.pointer,
		)
	}
	if got := StateFromStore(*fixture.state.pointer); !reflect.DeepEqual(got, markedState) {
		t.Fatalf("recovered state = %+v; want %+v", got, markedState)
	}
	if resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("recovered marker remains")
	}
	if fixture.provider.openCalls != 0 || fixture.blobReads != 0 ||
		fixture.state.clearCalls != 0 {
		t.Fatalf(
			"recovery rebuilt inputs: opens=%d blobs=%d clears=%d",
			fixture.provider.openCalls, fixture.blobReads, fixture.state.clearCalls,
		)
	}
}

func TestWorkerNondeterministicMarkedPublicationCannotBypassConflict(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	if err := fixture.handle(false); err != nil {
		t.Fatal(err)
	}
	prior := cloneWorkerTestPublication(*fixture.state.pointer)
	fixture.state.assertions = []store.Assertion{{
		ID: "assertion-1", Predicate: "DECLARES_OPERATION",
		Subject: "idl/api.proto", Object: "Service.Operation",
		Lineage: "lineage-1", Tier: "exact", Repo: fixture.repository,
		RunID: "run-proto-1",
	}}
	fixture.state.publishHook = func(
		_ context.Context,
		publication store.ResolverCatalogPublication,
	) error {
		if publication.GenerationDigest == prior.GenerationDigest &&
			publication.ManifestDigest != prior.ManifestDigest {
			return store.ErrConflict
		}
		return nil
	}

	for attempt := 1; attempt <= 2; attempt++ {
		err := fixture.handle(true)
		if !errors.Is(err, resolvercatalog.ErrNondeterministicPublication) ||
			!errors.Is(err, store.ErrConflict) || !store.IsTerminal(err) ||
			!store.IsSuccessorRetry(err) {
			t.Fatalf(
				"attempt %d error = %v; want successor-marked terminal nondeterminism/store conflict",
				attempt, err,
			)
		}
		if fixture.state.pointer == nil ||
			!reflect.DeepEqual(*fixture.state.pointer, prior) {
			t.Fatalf(
				"attempt %d changed prior pointer: got=%+v want=%+v",
				attempt, fixture.state.pointer, prior,
			)
		}
		if fixture.state.clearCalls != 0 {
			t.Fatalf("attempt %d cleared current authority", attempt)
		}
		if !resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
			t.Fatalf("attempt %d removed fail-closed marker", attempt)
		}
	}
	if fixture.state.publishCalls != 3 {
		t.Fatalf("publish calls = %d, want initial plus two rejected manifests",
			fixture.state.publishCalls)
	}
	if fixture.provider.openCalls != 2 {
		t.Fatalf("strict candidate opens = %d, want initial and first challenger only",
			fixture.provider.openCalls)
	}
}

func TestWorkerFinalAttemptQueuesRecoveryBeforeClearingDamagedMarker(t *testing.T) {
	fixture := newWorkerFixture(t)
	prior := workerTestPriorPointer(
		t, fixture.repository, fixture.registry,
	)
	fixture.state.pointer = &prior
	if err := os.MkdirAll(fixture.worker.root, 0o700); err != nil {
		t.Fatal(err)
	}
	targetMarker := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.PublishingName(fixture.repository),
	)
	if err := os.WriteFile(
		targetMarker, []byte(fixture.repository+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	targetArtifact := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.OwnedArtifactPrefix(fixture.repository)+"damaged",
	)
	if err := os.WriteFile(targetArtifact, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignRepository := "github.com/acme/foreign-resolver-worker"
	foreignMarker := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.PublishingName(foreignRepository),
	)
	foreignArtifact := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.OwnedArtifactPrefix(foreignRepository)+"keep",
	)
	if err := os.WriteFile(
		foreignMarker, []byte(foreignRepository+"\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignArtifact, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fixture.worker.Handle(t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository,
		Force: true, Attempts: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if fixture.state.clearCalls != 1 || fixture.state.pointer != nil {
		t.Fatalf(
			"damaged authority: clears=%d pointer=%+v",
			fixture.state.clearCalls, fixture.state.pointer,
		)
	}
	for _, removed := range []string{targetMarker, targetArtifact} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("target artifact %q remains: %v", removed, err)
		}
	}
	for _, retained := range []string{foreignMarker, foreignArtifact} {
		if _, err := os.Lstat(retained); err != nil {
			t.Fatalf("foreign artifact %q was touched: %v", retained, err)
		}
	}
	if fixture.provider.openCalls != 0 || fixture.state.publishCalls != 0 {
		t.Fatalf(
			"damaged-marker successor did strict work: opens=%d publishes=%d",
			fixture.provider.openCalls, fixture.state.publishCalls,
		)
	}
	if len(fixture.state.enqueued) != 1 ||
		fixture.state.enqueued[0].Kind != store.JobResolverCatalog ||
		fixture.state.enqueued[0].Target != fixture.repository ||
		!fixture.state.enqueued[0].Force {
		t.Fatalf("recovery successor = %+v, want one forced pending job",
			fixture.state.enqueued)
	}
	if !slices.Equal(fixture.state.events, []string{"enqueue", "clear"}) {
		t.Fatalf("recovery ordering = %v, want enqueue before clear", fixture.state.events)
	}
}

func TestWorkerRecoveryEnqueueFailurePreservesDamagedAuthority(t *testing.T) {
	fixture := newWorkerFixture(t)
	prior := workerTestPriorPointer(t, fixture.repository, fixture.registry)
	fixture.state.pointer = &prior
	if err := os.MkdirAll(fixture.worker.root, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.PublishingName(fixture.repository),
	)
	if err := os.WriteFile(marker, []byte(fixture.repository+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.state.enqueueErr = errors.New("queue unavailable")
	err := fixture.worker.Handle(t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository,
		Force: true, Attempts: 2,
	})
	if !errors.Is(err, fixture.state.enqueueErr) || !store.IsSuccessorRetry(err) {
		t.Fatalf("Handle = %v, want marked queue error", err)
	}
	if fixture.state.clearCalls != 0 || fixture.state.pointer == nil ||
		!reflect.DeepEqual(*fixture.state.pointer, prior) {
		t.Fatalf("queue failure changed authority: clears=%d pointer=%+v",
			fixture.state.clearCalls, fixture.state.pointer)
	}
	if !resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("queue failure removed recovery marker")
	}
}

func TestWorkerMarksAmbiguousRecoverySuccessorCommit(t *testing.T) {
	fixture := newWorkerFixture(t)
	prior := workerTestPriorPointer(t, fixture.repository, fixture.registry)
	fixture.state.pointer = &prior
	if err := os.MkdirAll(fixture.worker.root, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.PublishingName(fixture.repository),
	)
	if err := os.WriteFile(marker, []byte(fixture.repository+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.state.enqueueCommitErr = errors.New("successor response unavailable")
	err := fixture.worker.Handle(t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository,
		Force: true, Attempts: 2,
	})
	if !errors.Is(err, fixture.state.enqueueCommitErr) ||
		!store.IsSuccessorRetry(err) {
		t.Fatalf("Handle = %v, want marked ambiguous successor error", err)
	}
	if len(fixture.state.enqueued) != 1 || !fixture.state.enqueued[0].Force ||
		fixture.state.clearCalls != 0 || fixture.state.pointer == nil ||
		!reflect.DeepEqual(*fixture.state.pointer, prior) {
		t.Fatalf(
			"ambiguous successor boundary: enqueued=%+v clears=%d pointer=%+v",
			fixture.state.enqueued, fixture.state.clearCalls, fixture.state.pointer,
		)
	}
	if !resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("ambiguous successor response removed recovery marker")
	}
}

func TestWorkerRecoveryClearFailureCarriesSuccessorProvenance(t *testing.T) {
	fixture := newWorkerFixture(t)
	prior := workerTestPriorPointer(t, fixture.repository, fixture.registry)
	fixture.state.pointer = &prior
	if err := os.MkdirAll(fixture.worker.root, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.PublishingName(fixture.repository),
	)
	if err := os.WriteFile(marker, []byte(fixture.repository+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.state.clearErr = errors.New("resolver pointer clear unavailable")
	err := fixture.worker.Handle(t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository,
		Force: true, Attempts: 2,
	})
	if !errors.Is(err, fixture.state.clearErr) || !store.IsSuccessorRetry(err) {
		t.Fatalf("Handle = %v, want successor-marked clear error", err)
	}
	if len(fixture.state.enqueued) != 1 || !fixture.state.enqueued[0].Force ||
		fixture.state.clearCalls != 1 || fixture.state.pointer == nil ||
		!reflect.DeepEqual(*fixture.state.pointer, prior) {
		t.Fatalf(
			"clear failure boundary: enqueued=%+v clears=%d pointer=%+v",
			fixture.state.enqueued, fixture.state.clearCalls, fixture.state.pointer,
		)
	}
	if !resolvercatalog.IsPublishing(fixture.worker.root, fixture.repository) {
		t.Fatal("clear failure removed recovery marker")
	}
}

func TestWorkerMarkerIOFailurePreservesAuthority(t *testing.T) {
	fixture := newWorkerFixture(t)
	prior := workerTestPriorPointer(t, fixture.repository, fixture.registry)
	fixture.state.pointer = &prior
	if err := os.WriteFile(fixture.worker.root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := fixture.handle(true)
	if !errors.Is(err, resolvercatalog.ErrCatalogIO) {
		t.Fatalf("Handle = %v, want ErrCatalogIO", err)
	}
	if fixture.state.enqueueCalls != 0 || fixture.state.clearCalls != 0 ||
		fixture.state.pointer == nil ||
		!reflect.DeepEqual(*fixture.state.pointer, prior) {
		t.Fatalf(
			"operational marker failure mutated authority: enqueues=%d clears=%d pointer=%+v",
			fixture.state.enqueueCalls, fixture.state.clearCalls, fixture.state.pointer,
		)
	}
	if fixture.state.repoCalls != 0 || fixture.provider.generationCalls != 0 {
		t.Fatalf(
			"operational marker failure continued into materialization: repos=%d generations=%d",
			fixture.state.repoCalls, fixture.provider.generationCalls,
		)
	}
}

func TestWorkerFinalAttemptQueuesRecoveryBeforeClearingInvalidPointer(t *testing.T) {
	fixture := newWorkerFixture(t)
	fixture.publishDeclaration(fixture.digest)
	prior := workerTestPriorPointer(t, fixture.repository, fixture.registry)
	fixture.state.pointer = &prior
	fixture.state.pointerErr = store.ErrInvalidResolverCatalogPublication
	if err := os.MkdirAll(fixture.worker.root, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(
		fixture.worker.root,
		resolvercatalogid.OwnedArtifactPrefix(fixture.repository)+"invalid-pointer",
	)
	if err := os.WriteFile(artifact, []byte("derived"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := fixture.worker.Handle(t.Context(), store.Job{
		Kind: store.JobResolverCatalog, Target: fixture.repository,
		Force: true, Attempts: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.state.enqueued) != 1 ||
		fixture.state.enqueued[0].Kind != store.JobResolverCatalog ||
		fixture.state.enqueued[0].Target != fixture.repository ||
		!fixture.state.enqueued[0].Force {
		t.Fatalf("recovery successor = %+v, want one forced pending job",
			fixture.state.enqueued)
	}
	if !slices.Equal(fixture.state.events, []string{"enqueue", "clear"}) {
		t.Fatalf("recovery ordering = %v, want enqueue before clear", fixture.state.events)
	}
	if fixture.state.pointer != nil || fixture.provider.openCalls != 0 {
		t.Fatalf(
			"invalid-pointer claim continued: pointer=%+v strict opens=%d",
			fixture.state.pointer, fixture.provider.openCalls,
		)
	}
	if _, err := os.Lstat(artifact); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid-pointer artifact survived recovery: %v", err)
	}
}

func TestWorkerStaleInputsAndStoreConflictPreservePriorAuthority(t *testing.T) {
	for _, test := range []struct {
		name         string
		configure    func(*workerFixture)
		wantErr      error
		wantMarker   bool
		wantDeferral bool
	}{
		{
			name: "stale candidate",
			configure: func(fixture *workerFixture) {
				fixture.provider.generationErr = candidate.ErrInvalidManifest
			},
			wantErr: candidate.ErrInvalidManifest,
		},
		{
			name: "stale declaration",
			configure: func(fixture *workerFixture) {
				fixture.publishDeclaration(workerTestDigest('7'))
			},
			wantDeferral: true,
		},
		{
			name: "stale declaration control revision",
			configure: func(fixture *workerFixture) {
				fixture.publishDeclaration(fixture.digest)
				fixture.provider.pointer.ControlRevision++
			},
			wantDeferral: true,
		},
		{
			name: "store conflict",
			configure: func(fixture *workerFixture) {
				fixture.publishDeclaration(fixture.digest)
				fixture.state.publishErr = store.ErrConflict
			},
			wantErr: store.ErrConflict, wantMarker: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkerFixture(t)
			prior := workerTestPriorPointer(
				t, fixture.repository, fixture.registry,
			)
			fixture.state.pointer = &prior
			test.configure(fixture)
			before := cloneWorkerTestPublication(*fixture.state.pointer)

			err := fixture.handle(false)
			if test.wantDeferral && !store.IsDeferral(err) {
				t.Fatalf("Handle error = %v; want dependency deferral", err)
			}
			if test.wantErr == nil && !test.wantDeferral && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("Handle error = %v; want %v", err, test.wantErr)
			}
			if fixture.state.pointer == nil || !reflect.DeepEqual(
				*fixture.state.pointer, before,
			) {
				t.Fatalf(
					"prior authority changed: before=%+v after=%+v",
					before, fixture.state.pointer,
				)
			}
			if fixture.state.clearCalls != 0 {
				t.Fatalf("prior authority cleared %d times", fixture.state.clearCalls)
			}
			if got := resolvercatalog.IsPublishing(
				fixture.worker.root, fixture.repository,
			); got != test.wantMarker {
				t.Fatalf("publication marker = %v; want %v", got, test.wantMarker)
			}
		})
	}
}

type workerBackfillStore struct {
	store.Store
	repositories []store.Repo
	enqueued     []store.Job
}

func (state *workerBackfillStore) ListRepos(
	context.Context,
) ([]store.Repo, error) {
	return slices.Clone(state.repositories), nil
}

func (state *workerBackfillStore) EnqueuePending(
	_ context.Context,
	kind store.JobKind,
	target string,
	force bool,
) (*store.Job, error) {
	job := store.Job{Kind: kind, Target: target, Force: force}
	state.enqueued = append(state.enqueued, job)
	return &job, nil
}

func TestPartitionedDeclarationTerminalRootsAreSettled(t *testing.T) {
	pointer := extract.CandidateManifestPointerIdentity{
		ManifestDigest: "sha256:" + strings.Repeat("a", 64),
		PolicyDigest:   "sha256:" + strings.Repeat("b", 64),
	}
	for _, disposition := range []string{
		candidate.PartitionResultSuccess,
		candidate.PartitionResultEmpty,
		candidate.PartitionResultUnavailablePrerequisite,
		candidate.PartitionResultTerminalRefusal,
		candidate.PartitionResultRetryable,
	} {
		authority := candidate.DownstreamDomainAuthority{
			Disposition:             disposition,
			CandidateManifestDigest: pointer.ManifestDigest,
			CandidatePolicyDigest:   pointer.PolicyDigest,
		}
		settled, include := partitionedDeclarationState(authority, pointer)
		if !settled || include != (disposition == candidate.PartitionResultSuccess) {
			t.Fatalf("disposition %q = settled %t include %t", disposition, settled, include)
		}
	}
	stale := candidate.DownstreamDomainAuthority{
		Disposition:             candidate.PartitionResultSuccess,
		CandidateManifestDigest: "sha256:" + strings.Repeat("c", 64),
		CandidatePolicyDigest:   pointer.PolicyDigest,
	}
	if settled, _ := partitionedDeclarationState(stale, pointer); settled {
		t.Fatal("stale partitioned declaration authority was settled")
	}
}

func TestEnqueueBackfillSkipsUnindexedAndDeletingRepositories(t *testing.T) {
	state := &workerBackfillStore{repositories: []store.Repo{
		{Name: "github.com/acme/live", IndexedCommitHash: workerTestCommit},
		{Name: "github.com/acme/unindexed"},
		{
			Name: "github.com/acme/deleting", IndexedCommitHash: workerTestCommit,
			Deleting: true,
		},
	}}
	if err := EnqueueBackfill(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	want := []store.Job{{
		Kind: store.JobResolverCatalog, Target: "github.com/acme/live",
		Force: false,
	}}
	if !reflect.DeepEqual(state.enqueued, want) {
		t.Fatalf("backfill jobs = %+v; want %+v", state.enqueued, want)
	}
}
