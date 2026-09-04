package relationshippublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestPublicationTransitionV3HitAndStartupRecovery(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/return-a"
	publishPins := &testPublishPinsV3{}
	priorRoot := publishLifecycleGenerationV3(
		t, root, repository, "b", "prior-run", publishPins,
	)
	prior, err := ReadPointerV3(t.Context(), root, repository)
	if err != nil {
		t.Fatal(err)
	}
	prepared := prepareLifecycleGenerationV3(t, root, repository, "c", "target-run")
	targetRoot := prepared.Root()
	binding, chunk := publicationTransitionRuntimeFixtureV3(t, repository)
	planDigest, scheduleDigest, err := publicationTransitionRuntimeIdentityV3(chunk, binding)
	if err != nil {
		t.Fatal(err)
	}

	crash := errors.New("injected marker interruption")
	var hitTarget PublicationTransitionTargetV3
	publication, err := publishV3(
		t.Context(), prepared, publishPins,
		func(ctx context.Context, marker MarkerV3) error {
			var targetErr error
			hitTarget, targetErr = publicationTransitionTargetV3(
				prior, marker, planDigest, scheduleDigest,
			)
			if targetErr != nil {
				return targetErr
			}
			snapshot, counts, readErr := readPublicationTransitionTestV3(
				t, ctx, root, hitTarget.Request,
			)
			if readErr != nil {
				return readErr
			}
			if counts != (readaccounting.Counts{
				ControlFileReads: PublicationTransitionControlFileReadsV3,
			}) || snapshot.Point != PublicationTransitionHitV3 ||
				snapshot.PriorGenerationDigest != priorRoot.GenerationDigest ||
				snapshot.PriorRootDigest != priorRoot.Digest ||
				snapshot.TargetGenerationDigest != targetRoot.GenerationDigest ||
				snapshot.TargetRootDigest != targetRoot.Digest ||
				snapshot.TargetAuthorityDigest != targetRoot.AuthorityDigest {
				t.Fatalf("hit snapshot/counts = %+v %+v", snapshot, counts)
			}
			return crash
		},
	)
	if publication != nil || !errors.Is(err, crash) {
		t.Fatalf("interrupted publication = %+v, %v", publication, err)
	}
	if hitTarget.PlanDigest != binding.TargetGeneration ||
		hitTarget.ScheduleDigest != chunk.ScheduleDigest ||
		hitTarget.Request.TargetGenerationDigest != targetRoot.GenerationDigest ||
		hitTarget.Request.TargetRootDigest != targetRoot.Digest {
		t.Fatalf("runtime transition target = %+v", hitTarget)
	}

	recoveryRequest := hitTarget.Request
	recoveryRequest.Point = PublicationTransitionRecoveredV3
	recoveryReported := ErrInvalid
	recoveryPins := &fakeRecoveryPinsV3{}
	var observedRecovery PublicationTransitionRecoveryTargetV3
	report, err := RecoverAllWithV3TransitionObserver(
		t.Context(), dataDir, recoveryPins,
		func(ctx context.Context, target PublicationTransitionRecoveryTargetV3) error {
			observedRecovery = target
			snapshot, counts, readErr := readPublicationTransitionTestV3(
				t, ctx, root, recoveryRequest,
			)
			if readErr != nil {
				return readErr
			}
			if counts != (readaccounting.Counts{
				ControlFileReads: PublicationTransitionControlFileReadsV3,
			}) || snapshot.Point != PublicationTransitionRecoveredV3 ||
				snapshot.TargetGenerationDigest != targetRoot.GenerationDigest ||
				snapshot.TargetRootDigest != targetRoot.Digest {
				t.Fatalf("recovered snapshot/counts = %+v %+v", snapshot, counts)
			}
			return recoveryReported
		},
	)
	if !errors.Is(err, recoveryReported) || report.Completed != 0 {
		t.Fatalf("observed startup recovery = %+v, %v", report, err)
	}
	if observedRecovery.Repository != repository ||
		observedRecovery.TargetGenerationDigest != targetRoot.GenerationDigest ||
		observedRecovery.TargetRootDigest != targetRoot.Digest ||
		observedRecovery.FormerStageName != hitTarget.Request.FormerStageName {
		t.Fatalf("recovery target = %+v", observedRecovery)
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(base, "publishing.json"),
		filepath.Join(base, "publishing.json.tmp"),
		filepath.Join(base, hitTarget.Request.FormerStageName),
	} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("recovery residue %q = %v", filepath.Base(path), statErr)
		}
	}
}

