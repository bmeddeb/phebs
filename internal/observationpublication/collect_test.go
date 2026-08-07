package observationpublication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/store"
)

// collectFenceStore lets one test mutate schedule state between the
// collector's enumeration snapshot and its confirming re-read.
type collectFenceStore struct {
	*planningOwnershipStore
	betweenReads func()
	reads        int
}

func (state *collectFenceStore) GetGenerationSchedule(
	ctx context.Context, repository, stage string,
) (*store.GenerationSchedule, error) {
	state.reads++
	// The snapshot pass issues two schedule reads; fire before the confirming
	// pass begins.
	if state.reads == 3 && state.betweenReads != nil {
		state.betweenReads()
	}
	return state.planningOwnershipStore.GetGenerationSchedule(ctx, repository, stage)
}

func planNamespace(t *testing.T, runtime *Runtime, repository string) string {
	t.Helper()
	return filepath.Dir(runtime.planningBindingPath(
		repository, "sha256:"+strings.Repeat("0", 64),
	))
}

func namespaceNames(t *testing.T, runtime *Runtime, repository string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(planNamespace(t, runtime, repository))
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	return names
}

func collectionChunk(t *testing.T, state *planningOwnershipStore) store.GenerationChunk {
	t.Helper()
	specs := state.specsFor(PlanningScheduleStage)
	if len(specs) == 0 {
		t.Fatal("planning schedule is absent")
	}
	return planningChunkForSpec(t, specs[len(specs)-1])
}

// supersededFixture publishes generation A completely, then supersedes it
// with planned-but-unpublished generation B so A's execution binding and the
// planning bindings of both eras coexist with B's live authority.
func supersededFixture(t *testing.T) (*Runtime, *planningOwnershipStore, string, string) {
	t.Helper()
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
	if err := runtime.HandlePlanning(
		t.Context(), planningChunkForSpec(t, planning[len(planning)-1]),
	); err != nil {
		t.Fatal(err)
	}
	execution := state.specsFor(ScheduleStage)
	specA := execution[len(execution)-1]
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: specA.Generation,
		Offset: 0, Length: int(specA.TotalItems),
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
	planning = state.specsFor(PlanningScheduleStage)
	if err := runtime.HandlePlanning(
		t.Context(), planningChunkForSpec(t, planning[len(planning)-1]),
	); err != nil {
		t.Fatal(err)
	}
	execution = state.specsFor(ScheduleStage)
	specB := execution[len(execution)-1]
	if err := runtime.Handle(t.Context(), store.GenerationChunk{
		Repository: repository, Stage: ScheduleStage, Generation: specB.Generation,
		Offset: 0, Length: int(specB.TotalItems),
	}); err != nil {
		t.Fatal(err)
	}
	return runtime, state, repository, specA.Generation
}

