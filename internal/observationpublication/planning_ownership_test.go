package observationpublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/repowork"
	"github.com/bmeddeb/phebs/internal/store"
)

type planningOwnershipStore struct {
	store.GenerationSchedulerStore

	mu              sync.Mutex
	current         map[string]*store.GenerationSchedule
	specs           []store.GenerationScheduleSpec
	enqueueCalls    int
	failEnqueue     int
	failEnqueueWith error
	leaseLost       bool
}

func (state *planningOwnershipStore) EnqueueGenerationSchedule(
	_ context.Context, spec store.GenerationScheduleSpec,
) (*store.GenerationSchedule, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.enqueueCalls++
	if state.failEnqueue > 0 {
		state.failEnqueue--
		return nil, state.failEnqueueWith
	}
	digest, err := store.GenerationScheduleDigest(spec)
	if err != nil {
		return nil, err
	}
	if state.current == nil {
		state.current = make(map[string]*store.GenerationSchedule)
	}
	if previous := state.current[spec.Stage]; previous != nil {
		if previous.Digest == digest && previous.Status == store.GenerationScheduleActive {
			value := *previous
			return &value, nil
		}
		if previous.Status == store.GenerationScheduleActive {
			previous.Status = store.GenerationScheduleSuperseded
			previous.UpdatedAt = previous.UpdatedAt.Add(time.Nanosecond)
		}
	}
	now := time.Now().UTC()
	schedule := &store.GenerationSchedule{
		Schema: store.GenerationScheduleSchema, Digest: digest,
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ResourceClass: spec.ResourceClass, TotalItems: spec.TotalItems,
		ChunkItems:  spec.ChunkItems,
		TotalChunks: int((spec.TotalItems + int64(spec.ChunkItems) - 1) / int64(spec.ChunkItems)),
		MaxAttempts: spec.MaxAttempts, RepositoryTokens: spec.RepositoryTokens,
		Status: store.GenerationScheduleActive, CreatedAt: now, UpdatedAt: now,
	}
	state.current[spec.Stage] = schedule
	state.specs = append(state.specs, spec)
	value := *schedule
	return &value, nil
}

func (state *planningOwnershipStore) GetGenerationSchedule(
	_ context.Context, _, stage string,
) (*store.GenerationSchedule, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	schedule := state.current[stage]
	if schedule == nil {
		return nil, store.ErrNotFound
	}
	value := *schedule
	return &value, nil
}

func (state *planningOwnershipStore) HeartbeatGenerationChunk(
	_ context.Context, chunk store.GenerationChunk,
) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.leaseLost || chunk.Status != store.GenerationChunkRunning ||
		chunk.LeaseToken != "planning-lease" || chunk.ClaimedBy != "planning-worker" {
		return store.ErrGenerationLeaseLost
	}
	return nil
}

func (state *planningOwnershipStore) settlePlanningFailure(t *testing.T) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	schedule := state.current[PlanningScheduleStage]
	if schedule == nil {
		t.Fatal("planning schedule is absent")
	}
	schedule.NextOffset = schedule.TotalItems
	schedule.Materialized = schedule.TotalChunks
	schedule.Failed = schedule.TotalChunks
	schedule.Status = store.GenerationScheduleSettled
	schedule.UpdatedAt = schedule.UpdatedAt.Add(time.Nanosecond)
}

func (state *planningOwnershipStore) specsFor(stage string) []store.GenerationScheduleSpec {
	state.mu.Lock()
	defer state.mu.Unlock()
	var result []store.GenerationScheduleSpec
	for _, spec := range state.specs {
		if spec.Stage == stage {
			result = append(result, spec)
		}
	}
	return result
}

