package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/downstreamauthority"
	"github.com/bmeddeb/phebs/internal/extractionpublication"
	"github.com/bmeddeb/phebs/internal/observationpublication"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
	"github.com/bmeddeb/phebs/internal/store"
)

type runtimeHandleCleanupStore struct {
	RuntimeStore
	publication servicecatalog.Publication
	summary     servicecatalog.RepositoryState
	states      []servicecatalog.ServiceState
	resolver    store.ResolverCatalogPublication
	schedule    store.GenerationSchedule
	domains     map[string]store.PartitionedExtractionDomain

	confirmCalls    int
	snapshotCalls   int
	failConfirmCall int
	pinFailure      error
	failPinRun      string
	failPinAfterSet bool
	cancelPinRun    string
	cancelPin       context.CancelFunc
	unpinFailure    error
	failUnpinRun    string
	pinCalls        []string
	unpinCalls      []string
	unpinContextErr []error
	pinned          map[string]string
}

func (state *runtimeHandleCleanupStore) GetResolverCatalogPublication(
	_ context.Context, repository string,
) (*store.ResolverCatalogPublication, error) {
	if repository != state.resolver.Repository {
		return nil, store.ErrNotFound
	}
	value := state.resolver
	return &value, nil
}

func (state *runtimeHandleCleanupStore) ResolverCatalogPublicationCurrent(
	_ context.Context, value store.ResolverCatalogPublication,
) (bool, error) {
	return value.Repository == state.resolver.Repository &&
		value.GenerationDigest == state.resolver.GenerationDigest &&
		value.AuthorityDigest == state.resolver.AuthorityDigest, nil
}

func (state *runtimeHandleCleanupStore) ListServiceStates(
	_ context.Context,
	repository string,
	_ store.ServiceStateFilter,
	_ store.ServiceStatePosition,
	_ int,
) (*store.ServiceStatePage, error) {
	if repository != state.publication.Repository {
		return nil, store.ErrNotFound
	}
	entries := make([]store.ServiceStateEntry, len(state.states))
	for index, value := range state.states {
		entries[index] = store.ServiceStateEntry{State: value}
	}
	return &store.ServiceStatePage{
		Publication: state.publication,
		Summary:     state.summary,
		Entries:     entries,
	}, nil
}

func (state *runtimeHandleCleanupStore) GetAcceptedServiceStateSnapshot(
	ctx context.Context,
	repository string,
	limit int,
) (*store.AcceptedServiceStateSnapshot, error) {
	state.snapshotCalls++
	if repository != state.publication.Repository || len(state.states) > limit {
		return nil, store.ErrNotFound
	}
	if err := state.ConfirmServiceStateSnapshot(ctx, repository, state.summary); err != nil {
		return nil, err
	}
	return &store.AcceptedServiceStateSnapshot{
		Publication: state.publication, Summary: state.summary,
		States: slices.Clone(state.states),
	}, nil
}

func (state *runtimeHandleCleanupStore) ConfirmServiceStateSnapshot(
	_ context.Context, repository string, summary servicecatalog.RepositoryState,
) error {
	state.confirmCalls++
	if repository != state.publication.Repository || !sameSummaryFence(summary, state.summary) {
		return errors.New("service-state snapshot changed")
	}
	if state.failConfirmCall != 0 && state.confirmCalls == state.failConfirmCall {
		return errors.New("injected late service-state fence")
	}
	return nil
}

func (state *runtimeHandleCleanupStore) GetGenerationSchedule(
	_ context.Context, repository, stage string,
) (*store.GenerationSchedule, error) {
	if repository != state.schedule.Repository || stage != state.schedule.Stage {
		return nil, store.ErrNotFound
	}
	value := state.schedule
	return &value, nil
}

func (state *runtimeHandleCleanupStore) GetPartitionedExtractionDomain(
	_ context.Context, repository, domain string,
) (*store.PartitionedExtractionDomain, error) {
	value, present := state.domains[domain]
	if !present || value.Repository != repository {
		return nil, store.ErrNotFound
	}
	return &value, nil
}

