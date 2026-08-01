package callerexecute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/callerleafid"
	"github.com/bmeddeb/phebs/internal/callerpublication"
	"github.com/bmeddeb/phebs/internal/store"
)

type publicationReconcileTestStore struct {
	*workerTestStore
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
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
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
	if err := harness.worker.Handle(t.Context(), harness.job); err != nil {
		t.Fatal(err)
	}
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
