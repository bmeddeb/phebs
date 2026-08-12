package extractionpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/store"
)

type candidateFixture struct {
	repository          string
	repositoryDirectory string
	candidateDirectory  string
	publication         *candidate.Publication
}

func buildCandidateFixtureAt(
	t *testing.T,
	repository,
	repositoryDirectory,
	candidateDirectory string,
) candidateFixture {
	return buildCandidateFixtureAtWithTyped(
		t, repository, repositoryDirectory, candidateDirectory, false,
	)
}

func buildCandidateFixtureAtWithTyped(
	t *testing.T,
	repository,
	repositoryDirectory,
	candidateDirectory string,
	typed bool,
) candidateFixture {
	t.Helper()
	if err := os.MkdirAll(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repositoryDirectory, "init", "-q")
	runGit(t, repositoryDirectory, "config", "user.email", "partition@example.invalid")
	runGit(t, repositoryDirectory, "config", "user.name", "Partition Test")
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "schema.proto"),
		[]byte("syntax = \"proto3\";\nmessage Original {}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "README.md"), []byte("not selected\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	paths := []string{"schema.proto", "README.md"}
	if typed {
		if err := os.WriteFile(
			filepath.Join(repositoryDirectory, "index.scip"), []byte("typed-index"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, "index.scip")
	}
	runGit(t, repositoryDirectory, append([]string{"add", "--"}, paths...)...)
	runGit(t, repositoryDirectory, "commit", "-q", "-m", "fixture")
	commit := strings.TrimSpace(runGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	policy := candidate.Policy{
		Domain: "proto-contract", Version: "3.0.0",
		EnumerationPolicy: "proto-contract-paths-v1", Plane: candidate.PlaneRepository,
		Enumerate: func(path string) bool { return strings.HasSuffix(path, ".proto") },
		Required:  func(path string) bool { return strings.HasSuffix(path, ".proto") },
	}
	if typed {
		policy.TypedInputs = []string{"scip"}
	}
	policies := []candidate.Policy{policy}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := candidate.PolicyDigest(identities)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := candidate.Build(t.Context(), candidate.Request{
		RepoDir: repositoryDirectory, OutputDir: candidateDirectory,
		Repository: repository, Commit: commit, Policies: policies,
	})
	if err != nil {
		t.Fatal(err)
	}
	publication, err := candidate.Open(candidateDirectory, candidate.Expected{
		Repository: repository, Commit: commit, Policies: identities,
		PolicyDigest: policyDigest, GenerationDigest: manifest.GenerationDigest,
		ManifestDigest: manifest.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidateFixture{
		repository: repository, repositoryDirectory: repositoryDirectory,
		candidateDirectory: candidateDirectory, publication: publication,
	}
}

type testExtractionRunStore struct {
	mu         sync.Mutex
	begins     int
	aborts     int
	limits     []store.PartitionedExtractionRunLimits
	candidates []string
	published  map[string]store.PartitionedExtractionDomain
}

func (state *testExtractionRunStore) BeginPartitionedExtractionRun(
	_ context.Context,
	scope store.ExtractionScope,
	extractor,
	_ string,
	candidateDigest string,
	limits store.PartitionedExtractionRunLimits,
) (*store.ExtractionRun, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.begins++
	state.limits = append(state.limits, limits)
	state.candidates = append(state.candidates, candidateDigest)
	return &store.ExtractionRun{
		ID: "run-" + scope.Domain, Repo: scope.Repository, Commit: scope.Commit,
		UnitDigest: scope.UnitDigest, Domain: scope.Domain, Extractor: extractor,
		Status: "staged",
	}, nil
}

func (state *testExtractionRunStore) AbortExtractionRun(context.Context, string) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.aborts++
	return nil
}

func (state *testExtractionRunStore) GetPartitionedExtractionDomain(
	_ context.Context,
	_, domain string,
) (*store.PartitionedExtractionDomain, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	publication, ok := state.published[domain]
	if !ok {
		return nil, store.ErrNotFound
	}
	copy := publication
	return &copy, nil
}

func (state *testExtractionRunStore) counts() (int, int) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.begins, state.aborts
}

func TestReconcilerReservesBeforeContentAndReusesRunOnRestart(t *testing.T) {
	repository := "example.invalid/reconciler"
	fixture := buildCandidateFixtureAt(
		t, repository, filepath.Join(t.TempDir(), "repository"), t.TempDir(),
	)
	sourceDigest := "sha256:" + strings.Repeat("1", 64)
	observationDigest := "sha256:" + strings.Repeat("2", 64)
	testPlan := buildTestPlan(t, sourceDigest, true)
	runtime, schedules, source, _, _, _, _ := newRuntimeFixture(t, testPlan)
	runtime.Root = t.TempDir()
	evidence := &testExtractionRunStore{}
	var fullCandidateReads, fullAuthorityReads, transitionReferenceReads, transitionCandidateReads int
	transitionAuthority, transitionCandidate := false, false
	reconciler := &Reconciler{
		Root: runtime.Root, CandidateRoot: fixture.candidateDirectory,
		Runtime: runtime, Evidence: evidence,
		OpenCandidate: func(context.Context, string) (*candidate.Publication, error) {
			fullCandidateReads++
			return fixture.publication, nil
		},
		Authority: func(context.Context, string) (string, string, error) {
			fullAuthorityReads++
			return sourceDigest, observationDigest, nil
		},
		CandidateReference: func(context.Context, string) (candidate.State, error) {
			state := fixture.publication.State()
			if transitionCandidate {
				transitionCandidateReads++
				if transitionCandidateReads > 1 {
					state.ManifestDigest = "sha256:" + strings.Repeat("8", 64)
				}
			}
			return state, nil
		},
		AuthorityReference: func(context.Context, string) (string, string, error) {
			if transitionAuthority {
				transitionReferenceReads++
				if transitionReferenceReads > 1 {
					return sourceDigest, "sha256:" + strings.Repeat("9", 64), nil
				}
			}
			return sourceDigest, observationDigest, nil
		},
	}
	generation, err := reconciler.Reconcile(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if acquired, _ := source.counts(); acquired != 0 {
		t.Fatalf("planning acquired %d content leases", acquired)
	}
	schedule, err := schedules.GetGenerationSchedule(t.Context(), repository, ScheduleStage)
	if err != nil || schedule.ResourceClass != store.GenerationResourceExtraction ||
		schedule.TotalItems != 1 {
		t.Fatalf("partition schedule = %+v, %v", schedule, err)
	}
	if begins, aborts := evidence.counts(); begins != 1 || aborts != 0 {
		t.Fatalf("initial run lifecycle = %d begins, %d aborts", begins, aborts)
	}
	if fullCandidateReads != 1 || fullAuthorityReads != 1 {
		t.Fatalf("initial full validation reads = %d/%d", fullCandidateReads, fullAuthorityReads)
	}
	created, err := runtime.openGeneration(runtime.generationDirectory(repository, generation), repository, generation)
	if err != nil || len(created.Domains) != 1 {
		t.Fatalf("created generation = %+v, %v", created, err)
	}
	createdDomain, err := runtime.openDomainPlan(
		runtime.generationDirectory(repository, generation), created.Domains[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	wantLimits := store.PartitionedExtractionRunLimits{
		Facts: createdDomain.Plan.Reserved.Facts, Rows: createdDomain.Plan.Reserved.Rows,
		References: createdDomain.Plan.Reserved.References,
	}
	if len(evidence.limits) != 1 || evidence.limits[0] != wantLimits {
		t.Fatalf("partition run limits = %+v", evidence.limits)
	}
	if len(evidence.candidates) != 1 ||
		evidence.candidates[0] != createdDomain.Plan.CandidateManifestDigest {
		t.Fatalf("partition run candidate authority = %+v", evidence.candidates)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, schedules, repository, 0)); err != nil {
		t.Fatal(err)
	}
	schedules.settle(repository, 0)

	restarted := &Runtime{
		Root: runtime.Root, Store: schedules, Source: source,
		Executor: runtime.Executor, Publisher: runtime.Publisher, Fence: runtime.Fence,
	}
	reconciler.Runtime = restarted
	again, err := reconciler.Reconcile(t.Context(), repository)
	if err != nil || again != generation {
		t.Fatalf("restart reconcile = %q, %v, want %q", again, err, generation)
	}
	if begins, aborts := evidence.counts(); begins != 1 || aborts != 0 {
		t.Fatalf("restart run lifecycle = %d begins, %d aborts", begins, aborts)
	}
	if fullCandidateReads != 1 || fullAuthorityReads != 1 {
		t.Fatalf("hot restart repeated full validation = %d/%d", fullCandidateReads, fullAuthorityReads)
	}
	if acquired, released := source.counts(); acquired != 1 || released != 1 {
		t.Fatalf("hot restart content work = %d/%d", acquired, released)
	}
	transitionAuthority = true
	if _, err := reconciler.Reconcile(t.Context(), repository); !errors.Is(err, ErrStale) {
		t.Fatalf("authority transition during shallow reuse = %v, want stale", err)
	}
	if fullCandidateReads != 1 || fullAuthorityReads != 1 {
		t.Fatalf("stale shallow reuse entered full validation = %d/%d", fullCandidateReads, fullAuthorityReads)
	}
	transitionAuthority = false
	transitionCandidate = true
	if _, err := reconciler.Reconcile(t.Context(), repository); !errors.Is(err, ErrStale) {
		t.Fatalf("candidate transition during shallow reuse = %v, want stale", err)
	}
	if fullCandidateReads != 1 || fullAuthorityReads != 1 {
		t.Fatalf("candidate-stale shallow reuse entered full validation = %d/%d", fullCandidateReads, fullAuthorityReads)
	}
}

func TestReconcilerRestoresExcludedControlsFromCurrentStoreAuthority(t *testing.T) {
	repository := "example.invalid/restore-current"
	fixture := buildCandidateFixtureAt(
		t, repository, filepath.Join(t.TempDir(), "repository"), t.TempDir(),
	)
	sourceDigest := "sha256:" + strings.Repeat("3", 64)
	observationDigest := "sha256:" + strings.Repeat("4", 64)
	testPlan := buildTestPlan(t, sourceDigest, true)
	runtime, schedules, source, _, publisher, _, _ := newRuntimeFixture(t, testPlan)
	runtime.Root = t.TempDir()
	evidence := &testExtractionRunStore{}
	reconciler := &Reconciler{
		Root: runtime.Root, CandidateRoot: fixture.candidateDirectory,
		Runtime: runtime, Evidence: evidence,
		OpenCandidate: func(context.Context, string) (*candidate.Publication, error) {
			return fixture.publication, nil
		},
		Authority: func(context.Context, string) (string, string, error) {
			return sourceDigest, observationDigest, nil
		},
		CandidateReference: func(context.Context, string) (candidate.State, error) {
			return fixture.publication.State(), nil
		},
		AuthorityReference: func(context.Context, string) (string, string, error) {
			return sourceDigest, observationDigest, nil
		},
	}
	generation, err := reconciler.Reconcile(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Handle(t.Context(), currentChunk(t, schedules, repository, 0)); err != nil {
		t.Fatal(err)
	}
	schedules.settle(repository, 0)
	created, err := runtime.openGeneration(
		runtime.generationDirectory(repository, generation), repository, generation,
	)
	if err != nil || len(created.Domains) != 1 {
		t.Fatalf("created generation = %+v, %v", created, err)
	}
	domain, err := runtime.openDomainPlan(
		runtime.generationDirectory(repository, generation), created.Domains[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	root, err := runtime.Current(t.Context(), repository, domain.Plan.Domain)
	if err != nil {
		t.Fatal(err)
	}
	planRaw, err := canonical(domain.Plan)
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, err := canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	evidence.published = map[string]store.PartitionedExtractionDomain{
		domain.Plan.Domain: {
			Schema:     store.PartitionedExtractionDomainSchema,
			Repository: repository, Domain: domain.Plan.Domain, RunID: domain.RunID,
			PlanDigest: domain.Plan.Digest, RootDigest: root.Digest,
			CandidateDigest: domain.Plan.CandidateManifestDigest,
			SourceDigest:    sourceDigest, ObservationDigest: observationDigest,
			Facts: root.Totals.Facts, Rows: root.Totals.Rows,
			References: root.Totals.References,
			Plan:       string(planRaw), Root: string(rootRaw),
		},
	}
	if err := os.RemoveAll(runtime.Root); err != nil {
		t.Fatal(err)
	}
	restarted := &Runtime{
		Root: runtime.Root, Store: schedules, Source: source,
		Executor: runtime.Executor, Publisher: publisher, Fence: runtime.Fence,
	}
	reconciler.Runtime = restarted
	restored, err := reconciler.Reconcile(t.Context(), repository)
	if err != nil || restored != generation {
		t.Fatalf("restored generation = %q, %v, want %q", restored, err, generation)
	}
	if begins, aborts := evidence.counts(); begins != 1 || aborts != 0 {
		t.Fatalf("restore minted evidence runs = %d begins, %d aborts", begins, aborts)
	}
	if acquired, released := source.counts(); acquired != 1 || released != 1 {
		t.Fatalf("restore repeated source work = %d/%d", acquired, released)
	}
	status, err := restarted.Status(t.Context(), repository, generation)
	if err != nil || len(status.Domains) != 1 ||
		status.Domains[0].Settled != status.Domains[0].Expected ||
		!status.Domains[0].Current {
		t.Fatalf("restored status = %+v, %v", status, err)
	}
	restoredRoot, err := restarted.Current(t.Context(), repository, domain.Plan.Domain)
	if err != nil || restoredRoot.Digest != root.Digest {
		t.Fatalf("restored root = %+v, %v", restoredRoot, err)
	}
}

func TestAuthorityFenceHoldsLockUntilReleaseAndReleasesOnError(t *testing.T) {
	plan := buildTestPlan(t, "sha256:"+strings.Repeat("3", 64), true)
	schedules := &testScheduleStore{}
	var publicationLock sync.Mutex
	fence := AuthorityFence{
		Store: schedules,
		Acquire: func(context.Context) (func(), error) {
			publicationLock.Lock()
			return publicationLock.Unlock, nil
		},
		Current: func(context.Context, candidate.DomainResultPlan) error { return nil },
	}
	release, err := fence.FenceDomain(t.Context(), FenceRequest{Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if publicationLock.TryLock() {
		publicationLock.Unlock()
		t.Fatal("publication lock was released before caller transition")
	}
	release()
	if !publicationLock.TryLock() {
		t.Fatal("publication lock remained held after caller release")
	}
	publicationLock.Unlock()

	fence.Current = func(context.Context, candidate.DomainResultPlan) error { return ErrStale }
	if release, err := fence.FenceDomain(t.Context(), FenceRequest{Plan: plan}); release != nil || !errors.Is(err, ErrStale) {
		t.Fatalf("failed fence returned release=%t, error=%v", release != nil, err)
	}
	if !publicationLock.TryLock() {
		t.Fatal("failed authority check leaked publication lock")
	}
	publicationLock.Unlock()
}

func TestGitSparseSourceReadsOnlySelectedImmutableObjects(t *testing.T) {
	dataDirectory := t.TempDir()
	repository := "example.invalid/git-source"
	repositoryDirectory := filepath.Join(dataDirectory, "repos", "example.invalid", "git-source.git")
	fixture := buildCandidateFixtureAt(
		t, repository, repositoryDirectory, t.TempDir(),
	)
	sparseDirectory := t.TempDir()
	root, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, fixture.publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := fixture.publication.State()
	sparse, err := candidate.OpenSparse(
		t.Context(), sparseDirectory, fixture.candidateDirectory, state, root.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "proto-contract", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildReservedPlan(domain, candidate.DomainResultAuthority{
		SourceGenerationDigest:      "sha256:" + strings.Repeat("4", 64),
		ObservationGenerationDigest: "sha256:" + strings.Repeat("5", 64),
		ExtractorVersion:            "3.0.0",
		ExtractionPolicyDigest:      "sha256:" + strings.Repeat("6", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	source := GitSparseSource{
		DataDir: dataDirectory,
		OpenDomain: func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error) {
			return domain, nil
		},
	}
	lease, err := source.AcquirePartition(t.Context(), plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	var paths []string
	if err := lease.Corpus().WalkFiles(t.Context(), func(path string) error {
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "schema.proto" {
		t.Fatalf("selected paths = %v", paths)
	}
	if _, err := lease.Corpus().Read(t.Context(), "README.md"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unselected read error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "schema.proto"), []byte("changed worktree\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	blob, err := lease.Corpus().Read(t.Context(), "schema.proto")
	if err != nil {
		t.Fatal(err)
	}
	if blob.Content != "syntax = \"proto3\";\nmessage Original {}\n" {
		t.Fatalf("selected immutable content = %q", blob.Content)
	}
}

func TestGitTypedPartitionReadsIndexAndAdmittedDocumentsOnce(t *testing.T) {
	dataDirectory := t.TempDir()
	repository := "example.invalid/git-typed-source"
	repositoryDirectory := filepath.Join(dataDirectory, "repos", "example.invalid", "git-typed-source.git")
	fixture := buildCandidateFixtureAtWithTyped(
		t, repository, repositoryDirectory, t.TempDir(), true,
	)
	sparseDirectory := t.TempDir()
	root, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, fixture.publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	var reads candidate.SparseReadMetrics
	sparse, err := candidate.OpenSparse(
		t.Context(), sparseDirectory, fixture.candidateDirectory,
		fixture.publication.State(), root.Digest, &reads,
	)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sparse.OpenDomain(t.Context(), "proto-contract", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildReservedPlan(domain, candidate.DomainResultAuthority{
		SourceGenerationDigest:      "sha256:" + strings.Repeat("4", 64),
		ObservationGenerationDigest: "sha256:" + strings.Repeat("5", 64),
		ExtractorVersion:            "3.0.0",
		ExtractionPolicyDigest:      "sha256:" + strings.Repeat("6", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	typedOrdinal := -1
	for ordinal, partition := range domain.Partitions() {
		if partition.Kind == candidate.PartitionKindTypedInput {
			typedOrdinal = ordinal
		}
	}
	if typedOrdinal < 0 {
		t.Fatal("typed partition was not scheduled")
	}
	source := GitSparseSource{
		DataDir: dataDirectory,
		OpenDomain: func(context.Context, candidate.DomainResultPlan) (*candidate.SparseDomain, error) {
			return domain, nil
		},
	}
	lease, err := source.AcquirePartition(t.Context(), plan, typedOrdinal)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if reads.TypedScopeReads != 1 || reads.CandidateMemberReads != 0 {
		t.Fatalf("typed source control reads = %+v", reads)
	}
	var paths []string
	if err := lease.Corpus().WalkFiles(t.Context(), func(path string) error {
		paths = append(paths, path)
		return nil
	}); err != nil || len(paths) != 1 || paths[0] != "schema.proto" {
		t.Fatalf("typed admitted paths = %v, %v", paths, err)
	}
	partitioned, ok := lease.Corpus().(sdk.SCIPTypedPartition)
	if !ok || !partitioned.SCIPTypedPartition() || lease.TypedSourceRecords() != 1 {
		t.Fatal("typed source did not expose its partition scope")
	}
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "schema.proto"), []byte("changed worktree\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	document, err := lease.Corpus().Read(t.Context(), "schema.proto")
	if err != nil || document.Content != "syntax = \"proto3\";\nmessage Original {}\n" {
		t.Fatalf("typed admitted document = %q, %v", document.Content, err)
	}
	if _, err := lease.Corpus().Read(t.Context(), "README.md"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("typed unadmitted document error = %v", err)
	}
	typedCorpus, ok := lease.Corpus().(sdk.SCIPCorpus)
	if !ok {
		t.Fatal("typed Git corpus has no SCIP input capability")
	}
	input, err := typedCorpus.ReadSCIPIndex(t.Context())
	if err != nil || !input.Present || input.Path != "index.scip" || input.Content != "typed-index" {
		t.Fatalf("typed input = %+v, %v", input, err)
	}
}