func (state *runtimeHandleCleanupStore) PinPartitionedExtractionRun(
	ctx context.Context, runID, owner string,
) error {
	state.pinCalls = append(state.pinCalls, runID+"\x00"+owner)
	if runID == state.cancelPinRun {
		if state.cancelPin != nil {
			state.cancelPin()
		}
		return ctx.Err()
	}
	if runID == state.failPinRun {
		if state.failPinAfterSet {
			if state.pinned == nil {
				state.pinned = make(map[string]string)
			}
			state.pinned[runID] = owner
		}
		return state.pinFailure
	}
	if state.pinned == nil {
		state.pinned = make(map[string]string)
	}
	state.pinned[runID] = owner
	return nil
}

func (state *runtimeHandleCleanupStore) UnpinPartitionedExtractionRun(
	ctx context.Context, runID, owner string,
) error {
	state.unpinCalls = append(state.unpinCalls, runID+"\x00"+owner)
	state.unpinContextErr = append(state.unpinContextErr, ctx.Err())
	if err := ctx.Err(); err != nil {
		return err
	}
	if runID == state.failUnpinRun {
		return state.unpinFailure
	}
	if state.pinned[runID] == owner {
		delete(state.pinned, runID)
	}
	return nil
}

type runtimeHandleCleanupFixture struct {
	runtime  *Runtime
	store    *runtimeHandleCleanupStore
	chunk    store.GenerationChunk
	upstream downstreamauthority.Authority
	prior    Root
}

func TestRuntimeHandleAbortsRootStageAfterLateAuthorityFence(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	fixture.store.failConfirmCall = 2

	err := fixture.runtime.Handle(t.Context(), fixture.chunk)
	if !errors.Is(err, ErrPublishing) || fixture.store.confirmCalls != 2 ||
		fixture.store.snapshotCalls != 1 {
		t.Fatalf("late authority fence = calls %d, error %v", fixture.store.confirmCalls, err)
	}
	if len(fixture.store.pinCalls) != 0 {
		t.Fatalf("late fence reached extraction pins: %q", fixture.store.pinCalls)
	}
	assertRuntimeHandleRootStageAbsent(t, fixture)
	assertRuntimeHandleCurrentUnchanged(t, fixture)
}

func TestRuntimeHandleAbortsRootStageAndUnwindsPins(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	fixture.store.pinFailure = errors.New("injected extraction pin failure")
	fixture.store.failPinRun = fixture.upstream.Domains[1].RunID
	fixture.store.failPinAfterSet = true

	err := fixture.runtime.Handle(t.Context(), fixture.chunk)
	if !errors.Is(err, fixture.store.pinFailure) || fixture.store.confirmCalls != 2 ||
		fixture.store.snapshotCalls != 1 {
		t.Fatalf("partitioned pin failure = confirms %d, error %v", fixture.store.confirmCalls, err)
	}
	if len(fixture.store.pinCalls) != 2 || len(fixture.store.unpinCalls) != 2 ||
		!slices.Equal(fixture.store.unpinCalls, fixture.store.pinCalls) || len(fixture.store.pinned) != 0 {
		t.Fatalf("pin unwind = pins %q, unpins %q, retained %v",
			fixture.store.pinCalls, fixture.store.unpinCalls, fixture.store.pinned)
	}
	firstRun, owner, found := strings.Cut(fixture.store.pinCalls[0], "\x00")
	secondRun, secondOwner, secondFound := strings.Cut(fixture.store.pinCalls[1], "\x00")
	if !found || !secondFound || firstRun != fixture.upstream.Domains[0].RunID ||
		secondRun != fixture.upstream.Domains[1].RunID || owner != secondOwner ||
		!strings.HasPrefix(owner, "relationship:sha256:") ||
		owner == "relationship:"+fixture.prior.GenerationDigest {
		t.Fatalf("pin identities = %q", fixture.store.pinCalls)
	}
	assertRuntimeHandleRootStageAbsent(t, fixture)
	assertRuntimeHandleCurrentUnchanged(t, fixture)
}

