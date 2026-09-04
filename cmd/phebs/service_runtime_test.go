package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/config"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/relationshippublication"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

func TestServiceRuntimeRejectsV2TargetWithoutHoldingV3(t *testing.T) {
	ctx := t.Context()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	const repository = "example.com/acme/runtime-v2-ceiling"
	commit := strings.Repeat("a", 40)
	if err := st.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "runtime-v2-ceiling", Version: "v1",
	}
	catalog := servicecatalog.Catalog{Schema: servicecatalog.Schema, Authority: authority}
	owner := servicecatalog.Service{
		Key: "owner", DisplayName: "Owner", Disposition: servicecatalog.DispositionRejected,
		Origin: servicecatalog.OriginBase, Reason: "renamed",
	}
	for index := 0; index <= servicecatalogv3.MaxServiceSuccessors; index++ {
		key := fmt.Sprintf("target-%04d", index)
		catalog.Services = append(catalog.Services, servicecatalog.Service{
			Key: key, DisplayName: key, Disposition: servicecatalog.DispositionAccepted,
			Origin: servicecatalog.OriginBase,
		})
		catalog.Memberships = append(catalog.Memberships, servicecatalog.Membership{
			ServiceKey: key, Path: fmt.Sprintf("target/%04d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		})
		owner.Successors = append(owner.Successors, key)
	}
	catalog.Services = append(catalog.Services, owner)
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: servicecatalog.SourceOperator, SourcePath: "/catalog.json",
		SourceCommit: commit, SourceCensusDigest: testRuntimeDigest("1"),
		SourceFileCount: 1, AcceptedFileCount: 1,
		Authority: authority, CatalogDigest: catalogDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishServiceCatalog(ctx, publication); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	controller := &serviceRuntimeController{store: st}
	if _, err := controller.v3HoldingGeneration(
		ctx, repository, current.GenerationDigest,
	); !errors.Is(err, servicecatalogv3.ErrLimit) {
		t.Fatalf("v2 holding admission = %v, want v3 limit", err)
	}
}

func TestServiceStateV3ChunkWaitsForMutationFence(t *testing.T) {
	st, err := store.OpenLocal(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	indexDir := t.TempDir()
	releaseBackup, err := focusedindex.AcquireBackupLock(t.Context(), indexDir)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			releaseBackup()
		}
	})
	attempted := make(chan struct{})
	controller := &serviceRuntimeController{
		store: st, selections: map[string]config.ServiceCatalog{},
		acquire: func(ctx context.Context) (func(), error) {
			close(attempted)
			return focusedindex.AcquireMutationLock(ctx, indexDir)
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := controller.ProcessServiceStateV3Chunk(
			t.Context(), store.GenerationChunk{Repository: "example.com/acme/runtime"},
		)
		done <- err
	}()
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("state chunk did not request mutation fence")
	}
	select {
	case err := <-done:
		t.Fatalf("state chunk crossed held mutation fence: %v", err)
	default:
	}
	releaseBackup()
	released = true
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("invalid state chunk unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("state chunk did not resume after mutation fence")
	}
}