func TestEnqueuePlanningUsesBoundedControlsAndCoalesces(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commit := planningOwnershipFixture(t)
	source := publishPlanningOwnershipSource(
		t, dataDirectory, repositoryDirectory, repository, commit,
	)
	for _, member := range source.Members {
		if err := os.Remove(filepath.Join(dataDirectory, "index", member.Name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(repositoryDirectory, repositoryDirectory+".hidden"); err != nil {
		t.Fatal(err)
	}

	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	result, err := runtime.EnqueuePlanning(t.Context(), repository)
	if err != nil || result != PlanningEnqueued {
		t.Fatalf("first enqueue = %q, %v", result, err)
	}
	result, err = runtime.EnqueuePlanning(t.Context(), repository)
	if err != nil || result != PlanningActive {
		t.Fatalf("coalesced enqueue = %q, %v", result, err)
	}
	specs := state.specsFor(PlanningScheduleStage)
	if len(specs) != 1 {
		t.Fatalf("planning specs = %+v", specs)
	}
	spec := specs[0]
	if spec.TotalItems != 1 || spec.ChunkItems != 1 ||
		spec.ResourceClass != store.GenerationResourceIO ||
		spec.MaxAttempts != PlanningScheduleMaxAttempts ||
		spec.RepositoryTokens != PlanningScheduleRepositoryTokens {
		t.Fatalf("planning spec = %+v", spec)
	}
}

func TestEnqueuePlanningRepairsMissingInitialOwnership(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commit := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commit)
	wantErr := errors.New("injected enqueue interruption")
	state := &planningOwnershipStore{failEnqueue: 1, failEnqueueWith: wantErr}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}

	if result, err := runtime.EnqueuePlanning(t.Context(), repository); result != "" ||
		!errors.Is(err, wantErr) {
		t.Fatalf("interrupted enqueue = %q, %v", result, err)
	}
	target, _, err := planningTarget(repository, mustPlanningSource(t, dataDirectory, repository).Digest)
	if err != nil {
		t.Fatal(err)
	}
	bindingPath := runtime.planningBindingPath(repository, target)
	before, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.EnqueuePlanning(t.Context(), repository)
	if err != nil || result != PlanningEnqueued {
		t.Fatalf("repaired enqueue = %q, %v", result, err)
	}
	after, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("repair rewrote the immutable planning binding")
	}
	if state.enqueueCalls != 2 || len(state.specsFor(PlanningScheduleStage)) != 1 {
		t.Fatalf("enqueue calls/specs = %d/%+v", state.enqueueCalls, state.specsFor(PlanningScheduleStage))
	}
}

func TestEnqueuePlanningKeepsTerminalSameTargetSettled(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commit := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commit)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("initial enqueue = %q, %v", result, err)
	}
	state.settlePlanningFailure(t)
	result, err := runtime.EnqueuePlanning(t.Context(), repository)
	if err != nil || result != PlanningFailed {
		t.Fatalf("terminal coalesce = %q, %v", result, err)
	}
	if specs := state.specsFor(PlanningScheduleStage); len(specs) != 1 {
		t.Fatalf("terminal target was rescheduled: %+v", specs)
	}
}

func TestHandlePlanningClosesInvalidBindingAsTerminal(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commit := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commit)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if disposition, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		disposition != PlanningEnqueued {
		t.Fatalf("planning enqueue = %q, %v", disposition, err)
	}
	spec := state.specsFor(PlanningScheduleStage)[0]
	if err := os.WriteFile(
		runtime.planningBindingPath(repository, spec.Generation), []byte("{}\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	err := runtime.HandlePlanning(t.Context(), planningChunkForSpec(t, spec))
	if !store.IsTerminal(err) {
		t.Fatalf("invalid binding error is not terminal: %v", err)
	}
	receipt, ok := pipelinerefusal.From(err)
	if !ok || receipt.Stage != pipelinerefusal.StageSourcePartitionPlanning ||
		receipt.GenerationKind != pipelinerefusal.GenerationSourcePartition ||
		receipt.Classification != pipelinerefusal.ClassificationInvalid {
		t.Fatalf("invalid binding refusal = %+v, present=%t", receipt, ok)
	}
	for _, private := range []string{repository, repositoryDirectory, spec.Generation} {
		if strings.Contains(store.DurableErrorText(err), private) {
			t.Fatalf("durable terminal text contains %q: %s", private, store.DurableErrorText(err))
		}
	}
	state.settlePlanningFailure(t)
	if disposition, enqueueErr := runtime.EnqueuePlanning(t.Context(), repository); enqueueErr != nil || disposition != PlanningFailed {
		t.Fatalf("terminal corrupt same-target enqueue = %q, %v", disposition, enqueueErr)
	}
	if specs := state.specsFor(PlanningScheduleStage); len(specs) != 1 {
		t.Fatalf("terminal corrupt target was rescheduled: %+v", specs)
	}
}

func TestEnqueuePlanningMintsDistinguishableABARecovery(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue A = %q, %v", result, err)
	}
	initialA := state.specsFor(PlanningScheduleStage)[0]

	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue B = %q, %v", result, err)
	}
	all := state.specsFor(PlanningScheduleStage)
	b := all[len(all)-1]
	bDigest, err := store.GenerationScheduleDigest(b)
	if err != nil {
		t.Fatal(err)
	}

	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue returned A = %q, %v", result, err)
	}
	all = state.specsFor(PlanningScheduleStage)
	returnedA := all[len(all)-1]
	if returnedA.Generation == initialA.Generation ||
		returnedA.Generation != recoveryPlanningGeneration(initialA.Generation, bDigest) {
		t.Fatalf("returned A generation = %q, initial=%q B-digest=%q", returnedA.Generation, initialA.Generation, bDigest)
	}
	binding, err := runtime.readPlanningBinding(repository, returnedA.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if binding.TargetGeneration != initialA.Generation || binding.PriorScheduleDigest != bDigest {
		t.Fatalf("returned A binding = %+v", binding)
	}
}