func TestRuntimeHandleRollbackIgnoresCallerCancellation(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.store.cancelPinRun = fixture.upstream.Domains[1].RunID
	fixture.store.cancelPin = cancel

	err := fixture.runtime.Handle(ctx, fixture.chunk)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled partitioned pin = %v", err)
	}
	if len(fixture.store.unpinContextErr) != 2 || fixture.store.unpinContextErr[0] != nil ||
		fixture.store.unpinContextErr[1] != nil ||
		len(fixture.store.pinned) != 0 {
		t.Fatalf("canceled rollback = contexts %v, retained %v",
			fixture.store.unpinContextErr, fixture.store.pinned)
	}
	assertRuntimeHandleRootStageAbsent(t, fixture)
	assertRuntimeHandleCurrentUnchanged(t, fixture)
}

func TestRuntimeHandleReportsPinRollbackFailure(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	fixture.store.pinFailure = errors.New("injected extraction pin failure")
	fixture.store.failPinRun = fixture.upstream.Domains[1].RunID
	fixture.store.unpinFailure = errors.New("injected extraction unpin failure")
	fixture.store.failUnpinRun = fixture.upstream.Domains[0].RunID

	err := fixture.runtime.Handle(t.Context(), fixture.chunk)
	if !errors.Is(err, fixture.store.pinFailure) || !errors.Is(err, fixture.store.unpinFailure) {
		t.Fatalf("partitioned pin rollback error = %v", err)
	}
	if len(fixture.store.pinned) != 1 ||
		fixture.store.pinned[fixture.upstream.Domains[0].RunID] == "" {
		t.Fatalf("failed rollback did not report retained pin: %v", fixture.store.pinned)
	}
	assertRuntimeHandleRootStageAbsent(t, fixture)
	assertRuntimeHandleCurrentUnchanged(t, fixture)
}

func TestRuntimeHandleRedeliveryKeepsPublishedOwnerPins(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	wantPins := maps.Clone(fixture.store.pinned)
	fixture.store.pinCalls = nil
	fixture.store.unpinCalls = nil
	fixture.store.pinFailure = errors.New("redelivery must not repin current authority")
	fixture.store.failPinRun = fixture.upstream.Domains[0].RunID

	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatalf("redeliver published relationship root: %v", err)
	}
	if len(fixture.store.pinCalls) != 0 || len(fixture.store.unpinCalls) != 0 ||
		!maps.Equal(fixture.store.pinned, wantPins) {
		t.Fatalf("redelivery changed pins: pin=%q unpin=%q retained=%v want=%v",
			fixture.store.pinCalls, fixture.store.unpinCalls, fixture.store.pinned, wantPins)
	}
	assertRuntimeHandleRootStageAbsent(t, fixture)
}

func TestRuntimeHandleRetainsPinsAfterPostCurrentPublishFailure(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	base := repositoryRoot(fixture.runtime.relationshipRoot(), fixture.chunk.Repository)
	blocker := filepath.Join(base, UnavailableName)
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := fixture.runtime.Handle(t.Context(), fixture.chunk)
	if err == nil || !strings.Contains(err.Error(), "publish relationship root") {
		t.Fatalf("post-current publication failure = %v", err)
	}
	if len(fixture.store.pinCalls) != len(fixture.upstream.Domains) ||
		len(fixture.store.unpinCalls) != 0 || len(fixture.store.pinned) != len(fixture.upstream.Domains) {
		t.Fatalf("post-current pins = pins %q, unpins %q, retained %v",
			fixture.store.pinCalls, fixture.store.unpinCalls, fixture.store.pinned)
	}
	_, owner, found := strings.Cut(fixture.store.pinCalls[0], "\x00")
	if !found || !strings.HasPrefix(owner, "relationship:sha256:") {
		t.Fatalf("post-current owner = %q", owner)
	}
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	current, err := OpenCurrent(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	if current.Root().GenerationDigest != strings.TrimPrefix(owner, "relationship:") {
		t.Fatalf("current generation = %q, owner = %q",
			current.Root().GenerationDigest, owner)
	}
	for _, domain := range fixture.upstream.Domains {
		if fixture.store.pinned[domain.RunID] != owner {
			t.Fatalf("current run %q pin = %q, want %q", domain.RunID,
				fixture.store.pinned[domain.RunID], owner)
		}
	}
	assertRuntimeHandleRootStageAbsent(t, fixture)
}

func TestPreparedAbortAfterPreinstallPublishFailure(t *testing.T) {
	root := t.TempDir()
	catalog, states := relationshipCatalog(t)
	resolver := fakeResolver{root: resolverRoot(t, catalog.Repository)}
	rpc := fakeRPC{}
	rpc.root = rpcRoot(t, catalog.Repository, resolver.root, nil)
	kafka := fakeKafka{}
	kafka.root = kafkaRoot(t, catalog.Repository, rpc.root.Authority, nil)
	prepared, err := buildSources(t.Context(), root, catalog, states, resolver, rpc, kafka)
	if err != nil {
		t.Fatal(err)
	}
	stage := prepared.directory
	target := generationPath(root, catalog.Repository, prepared.rootValue.GenerationDigest)
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Publish(t.Context()); err == nil {
		t.Fatal("publish accepted conflicting incomplete generation")
	}
	if _, err := os.Lstat(stage); err != nil {
		t.Fatalf("pre-install publish failure consumed stage: %v", err)
	}
	if err := prepared.abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aborted stage remains: %v", err)
	}
}

