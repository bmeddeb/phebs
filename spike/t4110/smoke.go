package t4110

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/spike/t411"
)

// SmokeReport is deliberately not a T41.10 receipt. It proves only that the
// small frozen transition corpus traverses the public v3 catalog, state-plan,
// activation, and reader seams with clean local-store teardown.
type SmokeReport struct {
	Revisions        int
	FinalServices    int
	FinalAccepted    int
	ReaddIncarnation uint64
	StoreClosed      bool
	CustodyRemoved   bool
}

// RunSmoke executes the cheap transition shape without authoring evidence or
// claiming any part of the 10,000-service gate.
func RunSmoke(ctx context.Context) (_ SmokeReport, retErr error) {
	root, err := os.MkdirTemp("", "phebs-t4110-smoke-")
	if err != nil {
		return SmokeReport{}, err
	}
	removed := false
	defer func() {
		if !removed {
			retErr = errors.Join(retErr, os.RemoveAll(root))
		}
	}()

	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return SmokeReport{}, fmt.Errorf("create smoke data directory: %w", err)
	}
	state, err := store.OpenLocalMemory(ctx, dataDir)
	if err != nil {
		return SmokeReport{}, fmt.Errorf("open smoke store: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, closeLiveStore(state))
		}
	}()

	const repository = "neutral.invalid/t4110/smoke"
	commit := strings.Repeat("a", 40)
	if err := state.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://neutral.invalid/t4110/smoke.git",
	}); err != nil {
		return SmokeReport{}, fmt.Errorf("seed smoke repository: %w", err)
	}
	if err := state.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		return SmokeReport{}, fmt.Errorf("bind smoke repository revision: %w", err)
	}

	corpus, err := t411.BuildTransitionCorpus()
	if err != nil {
		return SmokeReport{}, err
	}
	search := "sha256:" + strings.Repeat("b", 64)
	for index, revision := range corpus.Profile.Revisions {
		generation, buildErr := servicecatalogv3.Build(servicecatalogv3.Binding{
			Repository: repository,
			Source: servicecatalogv3.Source{
				Kind: servicecatalog.SourceOperator,
				Path: filepath.Join(root, "catalog.json"), Commit: commit,
				CensusDigest:      "sha256:" + strings.Repeat("c", 64),
				FileCount:         len(revision.Catalog.Memberships),
				AcceptedFileCount: len(revision.Catalog.Memberships),
			},
			Authority: revision.Catalog.Authority,
		}, revision.Catalog)
		if buildErr != nil {
			return SmokeReport{}, fmt.Errorf("build smoke revision %d: %w", index, buildErr)
		}
		if err := state.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
			return SmokeReport{}, fmt.Errorf("publish smoke revision %d: %w", index, err)
		}
		reconcile, err := state.BeginServiceStateV3Reconcile(ctx, repository)
		if err != nil {
			return SmokeReport{}, fmt.Errorf("begin smoke reconcile %d: %w", index, err)
		}
		if _, err := runServiceStatePlan(
			ctx, state, reconcile, fmt.Sprintf("t4110-smoke-reconcile-%d", index),
		); err != nil {
			return SmokeReport{}, err
		}
		activation, err := state.BeginServiceStateV3Activation(ctx, repository, search)
		if err != nil {
			return SmokeReport{}, fmt.Errorf("begin smoke activation %d: %w", index, err)
		}
		if _, err := runServiceStatePlan(
			ctx, state, activation, fmt.Sprintf("t4110-smoke-activate-%d", index),
		); err != nil {
			return SmokeReport{}, err
		}
	}

	cache := servicecatalogv3.NewDefaultReadCache()
	reader, err := store.NewServiceStateV3Reader(state, cache)
	if err != nil {
		return SmokeReport{}, err
	}
	read, err := reader.OpenService(ctx, repository, "svc.readd")
	if err != nil {
		return SmokeReport{}, fmt.Errorf("open smoke re-added service: %w", err)
	}
	incarnation := read.Entry.State.Incarnation
	if err := reader.Confirm(ctx, read); err != nil {
		read.Close()
		return SmokeReport{}, fmt.Errorf("confirm smoke re-added service: %w", err)
	}
	read.Close()
	if incarnation != 2 {
		return SmokeReport{}, fmt.Errorf("smoke re-add incarnation = %d, want 2", incarnation)
	}
	summary, err := state.GetServiceStateV3Summary(ctx, repository)
	if err != nil {
		return SmokeReport{}, fmt.Errorf("read smoke state summary: %w", err)
	}
	if summary.LiveServiceCount != 4 || summary.CurrentCount != 2 ||
		summary.StaleCount != 0 || summary.UnavailableCount != 1 ||
		summary.ConflictCount != 1 {
		return SmokeReport{}, fmt.Errorf(
			"smoke final state summary live=%d current=%d stale=%d unavailable=%d conflict=%d",
			summary.LiveServiceCount,
			summary.CurrentCount,
			summary.StaleCount,
			summary.UnavailableCount,
			summary.ConflictCount,
		)
	}
	accepted, err := state.ListAcceptedServiceStateV3Rows(ctx, repository, 6)
	if err != nil {
		return SmokeReport{}, fmt.Errorf("read smoke accepted states: %w", err)
	}
	if len(accepted) != 2 {
		return SmokeReport{}, fmt.Errorf("smoke accepted states = %d, want 2", len(accepted))
	}

	if err := closeLiveStore(state); err != nil {
		return SmokeReport{}, fmt.Errorf("close smoke store: %w", err)
	}
	closed = true
	if err := os.RemoveAll(root); err != nil {
		return SmokeReport{}, fmt.Errorf("remove smoke custody: %w", err)
	}
	removed = true
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		return SmokeReport{}, errors.New("smoke custody remained after teardown")
	}
	return SmokeReport{
		Revisions: len(corpus.Profile.Revisions), FinalServices: summary.CatalogServiceCount,
		FinalAccepted: len(accepted), ReaddIncarnation: incarnation,
		StoreClosed: true, CustodyRemoved: true,
	}, nil
}