func TestEnqueuePlanningRecurrentPublicationSupersedesStalePlanner(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	v1 := state.specsFor(ScheduleStage)
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: v1[0].Generation,
		Offset: 0, Length: int(v1[0].TotalItems),
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue B = %q, %v", result, err)
	}
	ownedB := state.specsFor(PlanningScheduleStage)[0]

	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("re-current A did not supersede B = %q, %v", result, err)
	}
	plans := state.specsFor(PlanningScheduleStage)
	if len(plans) != 2 || plans[1].Generation == plans[0].Generation {
		t.Fatalf("planning schedules = %+v", plans)
	}
	if err := runtime.HandlePlanning(
		t.Context(), planningChunkForSpec(t, ownedB),
	); !errors.Is(err, store.ErrGenerationStale) {
		t.Fatalf("superseded B handler = %v", err)
	}
	current, err := CurrentGeneration(filepath.Join(dataDirectory, "observations"), repository)
	if err != nil || current != v1[0].Generation {
		t.Fatalf("re-current A pointer = %q, %v; want %q", current, err, v1[0].Generation)
	}
}

func TestHandlePlanningStaleFinalFencePreservesPriorPointer(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}

	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	v1 := state.specsFor(ScheduleStage)
	if len(v1) != 1 {
		t.Fatalf("legacy specs = %+v", v1)
	}
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: v1[0].Generation,
		Offset: 0, Length: int(v1[0].TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	prior, err := CurrentGeneration(filepath.Join(dataDirectory, "observations"), repository)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue B = %q, %v", result, err)
	}
	planning := state.specsFor(PlanningScheduleStage)
	owned := planning[len(planning)-1]

	release, err := repowork.LockContext(t.Context(), repositoryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ownedChunk := planningChunkForSpec(t, owned)
	go func() {
		done <- runtime.HandlePlanning(t.Context(), ownedChunk)
	}()
	waitForPlanningDirectory(t, runtime, repository)
	_, err = state.EnqueueGenerationSchedule(t.Context(), store.GenerationScheduleSpec{
		Repository: repository, Stage: PlanningScheduleStage,
		Generation:    "sha256:" + strings.Repeat("f", 64),
		ResourceClass: store.GenerationResourceIO, TotalItems: 1, ChunkItems: 1,
		MaxAttempts:      PlanningScheduleMaxAttempts,
		RepositoryTokens: PlanningScheduleRepositoryTokens,
	})
	if err != nil {
		release()
		t.Fatal(err)
	}
	release()
	select {
	case handleErr := <-done:
		if !errors.Is(handleErr, store.ErrGenerationStale) {
			t.Fatalf("stale planning error = %v", handleErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stale planning handler did not return")
	}
	current, err := CurrentGeneration(filepath.Join(dataDirectory, "observations"), repository)
	if err != nil || current != prior {
		t.Fatalf("current after stale plan = %q, %v; want %q", current, err, prior)
	}
	if specs := state.specsFor(ScheduleStage); len(specs) != 1 {
		t.Fatalf("stale planner enqueued a v1 successor: %+v", specs)
	}
}

func TestHandlePlanningReactivatesHistoricalGenerationOutsideTransitionFence(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	cache := &Cache{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: cache,
		AcquireTransition: noopPlanningTransition,
	}

	publish := func(label string) string {
		t.Helper()
		if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
			result != PlanningEnqueued {
			t.Fatalf("enqueue %s = %q, %v", label, result, err)
		}
		planning := state.specsFor(PlanningScheduleStage)
		owned := planning[len(planning)-1]
		if err := runtime.HandlePlanning(t.Context(), planningChunkForSpec(t, owned)); err != nil {
			t.Fatalf("plan %s: %v", label, err)
		}
		execution := state.specsFor(ScheduleStage)
		spec := execution[len(execution)-1]
		if err := runtime.Handle(t.Context(), store.GenerationChunk{
			Repository: repository, Stage: ScheduleStage, Generation: spec.Generation,
			Offset: 0, Length: int(spec.TotalItems),
		}); err != nil {
			t.Fatalf("publish %s: %v", label, err)
		}
		generation, err := CurrentGeneration(
			filepath.Join(dataDirectory, "observations"), repository,
		)
		if err != nil {
			t.Fatalf("current %s: %v", label, err)
		}
		return generation
	}

	generationA := publish("A")
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	generationB := publish("B")
	if generationA == generationB {
		t.Fatalf("A and B reused observation generation %q", generationA)
	}

	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue returned A = %q, %v", result, err)
	}
	planning := state.specsFor(PlanningScheduleStage)
	returnedA := planning[len(planning)-1]
	executionCount := len(state.specsFor(ScheduleStage))

	indexDirectory := filepath.Join(dataDirectory, "index")
	releaseRepository, err := repowork.LockContext(t.Context(), repositoryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	repositoryHeld := true
	defer func() {
		if repositoryHeld {
			releaseRepository()
		}
	}()
	transitionAttempt := make(chan struct{})
	var transitionOnce sync.Once
	runtime.AcquireTransition = func(ctx context.Context) (func(), error) {
		release, lockErr := focusedindex.AcquireExclusiveMutationLock(ctx, indexDirectory)
		if lockErr == nil {
			transitionOnce.Do(func() { close(transitionAttempt) })
		}
		return release, lockErr
	}
	done := make(chan error, 1)
	go func() {
		done <- runtime.HandlePlanning(t.Context(), planningChunkForSpec(t, returnedA))
	}()
	select {
	case <-transitionAttempt:
	case <-time.After(10 * time.Second):
		releaseRepository()
		repositoryHeld = false
		t.Fatal("historical reactivation did not reach the transition fence")
	}
	if !cache.Pinned(repository, generationA) {
		releaseRepository()
		repositoryHeld = false
		t.Fatal("historical generation was not validated and pinned before transition")
	}
	sharedAcquired := make(chan error, 1)
	releaseIndexer := make(chan struct{})
	go func() {
		release, lockErr := focusedindex.AcquireMutationLock(t.Context(), indexDirectory)
		sharedAcquired <- lockErr
		if lockErr == nil {
			<-releaseIndexer
			release()
		}
	}()
	select {
	case lockErr := <-sharedAcquired:
		if lockErr != nil {
			releaseRepository()
			repositoryHeld = false
			t.Fatalf("index publication shared fence: %v", lockErr)
		}
	case <-time.After(10 * time.Second):
		releaseRepository()
		repositoryHeld = false
		t.Fatal("planning held exclusive fence while waiting for repository lock")
	}
	current, err := CurrentGeneration(filepath.Join(dataDirectory, "observations"), repository)
	if err != nil || current != generationB {
		releaseRepository()
		repositoryHeld = false
		close(releaseIndexer)
		t.Fatalf("current while transition is excluded = %q, %v; want B %q", current, err, generationB)
	}
	select {
	case handleErr := <-done:
		releaseRepository()
		repositoryHeld = false
		close(releaseIndexer)
		t.Fatalf("historical reactivation crossed the held mutation lock: %v", handleErr)
	default:
	}

	releaseRepository()
	repositoryHeld = false
	close(releaseIndexer)
	select {
	case handleErr := <-done:
		if handleErr != nil {
			t.Fatalf("historical reactivation: %v", handleErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("historical reactivation did not finish")
	}
	current, err = CurrentGeneration(filepath.Join(dataDirectory, "observations"), repository)
	if err != nil || current != generationA {
		t.Fatalf("reactivated current = %q, %v; want A %q", current, err, generationA)
	}
	if cache.Pinned(repository, generationA) {
		t.Fatal("historical generation remained pinned after transition completion")
	}
	if specs := state.specsFor(ScheduleStage); len(specs) != executionCount {
		t.Fatalf("historical reactivation enqueued another execution schedule: %+v", specs)
	}
}

func TestHandlePlanningCancellationCleansTemporaryPlanAndPreservesAuthority(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	execution := state.specsFor(ScheduleStage)
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: execution[0].Generation,
		Offset: 0, Length: int(execution[0].TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDirectory, "observations")
	prior, err := CurrentGeneration(root, repository)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue B = %q, %v", result, err)
	}
	planning := state.specsFor(PlanningScheduleStage)
	owned := planning[len(planning)-1]
	beforeEntries := planningEntryNames(t, runtime, repository)
	executionCount := len(state.specsFor(ScheduleStage))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	err = runtime.HandlePlanning(canceled, planningChunkForSpec(t, owned))
	if !errors.Is(err, context.Canceled) || store.IsTerminal(err) {
		t.Fatalf("planning cancellation = %v, terminal=%t", err, store.IsTerminal(err))
	}
	receipt, ok := pipelinerefusal.From(err)
	if !ok || receipt.Stage != pipelinerefusal.StageSourcePartitionPlanning ||
		receipt.GenerationKind != pipelinerefusal.GenerationSourcePartition ||
		receipt.Classification != pipelinerefusal.ClassificationUnknown {
		t.Fatalf("planning cancellation refusal = %+v, present=%t", receipt, ok)
	}
	current, err := CurrentGeneration(root, repository)
	if err != nil || current != prior {
		t.Fatalf("current after cancellation = %q, %v; want %q", current, err, prior)
	}
	if _, present, markerErr := readMarker(root, repository); markerErr != nil || present {
		t.Fatalf("marker after cancellation = present=%t, err=%v", present, markerErr)
	}
	if specs := state.specsFor(ScheduleStage); len(specs) != executionCount {
		t.Fatalf("canceled planner enqueued a v1 successor: %+v", specs)
	}
	afterEntries := planningEntryNames(t, runtime, repository)
	if len(afterEntries) != len(beforeEntries) {
		t.Fatalf("planning entries after cancellation = %v; want %v", afterEntries, beforeEntries)
	}
	for index := range beforeEntries {
		if afterEntries[index] != beforeEntries[index] {
			t.Fatalf("planning entries after cancellation = %v; want %v", afterEntries, beforeEntries)
		}
	}
}

func TestHandlePlanningLeaseLossAtFinalFencePreservesPriorAuthority(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	execution := state.specsFor(ScheduleStage)
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: execution[0].Generation,
		Offset: 0, Length: int(execution[0].TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDirectory, "observations")
	prior, err := CurrentGeneration(root, repository)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue B = %q, %v", result, err)
	}
	planning := state.specsFor(PlanningScheduleStage)
	owned := planning[len(planning)-1]
	executionCount := len(state.specsFor(ScheduleStage))

	releaseRepository, err := repowork.LockContext(t.Context(), repositoryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runtime.HandlePlanning(t.Context(), planningChunkForSpec(t, owned))
	}()
	waitForPlanningDirectory(t, runtime, repository)
	state.mu.Lock()
	state.leaseLost = true
	state.mu.Unlock()
	releaseRepository()

	select {
	case handleErr := <-done:
		if !errors.Is(handleErr, store.ErrGenerationLeaseLost) || store.IsTerminal(handleErr) {
			t.Fatalf("lease-lost planning fence = %v, terminal=%t", handleErr, store.IsTerminal(handleErr))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lease-lost planning handler did not return")
	}
	current, err := CurrentGeneration(root, repository)
	if err != nil || current != prior {
		t.Fatalf("current after lease loss = %q, %v; want %q", current, err, prior)
	}
	if _, present, markerErr := readMarker(root, repository); markerErr != nil || present {
		t.Fatalf("marker after lease loss = present=%t, err=%v", present, markerErr)
	}
	if specs := state.specsFor(ScheduleStage); len(specs) != executionCount {
		t.Fatalf("lease-lost planner enqueued a v1 successor: %+v", specs)
	}
}

func TestDetachStagePreservesCurrentAndHandsStaleBytesToLifecycle(t *testing.T) {
	generation := "sha256:" + strings.Repeat("a", 64)
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	for _, test := range []struct {
		name    string
		current bool
	}{
		{name: "current marker cleanup preserves authority", current: true},
		{name: "stale stage enters bounded lifecycle namespace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "observations")
			repository := "example.com/detach/" + strings.ReplaceAll(test.name, " ", "-")
			repositoryRoot := repositoryDirectory(root, repository)
			generationPath := generationDirectory(root, repository, generation)
			if err := os.MkdirAll(filepath.Join(generationPath, "objects"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := writeAtomicCanonical(markerPath(root, repository), Marker{
				Schema: MarkerSchema, Repository: repository, GenerationDigest: generation,
			}); err != nil {
				t.Fatal(err)
			}
			if test.current {
				if err := writeAtomicCanonical(pointerPath(root, repository), Pointer{
					Schema: PointerSchema, Repository: repository,
					GenerationDigest: generation, ManifestDigest: manifestDigest,
					ManifestName: filepath.Join(strings.TrimPrefix(generation, "sha256:"), manifestName()),
				}); err != nil {
					t.Fatal(err)
				}
			}

			runtime := &Runtime{}
			release, err := runtime.quiesceBuild(t.Context(), repository, generation)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if err := runtime.detachStage(root, repository, generation); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(markerPath(root, repository)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("marker after detach = %v", err)
			}
			collectingPath := filepath.Join(
				repositoryRoot, "collecting-"+strings.TrimPrefix(generation, "sha256:"),
			)
			if test.current {
				if info, err := os.Lstat(generationPath); err != nil || !info.IsDir() {
					t.Fatalf("current generation after marker cleanup = %v, %v", info, err)
				}
				if _, err := os.Lstat(collectingPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("current generation was detached = %v", err)
				}
				return
			}
			if _, err := os.Lstat(generationPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale generation remained authoritative = %v", err)
			}
			if info, err := os.Lstat(collectingPath); err != nil || !info.IsDir() {
				t.Fatalf("stale generation lifecycle handoff = %v, %v", info, err)
			}
		})
	}
}

func TestPlanningConvergesAcrossPrePublicationABACycle(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commit := planningOwnershipFixture(t)
	commitA := commit
	state := &planningOwnershipStore{}
	cache := &Cache{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: cache,
		AcquireTransition: noopPlanningTransition,
	}
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commit)

	planCurrent := func(label string) {
		t.Helper()
		if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
			result != PlanningEnqueued {
			t.Fatalf("enqueue %s = %q, %v", label, result, err)
		}
		planning := state.specsFor(PlanningScheduleStage)
		if err := runtime.HandlePlanning(
			t.Context(), planningChunkForSpec(t, planning[len(planning)-1]),
		); err != nil {
			t.Fatalf("plan %s: %v", label, err)
		}
	}
	advance := func(label, body string) {
		t.Helper()
		if err := os.WriteFile(
			filepath.Join(repositoryDirectory, "main.go"), []byte(body), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		runObservationGit(t, repositoryDirectory, "add", "main.go")
		runObservationGit(t, repositoryDirectory, "commit", "-m", label)
		commit = strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
		publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commit)
	}

	planCurrent("A")
	advance("B", "package main\nfunc main() { println(2) }\n")
	planCurrent("B")
	commit = commitA
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	planCurrent("A2")
	advance("C", "package main\nfunc main() { println(3) }\n")
	planCurrent("C")

	root := filepath.Join(dataDirectory, "observations")
	if _, err := readPointer(root, repository); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-publication pointer = %v", err)
	}
	marker, present, err := readMarker(root, repository)
	if err != nil || !present {
		t.Fatalf("C marker = %+v, present=%t, err=%v", marker, present, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, repositoryHash(repository)))
	if err != nil {
		t.Fatal(err)
	}
	collecting := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "collecting-") {
			collecting++
		}
	}
	if collecting != 3 {
		t.Fatalf("pre-publication collecting generations = %d, want 3", collecting)
	}
	for remaining := 3; remaining > 0; remaining-- {
		result, err := SweepLifecycle(
			t.Context(), root, time.Now().UTC().Add(GenerationMaxAge), "", cache, 16,
		)
		if err != nil || result.Deleted == 0 {
			t.Fatalf("pre-publication lifecycle remaining=%d result=%+v err=%v", remaining, result, err)
		}
	}
	execution := state.specsFor(ScheduleStage)
	latest := execution[len(execution)-1]
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: latest.Generation,
		Offset: 0, Length: int(latest.TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	if current, err := CurrentGeneration(root, repository); err != nil || current != marker.GenerationDigest {
		t.Fatalf("first publication after A-B-A-C = %q, %v; want %q", current, err, marker.GenerationDigest)
	}
}

func TestPlanningWaitsForActiveV1WriterBeforeStageRetirement(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commitA := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitA)
	state := &planningOwnershipStore{}
	runtime := &Runtime{
		DataDir: dataDirectory, Store: state, Cache: &Cache{},
		AcquireTransition: noopPlanningTransition,
	}
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		t.Fatalf("enqueue A = %q, %v", result, err)
	}
	planning := state.specsFor(PlanningScheduleStage)
	if err := runtime.HandlePlanning(t.Context(), planningChunkForSpec(t, planning[0])); err != nil {
		t.Fatal(err)
	}
	executionA := state.specsFor(ScheduleStage)[0]
	markerA, present, err := readMarker(filepath.Join(dataDirectory, "observations"), repository)
	if err != nil || !present {
		t.Fatalf("A marker = %+v, present=%t, err=%v", markerA, present, err)
	}
	releaseWriter, err := runtime.beginBuild(repository, markerA.GenerationDigest)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() { println(2) }\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "B")
	commitB := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commitB)
	if result, err := runtime.EnqueuePlanning(t.Context(), repository); err != nil ||
		result != PlanningEnqueued {
		releaseWriter()
		t.Fatalf("enqueue B = %q, %v", result, err)
	}
	planning = state.specsFor(PlanningScheduleStage)
	done := make(chan error, 1)
	go func() {
		done <- runtime.HandlePlanning(
			t.Context(), planningChunkForSpec(t, planning[len(planning)-1]),
		)
	}()
	waitForBuildRetirement(t, runtime, repository, markerA.GenerationDigest)
	select {
	case handleErr := <-done:
		releaseWriter()
		t.Fatalf("planning retired an active v1 stage: %v", handleErr)
	default:
	}
	if _, err := os.Lstat(generationDirectory(
		filepath.Join(dataDirectory, "observations"), repository, markerA.GenerationDigest,
	)); err != nil {
		releaseWriter()
		t.Fatalf("active v1 stage moved before writer return: %v", err)
	}
	releaseWriter()
	select {
	case handleErr := <-done:
		if handleErr != nil {
			t.Fatalf("B planning after writer return: %v", handleErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("B planning did not resume after writer return")
	}
	root := filepath.Join(dataDirectory, "observations")
	canonicalA := generationDirectory(root, repository, markerA.GenerationDigest)
	if _, err := os.Lstat(canonicalA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired A canonical stage = %v", err)
	}
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: executionA.Generation,
		Offset: 0, Length: int(executionA.TotalItems),
	}); err == nil {
		t.Fatalf("superseded A writer = %v", err)
	}
	if _, err := os.Lstat(canonicalA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded A writer recreated canonical stage = %v", err)
	}
}