func TestBuildCleanupFailureIsNotClassifiedAsLimit(t *testing.T) {
	root := t.TempDir()
	repository := "example.com/acme/mono"
	ctx := &stageCleanupFailureContext{
		Context: t.Context(),
		remove:  repositoryRoot(root, repository),
		cause:   ErrLimit,
	}
	_, err := writePublicationStage(
		ctx, root, Authority{Repository: repository}, "unused",
		&buildAccumulator{repository: map[int][]Projection{0: {}}},
	)
	if ctx.removeErr != nil {
		t.Fatal(ctx.removeErr)
	}
	if err == nil || errors.Is(err, ErrLimit) ||
		!strings.Contains(err.Error(), "clean failed relationship publication stage") ||
		!strings.Contains(err.Error(), ErrLimit.Error()) {
		t.Fatalf("cleanup failure classification = %v", err)
	}
}

type stageCleanupFailureContext struct {
	context.Context
	remove    string
	cause     error
	removeErr error
}

func (ctx *stageCleanupFailureContext) Err() error {
	if ctx.remove != "" {
		ctx.removeErr = os.RemoveAll(ctx.remove)
		ctx.remove = ""
	}
	return ctx.cause
}

func newRuntimeHandleCleanupFixture(t *testing.T) runtimeHandleCleanupFixture {
	t.Helper()
	repository := "example.com/acme/mono"
	repositoryDirectory, commit := runtimeHandleGitRepository(t)
	dataDirectory := t.TempDir()
	observationRoot := filepath.Join(dataDirectory, "observations")
	transition, err := observationpublication.BeginInventoryPublicationV2(observationRoot, repository)
	if err != nil {
		t.Fatal(err)
	}
	sourceDirectory := filepath.Join(t.TempDir(), "source")
	source, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, sourceDirectory, repository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := sourcepartition.BuildSuperRoot(t.Context(), sourcepartition.BuildRequest{
		SourceDirectory: sourceDirectory, OutputDirectory: transition.SourceDirectory,
		Repository: repository, Source: source,
		Policy: sourcepartition.Policy{
			Schema: sourcepartition.PolicySchema, Name: "go-source", Version: "1.0.0",
			IncludeSuffixes: []string{".go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sourcepartition.OpenSuperRoot(t.Context(), transition.SourceDirectory, sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.BuildInventoryStageV2(
		t.Context(), observationpublication.InventoryBuildRequestV2{
			OutputDirectory: transition.InventoryDirectory, RepositoryDirectory: repositoryDirectory,
			Plan: plan,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := observationpublication.CompleteInventoryPublicationV2(
		t.Context(), observationRoot, repository, transition.TransitionID, nil,
	); err != nil {
		t.Fatal(err)
	}
	observation, err := observationpublication.CurrentInventoryDownstreamAuthorityV2(
		t.Context(), observationRoot, repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	domains, storedDomains := runtimeHandleDomains(
		t, repositoryDirectory, repository, commit, observation,
	)
	upstream, err := downstreamauthority.Build(observation, domains)
	if err != nil {
		t.Fatal(err)
	}

	catalog, states := relationshipCatalog(t)
	summary := relationshipSummary(t, catalog, states)
	stateSet, err := publicationServiceStateSetDigest(catalog, states)
	if err != nil {
		t.Fatal(err)
	}
	resolver := store.ResolverCatalogPublication{
		Repository: repository, HeadCommit: strings.Repeat("a", 40),
		GenerationDigest: fixedDigest("1"), AuthorityDigest: fixedDigest("2"),
	}
	buildPolicy, err := runtimeBuildPolicyDigest()
	if err != nil {
		t.Fatal(err)
	}
	target, err := runtimeTargetV3(
		repository, upstream.Digest, catalog.GenerationDigest, stateSet,
		resolver.GenerationDigest, summary.SummaryDigest, summary.ControlRevision, buildPolicy,
	)
	if err != nil {
		t.Fatal(err)
	}
	scheduleGeneration := fixedDigest("f")
	state := &runtimeHandleCleanupStore{
		publication: catalog, summary: summary, states: slices.Clone(states), resolver: resolver,
		domains: storedDomains,
		schedule: store.GenerationSchedule{
			Repository: repository, Stage: ScheduleStage, Generation: scheduleGeneration,
			Status: store.GenerationScheduleActive,
		},
	}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &observationpublication.Cache{},
		InventoryCache: &observationpublication.InventoryCacheV2{},
		Domains:        []downstreamauthority.DomainIdentity{},
		Acquire: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	}
	for _, domain := range upstream.Domains {
		runtime.Domains = append(runtime.Domains, downstreamauthority.DomainIdentity{
			Domain: domain.Domain, Version: domain.Version,
		})
	}
	if err := runtime.ensureBuildRoots(); err != nil {
		t.Fatal(err)
	}
	binding := scheduleBinding{
		Schema: BindingSchemaV3, Repository: repository,
		ScheduleGeneration: scheduleGeneration, TargetGeneration: target,
		ObservationGeneration: observation.ObservationGenerationDigest,
		CatalogGeneration:     catalog.GenerationDigest, ServiceStateSet: stateSet,
		ServiceStateSummary: summary.SummaryDigest, ServiceStateRevision: summary.ControlRevision,
		ResolverGeneration: resolver.GenerationDigest, BuildPolicyDigest: buildPolicy,
		Upstream: &upstream,
	}
	if err := setBindingDigest(&binding); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeBinding(binding); err != nil {
		t.Fatal(err)
	}

	priorResolver := fakeResolver{root: resolverRoot(t, repository)}
	priorRPC := fakeRPC{}
	priorRPC.root = rpcRoot(t, repository, priorResolver.root, nil)
	priorKafka := fakeKafka{}
	priorKafka.root = kafkaRoot(t, repository, priorRPC.root.Authority, nil)
	priorStage, err := buildSources(
		t.Context(), runtime.relationshipRoot(), catalog, states,
		priorResolver, priorRPC, priorKafka,
	)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := priorStage.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return runtimeHandleCleanupFixture{
		runtime: runtime, store: state, upstream: upstream, prior: prior.Root(),
		chunk: store.GenerationChunk{
			Repository: repository, Stage: ScheduleStage, Generation: scheduleGeneration,
			Offset: 0, Length: 1,
		},
	}
}

func runtimeHandleDomains(
	t *testing.T,
	repositoryDirectory, repository, commit string,
	observation observationpublication.DownstreamAuthority,
) ([]candidate.DownstreamDomainAuthority, map[string]store.PartitionedExtractionDomain) {
	t.Helper()
	policies := []candidate.Policy{
		{
			Domain: "proto-contract", Version: "1.0.0",
			EnumerationPolicy: "proto-contract-paths-v1", Plane: candidate.PlaneRepository,
			Enumerate: func(path string) bool { return strings.HasSuffix(path, ".proto") },
			Required:  func(path string) bool { return strings.HasSuffix(path, ".proto") },
		},
		{
			Domain: "thrift-field", Version: "1.0.0",
			EnumerationPolicy: "thrift-field-paths-v1", Plane: candidate.PlaneRepository,
			Enumerate: func(path string) bool { return strings.HasSuffix(path, ".thrift") },
			Required:  func(path string) bool { return strings.HasSuffix(path, ".thrift") },
		},
	}
	identities, err := candidate.PolicyIdentities(policies)
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := candidate.PolicyDigest(identities)
	if err != nil {
		t.Fatal(err)
	}
	candidateDirectory := t.TempDir()
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
	sparseDirectory := t.TempDir()
	sparseRoot, err := candidate.BuildSparseRoot(t.Context(), sparseDirectory, publication, nil)
	if err != nil {
		t.Fatal(err)
	}
	sparse, err := candidate.OpenSparse(
		t.Context(), sparseDirectory, candidateDirectory, manifest.State(), sparseRoot.Digest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	authorities := make([]candidate.DownstreamDomainAuthority, 0, len(policies))
	stored := make(map[string]store.PartitionedExtractionDomain, len(policies))
	for index, policy := range policies {
		domain, err := sparse.OpenDomain(t.Context(), policy.Domain, policy.Version)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := extractionpublication.BuildReservedPlan(domain, candidate.DomainResultAuthority{
			SourceGenerationDigest:      observation.SourceGenerationDigest,
			ObservationGenerationDigest: observation.ObservationGenerationDigest,
			ExtractorVersion:            "1.0.0", ExtractionPolicyDigest: fixedDigest(string(rune('d' + index))),
		})
		if err != nil {
			t.Fatal(err)
		}
		root, err := candidate.BuildDomainResultRoot(plan, []candidate.PartitionResult{})
		if err != nil {
			t.Fatal(err)
		}
		runID := "runtime-cleanup-" + policy.Domain
		authority, err := candidate.NewDownstreamDomainAuthority(plan, root, runID)
		if err != nil {
			t.Fatal(err)
		}
		planRaw, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		rootRaw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		authorities = append(authorities, authority)
		stored[policy.Domain] = store.PartitionedExtractionDomain{
			Schema:     store.PartitionedExtractionDomainSchema,
			Repository: repository, Domain: policy.Domain, RunID: runID,
			PlanDigest: plan.Digest, RootDigest: root.Digest,
			CandidateDigest:   plan.CandidateManifestDigest,
			SourceDigest:      observation.SourceGenerationDigest,
			ObservationDigest: observation.ObservationGenerationDigest,
			Facts:             root.Totals.Facts, Rows: root.Totals.Rows, References: root.Totals.References,
			Plan: string(planRaw), Root: string(rootRaw),
		}
	}
	return authorities, stored
}

func runtimeHandleGitRepository(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	runRuntimeHandleGit(t, directory, "init", "-q")
	runRuntimeHandleGit(t, directory, "config", "user.email", "runtime-cleanup@example.invalid")
	runRuntimeHandleGit(t, directory, "config", "user.name", "Runtime Cleanup")
	if err := os.WriteFile(
		filepath.Join(directory, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runRuntimeHandleGit(t, directory, "add", "main.go")
	runRuntimeHandleGit(t, directory, "commit", "-q", "-m", "fixture")
	return directory, strings.TrimSpace(runRuntimeHandleGit(t, directory, "rev-parse", "HEAD"))
}

func runRuntimeHandleGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(
		t.Context(), "git", append([]string{"-C", directory}, arguments...)...,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}

func assertRuntimeHandleRootStageAbsent(t *testing.T, fixture runtimeHandleCleanupFixture) {
	t.Helper()
	base := repositoryRoot(fixture.runtime.relationshipRoot(), fixture.chunk.Repository)
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") {
			t.Fatalf("relationship root stage remains: %s", filepath.Join(base, entry.Name()))
		}
	}
}

func assertRuntimeHandleCurrentUnchanged(t *testing.T, fixture runtimeHandleCleanupFixture) {
	t.Helper()
	current, err := OpenCurrent(
		t.Context(), fixture.runtime.relationshipRoot(), fixture.chunk.Repository,
	)
	if err != nil || current.Root().Digest != fixture.prior.Digest ||
		current.Root().GenerationDigest != fixture.prior.GenerationDigest {
		t.Fatalf("current relationship changed: root %+v, error %v", current, err)
	}
}