func TestCollectRemovesSupersededAndPreservesCurrentAuthority(t *testing.T) {
	runtime, state, repository, generationA := supersededFixture(t)
	// B's own planning run already collected what its lease could prove
	// superseded (A's planning binding); A's plan directory and execution
	// binding stayed protected while A was the current publication. Now that B
	// is current, they are the superseded era, alongside an orphaned planning
	// binding that no schedule ever named.
	orphan := "planning-" + strings.Repeat("e", 64) + ".json"
	namespace := planNamespace(t, runtime, repository)
	if err := os.WriteFile(
		filepath.Join(namespace, orphan), []byte("{}\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	before := namespaceNames(t, runtime, repository)
	planA := strings.TrimPrefix(generationA, "sha256:")
	scheduleBindingA := "schedule-" + planA + ".json"
	for _, name := range []string{planA, scheduleBindingA, orphan} {
		if !before[name] {
			t.Fatalf("superseded artifact %q is absent before collection: %v", name, before)
		}
	}

	result, err := runtime.CollectSupersededPlans(t.Context(), collectionChunk(t, state), 16)
	if err != nil || result.Aborted || result.Plans != 1 || result.Bindings != 2 {
		t.Fatalf("collection = %+v, %v", result, err)
	}
	after := namespaceNames(t, runtime, repository)
	for _, name := range []string{planA, scheduleBindingA, orphan} {
		if after[name] {
			t.Fatalf("superseded artifact %q survived collection: %v", name, after)
		}
	}

	// Current authority stays untouched: B's execution schedule binding, B's
	// plan directory, B's planning binding, and the current publication.
	executionB := state.specsFor(ScheduleStage)
	specB := executionB[len(executionB)-1]
	planningB := state.specsFor(PlanningScheduleStage)
	wantAlive := []string{
		strings.TrimPrefix(specB.Generation, "sha256:"),
		"schedule-" + strings.TrimPrefix(specB.Generation, "sha256:") + ".json",
		"planning-" + strings.TrimPrefix(
			planningB[len(planningB)-1].Generation, "sha256:",
		) + ".json",
	}
	for _, name := range wantAlive {
		if !after[name] {
			t.Fatalf("current artifact %q was collected: %v", name, after)
		}
	}
	root := filepath.Join(runtime.DataDir, "observations")
	current, err := CurrentGeneration(root, repository)
	if err != nil || current != specB.Generation {
		t.Fatalf("current after collection = %q, %v; want %q", current, err, specB.Generation)
	}
}

func TestCollectIsBoundedPerPass(t *testing.T) {
	runtime, state, _, _ := supersededFixture(t)
	first, err := runtime.CollectSupersededPlans(t.Context(), collectionChunk(t, state), 1)
	if err != nil || first.Aborted || first.Plans+first.Stages+first.Bindings != 1 {
		t.Fatalf("bounded first pass = %+v, %v", first, err)
	}
	total := first.Plans + first.Stages + first.Bindings
	for range 8 {
		next, err := runtime.CollectSupersededPlans(t.Context(), collectionChunk(t, state), 1)
		if err != nil || next.Aborted {
			t.Fatalf("bounded pass = %+v, %v", next, err)
		}
		if next.Plans+next.Stages+next.Bindings == 0 {
			break
		}
		total += next.Plans + next.Stages + next.Bindings
	}
	if final, err := runtime.CollectSupersededPlans(t.Context(), collectionChunk(t, state), 16); err != nil ||
		final.Plans+final.Stages+final.Bindings != 0 {
		t.Fatalf("converged namespace still collected %+v, %v (total %d)", final, err, total)
	}
}

func TestCollectAbortsWhenAuthorityMovesBetweenReads(t *testing.T) {
	runtime, state, repository, generationA := supersededFixture(t)
	fenced := &collectFenceStore{
		planningOwnershipStore: state,
		betweenReads: func() {
			state.settlePlanningFailure(t)
		},
	}
	runtime.Store = fenced
	result, err := runtime.CollectSupersededPlans(t.Context(), collectionChunk(t, state), 16)
	if err != nil || !result.Aborted || result.Plans+result.Bindings != 0 {
		t.Fatalf("moved authority collection = %+v, %v", result, err)
	}
	if !namespaceNames(t, runtime, repository)[strings.TrimPrefix(generationA, "sha256:")] {
		t.Fatal("aborted collection still removed a plan directory")
	}
}

func TestCollectSkipsInFlightBuilds(t *testing.T) {
	runtime, state, repository, generationA := supersededFixture(t)
	release, err := runtime.beginBuild(repository, generationA)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := runtime.CollectSupersededPlans(t.Context(), collectionChunk(t, state), 16); err != nil {
		t.Fatal(err)
	}
	if !namespaceNames(t, runtime, repository)[strings.TrimPrefix(generationA, "sha256:")] {
		t.Fatal("collection removed an in-flight build directory")
	}
}

func TestCollectRefusesRemovalAfterPlanningLeaseLoss(t *testing.T) {
	runtime, state, repository, generationA := supersededFixture(t)
	state.mu.Lock()
	state.leaseLost = true
	state.mu.Unlock()
	result, err := runtime.CollectSupersededPlans(
		t.Context(), collectionChunk(t, state), 16,
	)
	if !errors.Is(err, store.ErrGenerationLeaseLost) || result != (CollectResult{}) {
		t.Fatalf("lease-lost collection = %+v, %v", result, err)
	}
	if !namespaceNames(t, runtime, repository)[strings.TrimPrefix(generationA, "sha256:")] {
		t.Fatal("lease-lost collection removed a plan directory")
	}
}

func TestCollectReclaimsCrashStagesBeyondOneScanWindow(t *testing.T) {
	runtime, state, repository, _ := supersededFixture(t)
	namespace := planNamespace(t, runtime, repository)
	for index := 0; index < CollectScanLimit+17; index++ {
		if err := os.Mkdir(
			filepath.Join(namespace, fmt.Sprintf(".planning-orphan-%04d", index)), 0o700,
		); err != nil {
			t.Fatal(err)
		}
	}
	removed := 0
	for pass := 0; pass < CollectScanLimit; pass++ {
		result, err := runtime.CollectSupersededPlans(
			t.Context(), collectionChunk(t, state), CollectRemovalLimit,
		)
		if err != nil || result.Aborted {
			t.Fatalf("collection pass %d = %+v, %v", pass, result, err)
		}
		removed += result.Stages
		if result.Stages == 0 {
			break
		}
	}
	if removed != CollectScanLimit+17 {
		t.Fatalf("removed crash stages = %d, want %d", removed, CollectScanLimit+17)
	}
	for name := range namespaceNames(t, runtime, repository) {
		if strings.HasPrefix(name, ".planning-") {
			t.Fatalf("crash stage survived bounded convergence: %q", name)
		}
	}
}

func TestCollectPreservesRegisteredPlanningStage(t *testing.T) {
	runtime, state, repository, _ := supersededFixture(t)
	stage := filepath.Join(planNamespace(t, runtime, repository), ".planning-live")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	cleanup := runtime.trackPlanningStage(stage)
	defer cleanup()
	if _, err := runtime.CollectSupersededPlans(
		t.Context(), collectionChunk(t, state), 16,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("registered planning stage was collected: %v", err)
	}
}

func TestBuildAdmissionWaitsForCollectorMutationFence(t *testing.T) {
	runtime, _, repository, generation := supersededFixture(t)
	runtime.planMutation.Lock()
	type admission struct {
		release func()
		err     error
	}
	result := make(chan admission, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		release, err := runtime.beginBuild(repository, generation)
		result <- admission{release: release, err: err}
	}()
	<-started
	select {
	case admitted := <-result:
		runtime.planMutation.Unlock()
		if admitted.release != nil {
			admitted.release()
		}
		t.Fatal("build crossed the destructive namespace fence")
	case <-time.After(25 * time.Millisecond):
	}
	runtime.planMutation.Unlock()
	select {
	case admitted := <-result:
		if admitted.err != nil {
			t.Fatal(admitted.err)
		}
		admitted.release()
	case <-time.After(time.Second):
		t.Fatal("build did not resume after the destructive namespace fence")
	}
}