func TestServiceRuntimeReportsOnlyFreshActivationTransitionCommit(t *testing.T) {
	var reported []store.GenerationChunk
	controller := &serviceRuntimeController{
		afterActivationTransitionCommit: func(_ context.Context, chunk store.GenerationChunk) {
			reported = append(reported, chunk)
		},
	}
	target := store.GenerationChunk{
		Stage:  store.ServiceStateV3ActivateStage,
		Offset: store.ServiceStateV3ActivationTransitionTargetOffset,
	}
	controller.reportActivationTransitionCommit(
		t.Context(), target, store.ServiceStateV3ChunkResult{
			Applied: 1, Read: store.MaxServiceStateV3ChunkRows,
		},
	)
	exact := store.ServiceStateV3ChunkResult{Applied: 1, Read: store.MaxServiceStateV3ChunkRows}
	for _, changed := range []struct {
		chunk  store.GenerationChunk
		result store.ServiceStateV3ChunkResult
	}{
		{chunk: store.GenerationChunk{Stage: store.ServiceStateV3ReconcileStage, Offset: target.Offset}, result: exact},
		{chunk: store.GenerationChunk{Stage: target.Stage, Offset: target.Offset - 1}, result: exact},
		{chunk: store.GenerationChunk{Stage: target.Stage, Offset: target.Offset, Attempt: 1}, result: exact},
		{chunk: target},
		{chunk: target, result: store.ServiceStateV3ChunkResult{Read: store.MaxServiceStateV3ChunkRows}},
		{chunk: target, result: store.ServiceStateV3ChunkResult{Applied: 2, Read: store.MaxServiceStateV3ChunkRows}},
		{chunk: target, result: store.ServiceStateV3ChunkResult{Applied: 1, Read: store.MaxServiceStateV3ChunkRows - 1}},
	} {
		controller.reportActivationTransitionCommit(t.Context(), changed.chunk, changed.result)
	}
	if len(reported) != 1 || reported[0] != target {
		t.Fatalf("activation transition reports = %+v", reported)
	}
}