func TestPublicationTransitionV3RefusesMetadataResidue(t *testing.T) {
	root := t.TempDir()
	repository := "example.com/acme/return-a-residue"
	pins := &testPublishPinsV3{}
	prior := publishLifecycleGenerationV3(t, root, repository, "b", "prior-run", pins)
	prepared := prepareLifecycleGenerationV3(t, root, repository, "c", "target-run")
	crash := errors.New("stop")
	var request PublicationTransitionRequestV3
	_, err := publishV3(t.Context(), prepared, pins, func(_ context.Context, marker MarkerV3) error {
		request = PublicationTransitionRequestV3{
			Point: PublicationTransitionHitV3, Repository: repository,
			PriorGenerationDigest: prior.GenerationDigest, PriorRootDigest: prior.Digest,
			TargetGenerationDigest: marker.Pointer.GenerationDigest,
			TargetRootDigest:       marker.Pointer.RootDigest, FormerStageName: marker.StageName,
		}
		return crash
	})
	if !errors.Is(err, crash) {
		t.Fatal(err)
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, residue := range []string{request.FormerStageName, "publishing.json.tmp"} {
		path := filepath.Join(base, residue)
		if err := os.WriteFile(path, []byte("residue"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, counts, readErr := readPublicationTransitionTestV3(t, t.Context(), root, request)
		if !errors.Is(readErr, ErrInvalid) || counts != (readaccounting.Counts{
			ControlFileReads: PublicationTransitionControlFileReadsV3,
		}) {
			t.Fatalf("residue %q read = %+v, %v", residue, counts, readErr)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeV3MarkerInstallObserver(t *testing.T) {
	fixture := newRuntimeHandleCleanupFixture(t)
	if err := fixture.runtime.Handle(t.Context(), fixture.chunk); err != nil {
		t.Fatal(err)
	}
	repository := fixture.chunk.Repository
	root := fixture.runtime.relationshipRoot()
	priorRoot := publishLifecycleGenerationV3(
		t, root, repository, "b", "prior-run", &testPublishPinsV3{},
	)
	prior, err := ReadPointerV3(t.Context(), root, repository)
	if err != nil {
		t.Fatal(err)
	}
	catalog, generation := relationshipCatalogV3Test(t, repository, 2)
	states, summary := relationshipStatesV3Test(t, generation.Root, catalog)
	state := &runtimeTestStoreV3{
		runtimeHandleCleanupStore: fixture.store,
		candidate: &store.ServiceCatalogV3Candidate{
			Generation: generation, ControlRevision: summary.CatalogControlRevision,
			PublishedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		},
		summary: summary, states: states,
	}
	fixture.runtime.Store = state
	if current, err := fixture.runtime.ReconcileV3(t.Context(), repository); err != nil || current {
		t.Fatalf("v3 transition reconcile current=%t err=%v", current, err)
	}
	if len(state.enqueues) != 1 {
		t.Fatalf("v3 transition schedules = %d", len(state.enqueues))
	}
	binding, err := fixture.runtime.readRuntimeBindingV3(repository, state.enqueues[0].Generation)
	if err != nil {
		t.Fatal(err)
	}
	chunk := publicationTransitionClaimedChunkV3(t, binding)
	stop := errors.New("stop after marker observation")
	var observed PublicationTransitionTargetV3
	fixture.runtime.AfterV3MarkerInstall = func(ctx context.Context, target PublicationTransitionTargetV3) error {
		observed = target
		snapshot, counts, readErr := readPublicationTransitionTestV3(t, ctx, root, target.Request)
		if readErr != nil {
			return readErr
		}
		if counts != (readaccounting.Counts{ControlFileReads: PublicationTransitionControlFileReadsV3}) ||
			snapshot.PriorGenerationDigest != priorRoot.GenerationDigest ||
			snapshot.PriorRootDigest != priorRoot.Digest {
			t.Fatalf("runtime hit snapshot/counts = %+v %+v", snapshot, counts)
		}
		return stop
	}
	if err := fixture.runtime.HandleV3(t.Context(), chunk); !errors.Is(err, stop) {
		t.Fatalf("runtime marker observation = %v", err)
	}
	if observed.PlanDigest != binding.TargetGeneration || observed.ScheduleDigest != chunk.ScheduleDigest ||
		observed.Request.PriorGenerationDigest != prior.GenerationDigest ||
		observed.Request.PriorRootDigest != prior.RootDigest ||
		observed.Request.TargetGenerationDigest == prior.GenerationDigest ||
		observed.Request.TargetRootDigest == prior.RootDigest {
		t.Fatalf("runtime marker target = %+v", observed)
	}
	current, err := ReadPointerV3(t.Context(), root, repository)
	base, baseErr := RepositoryRootV3(root, repository)
	markerPresent := baseErr == nil && markerPresentV3(base)
	if err != nil || baseErr != nil || current != prior || !markerPresent {
		t.Fatalf("runtime marker custody current=%+v marker=%t err=%v", current,
			markerPresent, errors.Join(err, baseErr))
	}
}

func TestPublicationTransitionRuntimeIdentityV3IsExact(t *testing.T) {
	binding, chunk := publicationTransitionRuntimeFixtureV3(
		t, "example.com/acme/return-a-runtime",
	)
	tests := []struct {
		name   string
		mutate func(*runtimeBindingV3, *store.GenerationChunk)
	}{
		{"binding", func(binding *runtimeBindingV3, _ *store.GenerationChunk) {
			binding.TargetGeneration = fixedDigest("e")
		}},
		{"stage", func(_ *runtimeBindingV3, chunk *store.GenerationChunk) {
			chunk.Stage = ScheduleStage
		}},
		{"attempt", func(_ *runtimeBindingV3, chunk *store.GenerationChunk) {
			chunk.Attempt = 1
		}},
		{"priority", func(_ *runtimeBindingV3, chunk *store.GenerationChunk) {
			chunk.Priority = store.GenerationPriorityRetry
		}},
		{"schedule", func(_ *runtimeBindingV3, chunk *store.GenerationChunk) {
			chunk.ScheduleDigest = fixedDigest("d")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedBinding, changedChunk := binding, chunk
			test.mutate(&changedBinding, &changedChunk)
			if _, _, err := publicationTransitionRuntimeIdentityV3(
				changedChunk, changedBinding,
			); !errors.Is(err, ErrInvalid) {
				t.Fatalf("runtime identity error = %v", err)
			}
		})
	}
}

func readPublicationTransitionTestV3(
	t *testing.T,
	ctx context.Context,
	root string,
	request PublicationTransitionRequestV3,
) (PublicationTransitionSnapshotV3, readaccounting.Counts, error) {
	t.Helper()
	readCtx, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{
		ControlFileReads: PublicationTransitionControlFileReadsV3,
	})
	if err != nil {
		return PublicationTransitionSnapshotV3{}, readaccounting.Counts{}, err
	}
	snapshot, readErr := ReadPublicationTransitionV3(readCtx, root, request)
	counts, accountingErr := ledger.Finish()
	return snapshot, counts, errors.Join(readErr, accountingErr)
}

func publicationTransitionRuntimeFixtureV3(
	t *testing.T,
	repository string,
) (runtimeBindingV3, store.GenerationChunk) {
	t.Helper()
	policy := fixedDigest("f")
	target, err := runtimeTargetShadowV3(
		repository, fixedDigest("1"), fixedDigest("2"), fixedDigest("3"), 7,
		fixedDigest("4"), 9, policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := runtimeBindingV3{
		Schema: runtimeBindingSchemaV3, Repository: repository,
		ScheduleGeneration: fixedDigest("5"), TargetGeneration: target,
		SourceGeneration: fixedDigest("1"), SourceRoot: fixedDigest("2"),
		CatalogRoot: fixedDigest("3"), CatalogRevision: 7,
		StateSummary: fixedDigest("4"), StateRevision: 9, PolicyDigest: policy,
	}
	if err := setRuntimeBindingDigestV3(&binding); err != nil {
		t.Fatal(err)
	}
	return binding, publicationTransitionClaimedChunkV3(t, binding)
}

func publicationTransitionClaimedChunkV3(
	t *testing.T,
	binding runtimeBindingV3,
) store.GenerationChunk {
	t.Helper()
	schedule, err := store.GenerationScheduleDigest(store.GenerationScheduleSpec{
		Repository: binding.Repository, Stage: ScheduleStageV3,
		Generation: binding.ScheduleGeneration, ResourceClass: store.GenerationResourceMemory,
		TotalItems: 1, ChunkItems: 1, MaxAttempts: ScheduleMaxAttempts,
		RepositoryTokens: ScheduleRepositoryTokens,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed := time.Unix(1, 0).UTC()
	heartbeat := claimed.Add(time.Second)
	return store.GenerationChunk{
		ID: "chunk-record", Identity: publicationTransitionChunkIdentityV3(schedule, 0, 0),
		ScheduleDigest: schedule, Repository: binding.Repository, Stage: ScheduleStageV3,
		Generation: binding.ScheduleGeneration, ResourceClass: store.GenerationResourceMemory,
		Offset: 0, Length: 1, Attempt: 0, Priority: store.GenerationPriorityNeverRun,
		Status: store.GenerationChunkRunning, ClaimedBy: "return-a-worker",
		ClaimedAt: &claimed, HeartbeatAt: &heartbeat, LeaseToken: strings.Repeat("a", 32),
	}
}
