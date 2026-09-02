package relationshippublication

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/servicecatalogv3"
	"github.com/bmeddeb/phebs/internal/store"
)

type fakeRecoveryPinsV3 struct {
	fakeRecoveryPins
	relationshipPins  int
	catalogPins       int
	catalogReconciles int
	references        []store.ServiceCatalogV3RelationshipReference
	recovered         []store.ServiceCatalogV3RelationshipReference
	catalogPinErr     error
	cancel            context.CancelFunc
}

type historicalMarkerPinsV3 struct {
	catalog *store.Surreal
	runs    []string
}

func (pins *historicalMarkerPinsV3) RecoverRelationshipPublicationV3(
	ctx context.Context,
	repository, generation, root, catalogRoot string,
	catalogRevision, stateRevision uint64,
	stateSummary string,
) error {
	return pins.catalog.RecoverRelationshipPublicationV3(
		ctx, repository, generation, root, catalogRoot,
		catalogRevision, stateRevision, stateSummary,
	)
}

func (pins *historicalMarkerPinsV3) PinPartitionedExtractionRun(
	_ context.Context, runID, owner string,
) error {
	pins.runs = append(pins.runs, runID+"\x00"+owner)
	return nil
}

func (pins *fakeRecoveryPinsV3) PinPartitionedExtractionRun(
	ctx context.Context, runID, owner string,
) error {
	if pins.cancel != nil {
		pins.cancel()
		return context.Canceled
	}
	return pins.fakeRecoveryPins.PinPartitionedExtractionRun(ctx, runID, owner)
}

func (pins *fakeRecoveryPinsV3) RecoverRelationshipPublicationV3(
	_ context.Context,
	_, _, _, _ string,
	_, _ uint64,
	_ string,
) error {
	pins.relationshipPins++
	return nil
}

func (pins *fakeRecoveryPinsV3) ReconcileServiceCatalogV3RelationshipReferences(
	_ context.Context,
	references []store.ServiceCatalogV3RelationshipReference,
) error {
	pins.catalogReconciles++
	pins.references = slices.Clone(references)
	return nil
}

func (pins *fakeRecoveryPinsV3) RecoverServiceCatalogV3RelationshipReference(
	_ context.Context,
	reference store.ServiceCatalogV3RelationshipReference,
) error {
	pins.catalogPins++
	if pins.catalogPinErr != nil {
		return pins.catalogPinErr
	}
	pins.recovered = append(pins.recovered, reference)
	return nil
}

func TestRecoverAllRepinsV3AndReconcilesBothNamespaces(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	pins := &fakeRecoveryPinsV3{}

	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if err != nil || report.Repositories != 1 || report.Invalid != 0 ||
		pins.reconciles != 1 || pins.catalogPins != 1 || pins.catalogReconciles != 1 || len(pins.owners) != 2 ||
		len(pins.pins) != 2 || len(pins.references) != 1 {
		t.Fatalf("combined v2/v3 recovery = %+v pins=%+v err=%v", report, pins, err)
	}
	root := publication.Root()
	want := store.ServiceCatalogV3RelationshipReference{
		Repository:                   fixture.repository,
		RelationshipGenerationDigest: root.GenerationDigest,
		RelationshipRootDigest:       root.Digest,
		CatalogRootDigest:            root.Authority.CatalogRootDigest,
		CatalogControlRevision:       root.Authority.CatalogControlRevision,
		StateControlRevision:         root.Authority.ServiceStateControlRevision,
		StateSummaryDigest:           root.Authority.ServiceStateSummaryDigest,
	}
	if pins.references[0] != want ||
		!slices.Contains(pins.owners, "relationship:"+root.GenerationDigest) {
		t.Fatalf("v3 recovery reference = %+v owners=%q, want %+v", pins.references[0], pins.owners, want)
	}
}