func TestServiceRuntimeReportsCommittedActivationTransition(t *testing.T) {
	ctx := t.Context()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	const repository = "example.com/acme/runtime-transition"
	commit := strings.Repeat("7", 40)
	if err := st.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repository, commit, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	services := make([]servicecatalog.Service, 5_121)
	memberships := make([]servicecatalog.Membership, len(services))
	for index := range services {
		key := fmt.Sprintf("service-%05d", index)
		services[index] = servicecatalog.Service{
			Key: key, DisplayName: fmt.Sprintf("Service %d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
		memberships[index] = servicecatalog.Membership{
			ServiceKey: key, Path: fmt.Sprintf("svc/%05d", index),
			Role: servicecatalog.RolePrimary, Origin: servicecatalog.OriginBase,
		}
	}
	build := func(version string, values []servicecatalog.Service) servicecatalogv3.Generation {
		t.Helper()
		authority := servicecatalog.Authority{
			Kind: servicecatalog.AuthorityOperator, ID: "runtime-transition", Version: version,
		}
		generation, buildErr := servicecatalogv3.Build(servicecatalogv3.Binding{
			Repository: repository,
			Source: servicecatalogv3.Source{
				Kind: servicecatalog.SourceOperator, Path: "/catalog.json", Commit: commit,
				CensusDigest: testRuntimeDigest("c"), FileCount: 1, AcceptedFileCount: 1,
			},
			Authority: authority,
		}, servicecatalog.Catalog{
			Schema: servicecatalog.Schema, Authority: authority,
			Services: values, Memberships: memberships,
		})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		return generation
	}
	expand := func(begin store.ServiceStateV3Begin) {
		t.Helper()
		schedule := begin.Schedule
		for schedule.NextOffset < schedule.TotalItems {
			schedule, err = st.ExpandGenerationSchedule(
				ctx, schedule.Repository, schedule.Stage, schedule.Generation,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	run := func(begin store.ServiceStateV3Begin) {
		t.Helper()
		expand(begin)
		for {
			chunk, claimErr := st.ClaimGenerationChunk(
				ctx, store.GenerationResourceCPU, "runtime-transition-setup",
			)
			if claimErr != nil {
				t.Fatal(claimErr)
			}
			if _, processErr := st.ProcessServiceStateV3Chunk(ctx, *chunk); processErr != nil {
				t.Fatal(processErr)
			}
			if completeErr := st.CompleteGenerationChunk(ctx, *chunk); completeErr != nil {
				t.Fatal(completeErr)
			}
			schedule, scheduleErr := st.GetGenerationSchedule(ctx, chunk.Repository, chunk.Stage)
			if scheduleErr != nil {
				t.Fatal(scheduleErr)
			}
			if schedule.Status == store.GenerationScheduleSettled {
				return
			}
		}
	}

	generationA := build("a", services)
	if err := st.PublishServiceCatalogV3Candidate(ctx, generationA); err != nil {
		t.Fatal(err)
	}
	reconcile, err := st.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	run(reconcile)
	search := testRuntimeDigest("8")
	activation, err := st.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	run(activation)

	servicesB := slices.Clone(services)
	servicesB[5_000].DisplayName = "Service 5000 B"
	generationB := build("b", servicesB)
	if err := st.PublishServiceCatalogV3Candidate(ctx, generationB); err != nil {
		t.Fatal(err)
	}
	reconcile, err = st.BeginServiceStateV3Reconcile(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	run(reconcile)
	activation, err = st.BeginServiceStateV3Activation(ctx, repository, search)
	if err != nil {
		t.Fatal(err)
	}
	expand(activation)
	for offset := int64(0); offset < store.ServiceStateV3ActivationTransitionTargetOffset; offset++ {
		chunk, claimErr := st.ClaimGenerationChunk(ctx, store.GenerationResourceCPU, "runtime-transition")
		if claimErr != nil || chunk.Offset != offset {
			t.Fatalf("claim activation offset %d = %+v, %v", offset, chunk, claimErr)
		}
		if _, processErr := st.ProcessServiceStateV3Chunk(ctx, *chunk); processErr != nil {
			t.Fatal(processErr)
		}
		if completeErr := st.CompleteGenerationChunk(ctx, *chunk); completeErr != nil {
			t.Fatal(completeErr)
		}
	}
	target, err := st.ClaimGenerationChunk(ctx, store.GenerationResourceCPU, "runtime-transition")
	if err != nil || target.Offset != store.ServiceStateV3ActivationTransitionTargetOffset {
		t.Fatalf("claim activation target = %+v, %v", target, err)
	}

	locked := false
	reports := 0
	controller := &serviceRuntimeController{
		store: st, selections: map[string]config.ServiceCatalog{},
		acquire: func(context.Context) (func(), error) {
			if locked {
				t.Fatal("transition lock was reacquired")
			}
			locked = true
			return func() { locked = false }, nil
		},
		afterActivationTransitionCommit: func(callbackCtx context.Context, chunk store.GenerationChunk) {
			reports++
			if !locked || chunk.Identity != target.Identity {
				t.Fatalf("activation callback lost commit lock or target: locked=%t chunk=%+v", locked, chunk)
			}
			point, pointErr := st.GetServiceStateV3Point(
				callbackCtx, repository, servicesB[5_000].Key,
			)
			if pointErr != nil || point.DisplayName != servicesB[5_000].DisplayName ||
				point.ActiveCatalogGeneration != generationB.Root.Digest {
				t.Fatalf("activation callback preceded durable member commit: %+v, %v", point, pointErr)
			}
		},
	}
	result, err := controller.ProcessServiceStateV3Chunk(ctx, *target)
	if err != nil || result.Settled || result.Read != store.MaxServiceStateV3ChunkRows ||
		result.Applied != 1 || reports != 1 || locked {
		t.Fatalf("activation target = %+v, reports=%d, locked=%t, err=%v", result, reports, locked, err)
	}
	replay, err := controller.ProcessServiceStateV3Chunk(ctx, *target)
	if err != nil || replay.Settled || replay.Read != 0 || replay.Applied != 0 ||
		reports != 1 || locked {
		t.Fatalf("activation replay = %+v, reports=%d, locked=%t, err=%v", replay, reports, locked, err)
	}
}

func TestServiceRuntimeRepairsHoldingFromSelectedV2AfterIndexedAdvance(
	t *testing.T,
) {
	ctx := t.Context()
	st, err := store.OpenLocal(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	const repository = "example.com/acme/runtime-crash-recovery"
	commitB, commitC := strings.Repeat("b", 40), strings.Repeat("c", 40)
	if err := st.UpsertRepo(ctx, store.Repo{
		Name: repository, CloneURL: "https://" + repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepoIndexed(ctx, repository, commitB, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	authority := servicecatalog.Authority{
		Kind: servicecatalog.AuthorityOperator, ID: "runtime-recovery", Version: "v1",
	}
	catalog := servicecatalog.Catalog{
		Schema: servicecatalog.Schema, Authority: authority,
		Services: []servicecatalog.Service{{
			Key: "orders", DisplayName: "Orders",
			Disposition: servicecatalog.DispositionAccepted,
			Origin:      servicecatalog.OriginBase,
		}},
		Memberships: []servicecatalog.Membership{{
			ServiceKey: "orders", Path: "svc", Role: servicecatalog.RolePrimary,
			Origin: servicecatalog.OriginBase,
		}},
		Unowned: []servicecatalog.UnownedPlacement{},
	}
	canonical, err := servicecatalog.Canonical(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := servicecatalog.Digest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication := servicecatalog.Publication{
		Schema: servicecatalog.PublicationSchema, Repository: repository,
		SourceKind: servicecatalog.SourceOperator, SourcePath: "/catalog.json",
		SourceCommit: commitB, SourceCensusDigest: testRuntimeDigest("1"),
		SourceFileCount: 1, AcceptedFileCount: 1,
		Authority: authority, CatalogDigest: catalogDigest, Canonical: canonical,
	}
	publication.GenerationDigest, err = servicecatalog.PublicationGenerationDigest(publication)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PublishServiceCatalog(ctx, publication); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetServiceCatalog(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReconcileServiceStates(ctx, *current); err != nil {
		t.Fatal(err)
	}
	source, err := servicecatalog.SourceGenerationDigest(*current)
	if err != nil {
		t.Fatal(err)
	}
	searchB := testRuntimeDigest("2")
	if _, err := st.ActivateServiceGeneration(
		ctx, repository, current.GenerationDigest, source, searchB,
	); err != nil {
		t.Fatal(err)
	}
	summary, err := st.GetServiceStateSummary(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := st.SelectServiceRuntimeV2(
		ctx, store.ServiceRuntimeSelectionRequest{
			Repository: repository,
			Target: store.ServiceRuntimeTarget{
				CatalogGenerationDigest:      current.GenerationDigest,
				CatalogControlRevision:       current.ControlRevision,
				StateControlRevision:         summary.ControlRevision,
				StateSummaryDigest:           summary.SummaryDigest,
				SearchGenerationDigest:       searchB,
				RelationshipGenerationDigest: testRuntimeDigest("3"),
				RelationshipRootDigest:       testRuntimeDigest("4"),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	disabled := &serviceRuntimeController{
		store: st,
		acquire: func(context.Context) (func(), error) {
			return func() {}, nil
		},
	}
	if err := disabled.PinSelections(ctx); !errors.Is(
		err, errServiceRuntimeExtractionUnavailable,
	) {
		t.Fatalf("selected runtime with disabled extraction = %v", err)
	}
	if err := st.SetRepoIndexed(ctx, repository, commitC, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	controller := &serviceRuntimeController{
		dataDir: t.TempDir(), store: st,
		relationship: &relationshippublication.Runtime{},
	}
	if _, err := controller.prepareV3HoldingLocked(
		ctx, repository, &selected,
	); !errors.Is(err, errServiceRuntimePending) {
		t.Fatalf("first holding repair = %v", err)
	}
	historical, err := st.GetServiceCatalogGeneration(
		ctx, repository, selected.CatalogGenerationDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	want, err := servicecatalogv3.FromV2(*historical, catalog)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetServiceCatalogV3CandidateRoot(ctx, repository)
	if err != nil || got.Root.Digest != want.Root.Digest ||
		got.Root.Binding.Source.Commit != commitB {
		t.Fatalf("recovered holding candidate = %+v, %v", got, err)
	}
}

func testRuntimeDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64)
}