func TestPlanningAPILeavesV1ReconcileAndHandleCompatible(t *testing.T) {
	dataDirectory, repositoryDirectory, repository, commit := planningOwnershipFixture(t)
	publishPlanningOwnershipSource(t, dataDirectory, repositoryDirectory, repository, commit)
	state := &planningOwnershipStore{}
	runtime := &Runtime{DataDir: dataDirectory, Store: state}

	if err := runtime.Reconcile(t.Context(), repository); err != nil {
		t.Fatal(err)
	}
	specs := state.specsFor(ScheduleStage)
	if len(specs) != 1 || specs[0].Stage != ScheduleStage ||
		len(state.specsFor(PlanningScheduleStage)) != 0 {
		t.Fatalf("legacy reconcile specs = %+v, planning=%+v", specs, state.specsFor(PlanningScheduleStage))
	}
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: specs[0].Stage, Generation: specs[0].Generation,
		Offset: 0, Length: int(specs[0].TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	publication, err := Open(t.Context(), filepath.Join(dataDirectory, "observations"), repository)
	if err != nil || publication.manifest.GenerationDigest != specs[0].Generation {
		t.Fatalf("legacy publication = %+v, %v", publication, err)
	}
}

func planningOwnershipFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	dataDirectory := t.TempDir()
	repository := "github.com/example/planning-ownership"
	repositoryDirectory := filepath.Join(
		dataDirectory, "repos", filepath.FromSlash(repository)+".git",
	)
	if err := os.MkdirAll(repositoryDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "init")
	runObservationGit(t, repositoryDirectory, "config", "user.email", "fixture@example.invalid")
	runObservationGit(t, repositoryDirectory, "config", "user.name", "Fixture")
	if err := os.WriteFile(
		filepath.Join(repositoryDirectory, "main.go"),
		[]byte("package main\nfunc main() {}\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	runObservationGit(t, repositoryDirectory, "add", "main.go")
	runObservationGit(t, repositoryDirectory, "commit", "-m", "A")
	commit := strings.TrimSpace(runObservationGit(t, repositoryDirectory, "rev-parse", "HEAD"))
	return dataDirectory, repositoryDirectory, repository, commit
}

func publishPlanningOwnershipSource(
	t *testing.T, dataDirectory, repositoryDirectory, repository, commit string,
) repositoryindex.SourceManifest {
	t.Helper()
	indexDirectory := filepath.Join(dataDirectory, "index")
	if err := os.RemoveAll(indexDirectory); err != nil {
		t.Fatal(err)
	}
	manifest, err := repositoryindex.BuildSourceGeneration(
		t.Context(), repositoryDirectory, indexDirectory, repository,
		[]store.IndexedRevision{{Selector: "HEAD", Branch: "HEAD", Commit: commit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func mustPlanningSource(
	t *testing.T, dataDirectory, repository string,
) repositoryindex.SourceManifest {
	t.Helper()
	manifest, err := repositoryindex.ReadSourceManifest(
		filepath.Join(dataDirectory, "index"), repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func planningChunkForSpec(
	t *testing.T, spec store.GenerationScheduleSpec,
) store.GenerationChunk {
	t.Helper()
	digest, err := store.GenerationScheduleDigest(spec)
	if err != nil {
		t.Fatal(err)
	}
	return store.GenerationChunk{
		Repository: spec.Repository, Stage: spec.Stage, Generation: spec.Generation,
		ScheduleDigest: digest, ResourceClass: spec.ResourceClass, Offset: 0, Length: 1,
		ID: "planning-chunk", Status: store.GenerationChunkRunning,
		LeaseToken: "planning-lease", ClaimedBy: "planning-worker",
	}
}

func waitForPlanningDirectory(t *testing.T, runtime *Runtime, repository string) {
	t.Helper()
	root := filepath.Dir(runtime.planningBindingPath(
		repository, "sha256:"+strings.Repeat("0", 64),
	))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(root)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), ".planning-") {
					return
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("planning stage did not reach its final fence")
}

func waitForBuildRetirement(
	t *testing.T, runtime *Runtime, repository, generation string,
) {
	t.Helper()
	key := planKey(repository, generation)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		retiring := runtime.retiringBuilds[key]
		runtime.mu.Unlock()
		if retiring {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("planning did not fence the active v1 stage")
}

func noopPlanningTransition(context.Context) (func(), error) {
	return func() {}, nil
}

func planningEntryNames(t *testing.T, runtime *Runtime, repository string) []string {
	t.Helper()
	root := filepath.Dir(runtime.planningBindingPath(
		repository, "sha256:"+strings.Repeat("0", 64),
	))
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