func TestRecoverAllRemovesUncommittedV3TemporaryAndStage(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	repository := "example.com/acme/v3-uncommitted-stage"
	prepared := preparedRelationshipV3Test(t, root, repository)
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(base, "publishing.json.tmp")
	if err := os.WriteFile(temporary, []byte("uncommitted"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared.closed = true
	pins := &fakeRecoveryPinsV3{
		fakeRecoveryPins: fakeRecoveryPins{owners: []string{"relationship:orphan"}},
		references: []store.ServiceCatalogV3RelationshipReference{{
			RelationshipGenerationDigest: fixedDigest("f"),
		}},
	}

	report, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil || report.Repositories != 0 || report.Invalid != 0 ||
		pins.reconciles != 1 || pins.catalogReconciles != 1 ||
		len(pins.owners) != 0 || len(pins.references) != 0 {
		t.Fatalf("uncommitted v3 cleanup = %+v pins=%+v err=%v", report, pins, err)
	}
	for _, path := range []string{temporary, prepared.directory, base} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("uncommitted recovery residue %q: %v", path, err)
		}
	}
}

func TestRecoverAllRepublishCrashPreservesCurrentV3GenerationAndPins(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-uncommitted-republish"
	current := publishLifecycleGenerationV3(
		t, root, repository, "5", "current-run", &lifecyclePinStoreV3{},
	)
	orphan := prepareLifecycleGenerationV3(
		t, root, repository, "6", "orphan-run",
	)
	if orphan.rootValue.GenerationDigest == current.GenerationDigest {
		t.Fatal("republish crash stage did not change generation identity")
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(base, "publishing.json.tmp")
	if err := os.WriteFile(temporary, []byte("uncommitted-republish"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphan.closed = true
	pins := &fakeRecoveryPinsV3{
		fakeRecoveryPins: fakeRecoveryPins{owners: []string{
			"relationship:" + orphan.rootValue.GenerationDigest,
		}},
		references: []store.ServiceCatalogV3RelationshipReference{{
			RelationshipGenerationDigest: orphan.rootValue.GenerationDigest,
		}},
	}

	report, err := RecoverAll(t.Context(), dataDir, pins)
	wantOwner := "relationship:" + current.GenerationDigest
	if err != nil || report.Repositories != 1 || report.Completed != 0 || report.Invalid != 0 ||
		pins.reconciles != 1 || pins.catalogReconciles != 1 || pins.catalogPins != 1 ||
		!slices.Equal(pins.owners, []string{wantOwner}) || len(pins.references) != 1 ||
		pins.references[0].RelationshipGenerationDigest != current.GenerationDigest {
		t.Fatalf("republish crash recovery = %+v pins=%+v err=%v", report, pins, err)
	}
	if !slices.Equal(pins.pins, []string{"current-run\x00" + wantOwner}) {
		t.Fatalf("republish current extraction pins = %q", pins.pins)
	}
	for _, path := range []string{temporary, orphan.directory} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("republish crash residue %q: %v", path, err)
		}
	}
	pointer, err := ReadPointerV3(t.Context(), root, repository)
	if err != nil || pointer.GenerationDigest != current.GenerationDigest ||
		pointer.RootDigest != current.Digest {
		t.Fatalf("republish current pointer = %+v, %v", pointer, err)
	}
	if _, err := ValidateGenerationV3(
		t.Context(), root, repository, current.GenerationDigest, current.Digest,
	); err != nil {
		t.Fatalf("republish current generation changed: %v", err)
	}
}

func TestRecoverAllDefersGlobalReconcileForReadableV3GenerationOutsideFloor(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/v3-readable-recovery-floor"
	publicationPins := &lifecyclePinStoreV3{}
	rootA := publishLifecycleGenerationV3(t, root, repository, "5", "run-a", publicationPins)
	rootB := publishLifecycleGenerationV3(t, root, repository, "6", "run-b", publicationPins)
	for index, value := range []RootV3{rootA, rootB} {
		directory := mustGenerationPathV3(t, root, repository, value.GenerationDigest)
		modified := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(directory, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	preparedC := prepareLifecycleGenerationV3(t, root, repository, "7", "run-c")
	pointerC, err := newPointerV3(preparedC.rootValue)
	if err != nil {
		t.Fatal(err)
	}
	_, markerRaw, _, err := publicationControlsV3(
		pointerC, filepath.Base(preparedC.directory),
	)
	if err != nil {
		t.Fatal(err)
	}
	base, err := RepositoryRootV3(root, repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}
	preparedC.closed = true
	referenceA := store.ServiceCatalogV3RelationshipReference{
		Repository: repository, RelationshipGenerationDigest: rootA.GenerationDigest,
		RelationshipRootDigest: rootA.Digest,
		CatalogRootDigest:      rootA.Authority.CatalogRootDigest,
		CatalogControlRevision: rootA.Authority.CatalogControlRevision,
		StateControlRevision:   rootA.Authority.ServiceStateControlRevision,
		StateSummaryDigest:     rootA.Authority.ServiceStateSummaryDigest,
	}
	ownerA := "relationship:" + rootA.GenerationDigest
	pins := &fakeRecoveryPinsV3{
		fakeRecoveryPins: fakeRecoveryPins{owners: []string{ownerA}},
		references:       []store.ServiceCatalogV3RelationshipReference{referenceA},
	}

	report, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil || report.Repositories != 1 || report.Completed != 1 || report.Invalid != 0 ||
		pins.reconciles != 0 || pins.catalogReconciles != 0 || pins.catalogPins != 2 ||
		!slices.Equal(pins.owners, []string{ownerA}) ||
		!slices.Equal(pins.references, []store.ServiceCatalogV3RelationshipReference{referenceA}) {
		t.Fatalf("out-of-floor recovery defer = %+v pins=%+v err=%v", report, pins, err)
	}
	for _, generation := range []string{rootB.GenerationDigest, preparedC.rootValue.GenerationDigest} {
		if !slices.ContainsFunc(pins.recovered, func(
			reference store.ServiceCatalogV3RelationshipReference,
		) bool {
			return reference.RelationshipGenerationDigest == generation
		}) {
			t.Fatalf("protected generation %q was not add-only reconstructed: %+v", generation, pins.recovered)
		}
	}
	if slices.ContainsFunc(pins.recovered, func(
		reference store.ServiceCatalogV3RelationshipReference,
	) bool {
		return reference.RelationshipGenerationDigest == rootA.GenerationDigest
	}) {
		t.Fatalf("out-of-floor A was unexpectedly re-audited: %+v", pins.recovered)
	}

	sweep, err := SweepLifecycleV3(
		t.Context(), dataDir, time.Now().UTC(), "", &CacheV3{}, 8,
	)
	if err != nil || sweep.ReleasedPinOwner != ownerA || sweep.ReleasedRootV3 == nil ||
		sweep.ReleasedRootV3.GenerationDigest != rootA.GenerationDigest {
		t.Fatalf("out-of-floor lifecycle release = %+v, %v", sweep, err)
	}
	unpins := &lifecyclePinStoreV3{}
	if err := UnpinLifecycleV3(t.Context(), unpins, *sweep.ReleasedRootV3); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unpins.unpinnedRuns, []string{"run-a\x00" + ownerA}) ||
		len(unpins.unpinnedCatalogs) != 1 ||
		unpins.unpinnedCatalogs[0].generation != rootA.GenerationDigest {
		t.Fatalf("out-of-floor exact unpin = runs %q catalogs %+v", unpins.unpinnedRuns, unpins.unpinnedCatalogs)
	}
}

func TestRecoverAllDefersGlobalReconcileForReadableLegacyGenerationOutsideFloor(t *testing.T) {
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "relationships")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := "example.com/acme/legacy-readable-recovery-floor"
	rootA := publishRecoveryLegacyGeneration(t, root, repository, "5", "run-a")
	rootB := publishRecoveryLegacyGeneration(t, root, repository, "6", "run-b")
	rootC := publishRecoveryLegacyGeneration(t, root, repository, "7", "run-c")
	for index, value := range []Root{rootA, rootB, rootC} {
		directory := generationPath(root, repository, value.GenerationDigest)
		modified := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(directory, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	ownerA := "relationship:" + rootA.GenerationDigest
	pins := &fakeRecoveryPinsV3{
		fakeRecoveryPins: fakeRecoveryPins{owners: []string{ownerA}},
		references: []store.ServiceCatalogV3RelationshipReference{{
			RelationshipGenerationDigest: fixedDigest("f"),
		}},
	}

	report, err := RecoverAll(t.Context(), dataDir, pins)
	if err != nil || report.Repositories != 1 || report.Invalid != 0 ||
		pins.reconciles != 0 || pins.catalogReconciles != 0 ||
		!slices.Equal(pins.owners, []string{ownerA}) || len(pins.references) != 1 {
		t.Fatalf("legacy out-of-floor recovery defer = %+v pins=%+v err=%v", report, pins, err)
	}
	for _, value := range []Root{rootB, rootC} {
		want := value.Authority.Upstream.Domains[0].RunID + "\x00relationship:" + value.GenerationDigest
		if !slices.Contains(pins.pins, want) {
			t.Fatalf("protected legacy generation pin %q missing from %q", want, pins.pins)
		}
	}
	if slices.Contains(pins.pins, "run-a\x00"+ownerA) {
		t.Fatalf("out-of-floor legacy A was unexpectedly re-audited: %q", pins.pins)
	}

	sweep, err := SweepLifecycle(
		t.Context(), dataDir, time.Now().UTC(), "", &Cache{}, 8,
	)
	if err != nil || sweep.ReleasedPinOwner != ownerA {
		t.Fatalf("legacy out-of-floor lifecycle release = %+v, %v", sweep, err)
	}
}

func TestRecoverAllRejectsMissingOrNonDirectoryCurrentV3Generation(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "absent",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "file",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not-a-generation"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			root := filepath.Join(dataDir, "relationships")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			repository := "example.com/acme/v3-dangling-current-" + test.name
			current := publishLifecycleGenerationV3(
				t, root, repository, "5", "current-run", &lifecyclePinStoreV3{},
			)
			directory := mustGenerationPathV3(
				t, root, repository, current.GenerationDigest,
			)
			test.mutate(t, directory)
			owner := "relationship:" + current.GenerationDigest
			reference := store.ServiceCatalogV3RelationshipReference{
				Repository: repository, RelationshipGenerationDigest: current.GenerationDigest,
				RelationshipRootDigest: current.Digest,
				CatalogRootDigest:      current.Authority.CatalogRootDigest,
				CatalogControlRevision: current.Authority.CatalogControlRevision,
				StateControlRevision:   current.Authority.ServiceStateControlRevision,
				StateSummaryDigest:     current.Authority.ServiceStateSummaryDigest,
			}
			pins := &fakeRecoveryPinsV3{
				fakeRecoveryPins: fakeRecoveryPins{owners: []string{owner}},
				references:       []store.ServiceCatalogV3RelationshipReference{reference},
			}

			report, err := RecoverAll(t.Context(), dataDir, pins)
			if err != nil || report.Repositories != 1 || report.Invalid == 0 ||
				pins.reconciles != 0 || pins.catalogReconciles != 0 || pins.catalogPins != 0 ||
				len(pins.pins) != 0 || !slices.Equal(pins.owners, []string{owner}) ||
				!slices.Equal(pins.references, []store.ServiceCatalogV3RelationshipReference{reference}) {
				t.Fatalf("dangling current recovery = %+v pins=%+v err=%v", report, pins, err)
			}
		})
	}
}

func TestRepairStageDirectoriesChargesEachRemovalOnce(t *testing.T) {
	base := filepath.Join(t.TempDir(), "repository")
	stage := filepath.Join(base, ".stage-crash")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "partial.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, work, err := repairStageDirectories(t.Context(), base, 4)
	if err != nil || !removed || work != 4 {
		t.Fatalf("exact stage repair charge = removed %t work %d err %v", removed, work, err)
	}
	if _, err := os.Lstat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exactly budgeted stage repository remains: %v", err)
	}
}

func TestRecoverV3UsesHistoricalReferenceAfterCatalogAndStateAdvance(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	rootValue := publication.Root()
	root := filepath.Join(fixture.dataDir, "relationships")
	base, err := RepositoryRootV3(root, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	_, markerRaw, _, err := publicationControlsV3(publication.pointer, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(base, "publishing.json"), markerRaw); err != nil {
		t.Fatal(err)
	}

	openCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	catalogStore, err := store.OpenLocalMemory(openCtx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalogStore.Close(context.Background()) })
	catalogA, _, _ := archiveV3CatalogState(t, fixture.catalog)
	if err := catalogStore.UpsertRepo(t.Context(), store.Repo{
		Name: fixture.repository, CloneURL: "https://" + fixture.repository + ".git",
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.SetRepoIndexed(
		t.Context(), fixture.repository, catalogA.Root.Binding.Source.Commit, time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.PublishServiceCatalogV3Candidate(t.Context(), catalogA); err != nil {
		t.Fatal(err)
	}
	runRecoveryServiceStateV3Plan(t, catalogStore, fixture.repository)
	catalog, err := catalogA.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	bindingB := catalogA.Root.Binding
	bindingB.Source.CensusDigest = fixedDigest("0")
	catalogB, err := servicecatalogv3.Build(bindingB, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.PublishServiceCatalogV3Candidate(t.Context(), catalogB); err != nil {
		t.Fatal(err)
	}
	runRecoveryServiceStateV3Plan(t, catalogStore, fixture.repository)
	summary, err := catalogStore.GetServiceStateV3Summary(t.Context(), fixture.repository)
	if err != nil || summary.CatalogGeneration != catalogB.Root.Digest {
		t.Fatalf("advanced service state = %+v, %v", summary, err)
	}
	if err := catalogStore.PinRelationshipPublicationV3(
		t.Context(), fixture.repository, rootValue.GenerationDigest, rootValue.Digest,
		rootValue.Authority.CatalogRootDigest, rootValue.Authority.CatalogControlRevision,
		rootValue.Authority.ServiceStateControlRevision,
		rootValue.Authority.ServiceStateSummaryDigest,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("ordinary current-only pin accepted historical A: %v", err)
	}

	pins := &historicalMarkerPinsV3{catalog: catalogStore}
	recovered, err := RecoverV3(t.Context(), root, fixture.repository, pins)
	if err != nil || !recovered {
		t.Fatalf("historical marker recovery = %t, %v", recovered, err)
	}
	if _, err := os.Lstat(filepath.Join(base, "publishing.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("historical marker remains: %v", err)
	}
	pointer, err := ReadPointerV3(t.Context(), root, fixture.repository)
	if err != nil || pointer.GenerationDigest != rootValue.GenerationDigest ||
		pointer.RootDigest != rootValue.Digest {
		t.Fatalf("recovered A pointer = %+v, %v", pointer, err)
	}
	wantRun := "partition-run\x00relationship:" + rootValue.GenerationDigest
	if !slices.Equal(pins.runs, []string{wantRun}) {
		t.Fatalf("historical extraction repins = %q", pins.runs)
	}
	report, err := catalogStore.ValidateServiceCatalogV3Precious(t.Context())
	if err != nil || report.RelationshipReferences != 1 {
		t.Fatalf("historical A catalog reference = %+v, %v", report, err)
	}
	collision := store.ServiceCatalogV3RelationshipReference{
		Repository:                   fixture.repository,
		RelationshipGenerationDigest: rootValue.GenerationDigest,
		RelationshipRootDigest:       fixedDigest("f"),
		CatalogRootDigest:            rootValue.Authority.CatalogRootDigest,
		CatalogControlRevision:       rootValue.Authority.CatalogControlRevision,
		StateControlRevision:         rootValue.Authority.ServiceStateControlRevision,
		StateSummaryDigest:           rootValue.Authority.ServiceStateSummaryDigest,
	}
	if err := catalogStore.RecoverServiceCatalogV3RelationshipReference(
		t.Context(), collision,
	); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("recovered A identity was not exact: %v", err)
	}
}

func runRecoveryServiceStateV3Plan(
	t *testing.T,
	catalogStore *store.Surreal,
	repository string,
) {
	t.Helper()
	begin, err := catalogStore.BeginServiceStateV3Reconcile(t.Context(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if begin.Noop {
		return
	}
	if begin.Plan == nil || begin.Schedule == nil {
		t.Fatalf("service state reconcile did not return a plan: %+v", begin)
	}
	schedule := begin.Schedule
	for schedule.NextOffset < schedule.TotalItems {
		schedule, err = catalogStore.ExpandGenerationSchedule(
			t.Context(), schedule.Repository, schedule.Stage, schedule.Generation,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for {
		chunk, err := catalogStore.ClaimGenerationChunk(
			t.Context(), store.GenerationResourceCPU, "relationship-v3-recovery-test",
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = catalogStore.ProcessServiceStateV3Chunk(t.Context(), *chunk)
		if err != nil {
			t.Fatal(err)
		}
		if err := catalogStore.CompleteGenerationChunk(t.Context(), *chunk); err != nil {
			t.Fatal(err)
		}
		settled, err := catalogStore.GetGenerationSchedule(
			t.Context(), schedule.Repository, schedule.Stage,
		)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status == store.GenerationScheduleSettled {
			return
		}
	}
}

func TestRecoverAllPinsValidV3BeforeUnrelatedCorruptOmission(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	root := filepath.Join(fixture.dataDir, "relationships")
	corrupt, err := RepositoryRootV3(root, "example.com/acme/corrupt-v3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(corrupt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, "current.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}
	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if err != nil || report.Invalid == 0 || pins.catalogPins != 1 ||
		len(pins.recovered) != 1 || pins.catalogReconciles != 0 || pins.reconciles != 0 ||
		pins.recovered[0].RelationshipGenerationDigest != publication.Root().GenerationDigest {
		t.Fatalf("partial recovery pins = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestRecoverAllRepairsValidV3SiblingWhenLegacyControlIsCorrupt(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	legacyControl := filepath.Join(
		fixture.dataDir, "relationships", "relationship-publications",
		repositoryHash(fixture.repository), "current.json",
	)
	if err := os.WriteFile(legacyControl, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}

	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	wantOwner := "relationship:" + publication.Root().GenerationDigest
	if err != nil || report.Repositories != 1 || report.Invalid == 0 ||
		pins.catalogPins != 1 || len(pins.recovered) != 1 ||
		pins.recovered[0].RelationshipGenerationDigest != publication.Root().GenerationDigest ||
		!slices.Contains(pins.pins, "partition-run\x00"+wantOwner) ||
		pins.reconciles != 0 || pins.catalogReconciles != 0 {
		t.Fatalf("corrupt legacy with valid v3 sibling = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestRecoverAllRepairsValidLegacySiblingWhenV3ControlIsCorrupt(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	legacy, err := OpenCurrent(
		t.Context(), filepath.Join(fixture.dataDir, "relationships"), fixture.repository,
	)
	if err != nil {
		t.Fatal(err)
	}
	shadow := fixture.publishV3(t)
	if err := os.WriteFile(filepath.Join(shadow.base, "current.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}

	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	wantOwner := "relationship:" + legacy.Root().GenerationDigest
	if err != nil || report.Repositories != 1 || report.Invalid == 0 ||
		pins.catalogPins != 0 || len(pins.recovered) != 0 ||
		!slices.Contains(pins.pins, "partition-run\x00"+wantOwner) ||
		pins.reconciles != 0 || pins.catalogReconciles != 0 {
		t.Fatalf("corrupt v3 with valid legacy sibling = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestRecoverAllPropagatesCatalogReferenceRecoveryFailure(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	fixture.publishV3(t)
	sentinel := errors.New("catalog reference store unavailable")
	pins := &fakeRecoveryPinsV3{catalogPinErr: sentinel}
	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if !errors.Is(err, sentinel) || report.Invalid != 0 || pins.catalogPins != 1 ||
		pins.catalogReconciles != 0 || pins.reconciles != 0 {
		t.Fatalf("catalog pin failure = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestRecoverAllCorruptV3PreservesBothGlobalPinSets(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	root := publication.Root()
	if len(root.ServiceMembers) == 0 {
		t.Fatal("v3 fixture has no service member")
	}
	if err := os.WriteFile(
		filepath.Join(publication.directory, root.ServiceMembers[0].Name),
		[]byte("{}"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}
	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if err != nil || report.Invalid == 0 || pins.reconciles != 0 ||
		pins.catalogReconciles != 0 || len(pins.references) != 0 {
		t.Fatalf("corrupt v3 recovery = %+v pins=%+v err=%v", report, pins, err)
	}
	for _, directory := range []string{
		filepath.Join(fixture.dataDir, "relationship-resolver-namespaces"),
		filepath.Join(fixture.dataDir, "relationship-rpc-postings"),
		filepath.Join(fixture.dataDir, "relationship-kafka-postings"),
	} {
		if _, err := os.Lstat(directory); err != nil {
			t.Fatalf("shared component removed after corrupt shadow: %s: %v", directory, err)
		}
	}
}

func TestRecoverAllReconcilesEmptyV3ReferenceSet(t *testing.T) {
	pins := &fakeRecoveryPinsV3{}
	report, err := RecoverAll(t.Context(), t.TempDir(), pins)
	if err != nil || report != (RecoveryReport{}) || pins.reconciles != 1 ||
		pins.catalogReconciles != 1 || len(pins.references) != 0 {
		t.Fatalf("empty v3 recovery = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestRecoverAllPropagatesCancellation(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	fixture.publishV3(t)
	ctx, cancel := context.WithCancel(t.Context())
	pins := &fakeRecoveryPinsV3{cancel: cancel}

	report, err := RecoverAll(ctx, fixture.dataDir, pins)
	if !errors.Is(err, context.Canceled) || report.Invalid != 0 ||
		pins.reconciles != 0 || pins.catalogReconciles != 0 {
		t.Fatalf("cancelled recovery = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestDerivedRecoveryOmissionClassification(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		want  bool
	}{
		{name: "invalid", cause: ErrInvalid, want: true},
		{name: "not found", cause: ErrNotFound, want: true},
		{name: "absent path", cause: &os.PathError{Op: "open", Path: "root.json", Err: os.ErrNotExist}, want: true},
		{name: "joined derived", cause: errors.Join(ErrInvalid, ErrNotFound), want: true},
		{name: "permission", cause: &os.PathError{Op: "open", Path: "root.json", Err: os.ErrPermission}},
		{name: "operational", cause: errors.New("input/output failure")},
		{name: "joined operational", cause: errors.Join(ErrInvalid, errors.New("input/output failure"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isDerivedRecoveryOmission(test.cause); got != test.want {
				t.Fatalf("isDerivedRecoveryOmission(%v) = %t, want %t", test.cause, got, test.want)
			}
		})
	}
}

func TestRecoveryDirectoryExistenceClassifiesAbsenceShapeAndOperationalErrors(t *testing.T) {
	root := t.TempDir()
	if exists, err := directoryExists(root); err != nil || !exists {
		t.Fatalf("existing recovery directory = %t, %v", exists, err)
	}
	if exists, err := directoryExists(filepath.Join(root, "absent")); err != nil || exists {
		t.Fatalf("absent recovery directory = %t, %v", exists, err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exists, err := directoryExists(file); exists || !errors.Is(err, ErrInvalid) {
		t.Fatalf("file-shaped recovery directory = %t, %v", exists, err)
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(t.TempDir(), symlink); err != nil {
		t.Fatal(err)
	}
	if exists, err := directoryExists(symlink); exists || !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlink-shaped recovery directory = %t, %v", exists, err)
	}
	if exists, err := directoryExists(filepath.Join(file, "child")); exists || err == nil || isDerivedRecoveryOmission(err) {
		t.Fatalf("operational recovery stat error = %t, %v", exists, err)
	}
}

func TestRecoveryFatalPropagatesOperationalErrors(t *testing.T) {
	sentinel := errors.New("operational failure")
	if got := recoveryFatal(t.Context(), sentinel); !errors.Is(got, sentinel) {
		t.Fatalf("operational recovery error = %v, want %v", got, sentinel)
	}
	if got := recoveryFatal(t.Context(), ErrInvalid); got != nil {
		t.Fatalf("derived corruption = %v, want nonfatal", got)
	}
	if got := recoveryFatal(t.Context(), ErrNotFound); got != nil {
		t.Fatalf("missing derived generation = %v, want nonfatal", got)
	}
	joined := errors.Join(ErrInvalid, sentinel)
	if got := recoveryFatal(t.Context(), joined); !errors.Is(got, sentinel) {
		t.Fatalf("joined operational recovery error = %v, want %v", got, sentinel)
	}
}

func TestRecoverAllReturnsPreCanceledContextBeforeDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report, err := RecoverAll(ctx, t.TempDir(), &fakeRecoveryPinsV3{})
	if !errors.Is(err, context.Canceled) || report != (RecoveryReport{}) {
		t.Fatalf("pre-canceled recovery = %+v, %v", report, err)
	}
}

func TestRecoverAllRejectsNoncanonicalV3Current(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	current := filepath.Join(publication.base, "current.json")
	raw, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}
	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if err != nil || report.Invalid == 0 || pins.reconciles != 0 ||
		pins.catalogReconciles != 0 || pins.relationshipPins != 0 {
		t.Fatalf("noncanonical current = %+v pins=%+v err=%v", report, pins, err)
	}
}

func TestRecoverAllRejectsNoncanonicalV3MarkerWithoutSwap(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	pointer, err := newPointerV3(publication.Root())
	if err != nil {
		t.Fatal(err)
	}
	marker, _, _, err := publicationControlsV3(pointer, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(publication.base, "current.json")); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(publication.base, "publishing.json")
	if err := replaceFile(markerPath, raw); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}
	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if err != nil || report.Invalid == 0 || pins.reconciles != 0 ||
		pins.catalogReconciles != 0 || pins.relationshipPins != 0 {
		t.Fatalf("noncanonical marker = %+v pins=%+v err=%v", report, pins, err)
	}
	if _, err := os.Lstat(filepath.Join(publication.base, "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("noncanonical marker swapped current: %v", err)
	}
	if _, err := os.Lstat(markerPath); err != nil {
		t.Fatalf("noncanonical marker removed: %v", err)
	}
}

func TestRecoverAllCountsMissingV3GenerationAsCorrupt(t *testing.T) {
	fixture := newArchiveV3Fixture(t)
	publication := fixture.publishV3(t)
	if err := os.Remove(filepath.Join(publication.directory, "root.json")); err != nil {
		t.Fatal(err)
	}
	pins := &fakeRecoveryPinsV3{}
	report, err := RecoverAll(t.Context(), fixture.dataDir, pins)
	if err != nil || report.Invalid == 0 || pins.reconciles != 0 ||
		pins.catalogReconciles != 0 {
		t.Fatalf("missing v3 generation = %+v pins=%+v err=%v", report, pins, err)
	}
}

func publishRecoveryLegacyGeneration(
	t *testing.T,
	root, repository, suffix, runID string,
) Root {
	t.Helper()
	upstream := lifecycleUpstreamV3(t, repository, runID)
	authority := testAuthority(t, repository)
	authority.ServiceStateSummaryDigest = fixedDigest("d")
	authority.ServiceStateControlRevision = 9
	authority.ObservationGenerationDigest = upstream.Observation.ObservationGenerationDigest
	authority.ObservationManifestDigest = upstream.Observation.ObservationRootDigest
	authority.ObservationSourceDigest = upstream.Observation.SourceRootDigest
	authority.ResolverGenerationDigest = fixedDigest(suffix)
	authority.Upstream = &upstream
	prepared, err := writePublicationStageVersioned(
		t.Context(), root, RootSchemaV2, authority, mustDigest(t, authority), nil,
		&buildAccumulator{
			repository: map[int][]Projection{}, services: map[string]*serviceAccumulator{},
			seen: map[string]struct{}{}, serviceRefLimit: MaxServiceReferences,
			totalRefLimit: MaxTotalServiceReferences, residentLimit: MaxResidentChargeBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := prepared.Publish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return publication.Root()
}
